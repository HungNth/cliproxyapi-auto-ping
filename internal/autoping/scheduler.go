package autoping

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	fallbackNonceHeader = "X-Auto-Ping-Nonce"
	priorityBoostFloor  = 1000
)

type fallbackSession struct {
	CredentialID string
	ExpiresAt    time.Time
	Selected     bool
	SelectionErr string
}

type schedulerResponse struct {
	Handled bool   `json:"Handled"`
	AuthID  string `json:"AuthID,omitempty"`
	Reason  string `json:"Reason,omitempty"`
}

type schedulerRequest struct {
	Candidates []schedulerCandidate `json:"Candidates"`
	Headers    http.Header          `json:"Headers"`
	Options    struct {
		Headers http.Header `json:"Headers"`
	} `json:"Options"`
}

type schedulerCandidate struct {
	ID             string `json:"ID"`
	IDLower        string `json:"id"`
	AuthID         string `json:"AuthID"`
	AuthIDLower    string `json:"auth_id"`
	AuthIndex      string `json:"AuthIndex"`
	AuthIndexLower string `json:"auth_index"`
}

func (r *Runtime) schedulerPick(data []byte) (schedulerResponse, error) {
	var request schedulerRequest
	if len(data) > 0 {
		if err := json.Unmarshal(data, &request); err != nil {
			return schedulerResponse{}, fmt.Errorf("decode scheduler request: %w", err)
		}
	}
	headers := request.Headers
	if len(request.Options.Headers) > 0 {
		headers = request.Options.Headers
	}
	nonce := headerValue(headers, fallbackNonceHeader)
	if nonce == "" {
		return schedulerResponse{Reason: "nonce_missing"}, nil
	}

	r.sessionMu.Lock()
	session, ok := r.sessions[nonce]
	if !ok || !r.now().Before(session.ExpiresAt) {
		delete(r.sessions, nonce)
		r.sessionMu.Unlock()
		return schedulerResponse{Reason: "nonce_unknown"}, nil
	}
	target := session.CredentialID
	found := false
	for _, candidate := range request.Candidates {
		if firstNonBlank(candidate.ID, candidate.IDLower, candidate.AuthID, candidate.AuthIDLower, candidate.AuthIndex, candidate.AuthIndexLower) == target {
			found = true
			break
		}
	}
	if !found {
		session.SelectionErr = "target credential was not offered to the scheduler"
		r.sessionMu.Unlock()
		return schedulerResponse{}, errors.New(session.SelectionErr)
	}
	session.Selected = true
	r.sessionMu.Unlock()
	return schedulerResponse{Handled: true, AuthID: target, Reason: "target_confirmed"}, nil
}

func (r *Runtime) schedulerActivate(ctx context.Context, cfg Config, file AuthFile, model string) ActivationResult {
	restore, err := r.boostCredential(ctx, file)
	if err != nil {
		return ActivationResult{Model: model, Transport: TransportSchedulerBoost, Failure: FailureTransport, Message: "scheduler priority boost failed"}
	}
	defer restore(context.Background())

	nonce, err := r.createFallbackSession(file.CredentialID())
	if err != nil {
		return ActivationResult{Model: model, Transport: TransportSchedulerBoost, Failure: FailureTransport, Message: "scheduler session creation failed"}
	}
	defer r.deleteFallbackSession(nonce)

	body, err := json.Marshal(map[string]any{
		"model": model,
		"input": cfg.Prompt,
		"store": false,
	})
	if err != nil {
		return ActivationResult{Model: model, Transport: TransportSchedulerBoost, Failure: FailureBusiness, Message: "encode scheduler request failed"}
	}
	requestCtx, cancel := context.WithTimeoutCause(ctx, cfg.RequestTimeout, errors.New("scheduler request timeout"))
	defer cancel()
	response, err := r.host.ModelExecute(requestCtx, ModelExecuteRequest{
		EntryProtocol: "openai-response",
		ExitProtocol:  "openai-response",
		Model:         model,
		Stream:        false,
		Body:          body,
		Headers:       http.Header{fallbackNonceHeader: {nonce}},
	})
	selected, selectionErr := r.fallbackSelection(nonce)
	if !selected {
		if selectionErr == "" {
			selectionErr = "scheduler did not confirm the target credential"
		}
		return ActivationResult{Model: model, Transport: TransportSchedulerBoost, Failure: FailureTransport, Message: selectionErr}
	}
	if err != nil {
		failure := FailureTransport
		if errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			failure = FailureTimeout
		}
		return ActivationResult{Model: model, Transport: TransportSchedulerBoost, Failure: failure, Message: "scheduler model execution failed"}
	}
	success, failure, message := evaluateCodexResponse(response.StatusCode, response.Body)
	return ActivationResult{
		Success: success, Model: model, Transport: TransportSchedulerBoost,
		StatusCode: response.StatusCode, Failure: failure, Message: message,
	}
}

func (r *Runtime) createFallbackSession(credentialID string) (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	nonce := hex.EncodeToString(random)
	r.sessionMu.Lock()
	defer r.sessionMu.Unlock()
	now := r.now()
	for key, session := range r.sessions {
		if !now.Before(session.ExpiresAt) {
			delete(r.sessions, key)
		}
	}
	r.sessions[nonce] = &fallbackSession{CredentialID: credentialID, ExpiresAt: now.Add(10 * time.Minute)}
	return nonce, nil
}

func (r *Runtime) fallbackSelection(nonce string) (bool, string) {
	r.sessionMu.Lock()
	defer r.sessionMu.Unlock()
	session := r.sessions[nonce]
	if session == nil {
		return false, "scheduler session expired"
	}
	return session.Selected, session.SelectionErr
}

func (r *Runtime) deleteFallbackSession(nonce string) {
	r.sessionMu.Lock()
	delete(r.sessions, nonce)
	r.sessionMu.Unlock()
}

func headerValue(headers http.Header, name string) string {
	for key, values := range headers {
		if !strings.EqualFold(key, name) {
			continue
		}
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func (r *Runtime) boostCredential(ctx context.Context, target AuthFile) (func(context.Context), error) {
	provider := strings.ToLower(firstNonBlank(target.Provider, target.Type))
	lock := r.providerLock(provider)
	lock.Lock()
	release := sync.OnceFunc(lock.Unlock)
	noop := func(context.Context) { release() }

	files, err := r.host.ListAuth(ctx)
	if err != nil {
		release()
		return func(context.Context) {}, err
	}
	target, ok := findCredential(files, target.CredentialID())
	if !ok {
		release()
		return func(context.Context) {}, errors.New("target credential not found")
	}
	maximum, peers := providerPriorityStats(files, provider)
	if target.Priority == maximum && peers == 1 {
		return noop, nil
	}
	if target.AuthIndex == "" {
		release()
		return func(context.Context) {}, errors.New("target credential has no auth index")
	}
	document, err := r.host.GetAuth(ctx, target.AuthIndex)
	if err != nil || len(document.JSON) == 0 || document.Name == "" {
		release()
		return func(context.Context) {}, errors.New("target credential document unavailable")
	}
	originalPriority := priorityFromJSON(document.JSON, target.Priority)
	boostedPriority := max(maximum+1, priorityBoostFloor)
	boosted, err := withPriority(document.JSON, boostedPriority)
	if err != nil {
		release()
		return func(context.Context) {}, err
	}
	if err := r.host.SaveAuth(ctx, document.Name, boosted); err != nil {
		release()
		return func(context.Context) {}, err
	}
	if err := r.confirmCredentialPriority(ctx, target, provider, boostedPriority); err != nil {
		if restored, restoreErr := withPriority(document.JSON, originalPriority); restoreErr == nil {
			_ = r.host.SaveAuth(context.Background(), document.Name, restored)
		}
		release()
		return func(context.Context) {}, err
	}

	return func(restoreCtx context.Context) {
		defer release()
		latest, getErr := r.host.GetAuth(restoreCtx, target.AuthIndex)
		if getErr != nil || len(latest.JSON) == 0 {
			return
		}
		restored, patchErr := withPriority(latest.JSON, originalPriority)
		if patchErr != nil {
			return
		}
		_ = r.host.SaveAuth(restoreCtx, firstNonBlank(latest.Name, document.Name), restored)
	}, nil
}

func (r *Runtime) providerLock(provider string) *sync.Mutex {
	value, _ := r.boostLocks.LoadOrStore(provider, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func (r *Runtime) confirmCredentialPriority(ctx context.Context, target AuthFile, provider string, priority int) error {
	for range 3 {
		runtimeFile, runtimeErr := r.host.GetRuntimeAuth(ctx, target.AuthIndex)
		files, listErr := r.host.ListAuth(ctx)
		if runtimeErr == nil && listErr == nil && runtimeFile.Priority >= priority && uniqueProviderTop(files, target.CredentialID(), provider, priority) {
			return nil
		}
		if !sleepContext(ctx, 50*time.Millisecond) {
			break
		}
	}
	return errors.New("target credential priority was not confirmed")
}

func providerPriorityStats(files []AuthFile, provider string) (maximum, peers int) {
	found := false
	for _, file := range files {
		if !strings.EqualFold(firstNonBlank(file.Provider, file.Type), provider) {
			continue
		}
		if !found || file.Priority > maximum {
			maximum = file.Priority
			peers = 1
			found = true
		} else if file.Priority == maximum {
			peers++
		}
	}
	return maximum, peers
}

func uniqueProviderTop(files []AuthFile, credentialID, provider string, priority int) bool {
	targetSeen := false
	for _, file := range files {
		if !strings.EqualFold(firstNonBlank(file.Provider, file.Type), provider) {
			continue
		}
		isTarget := file.CredentialID() == credentialID
		if file.Priority > priority || file.Priority == priority && !isTarget {
			return false
		}
		targetSeen = targetSeen || isTarget && file.Priority == priority
	}
	return targetSeen
}

func findCredential(files []AuthFile, credentialID string) (AuthFile, bool) {
	for _, file := range files {
		if file.CredentialID() == credentialID || file.ID == credentialID || file.Name == credentialID || file.AuthIndex == credentialID {
			return file, true
		}
	}
	return AuthFile{}, false
}

func priorityFromJSON(data []byte, fallback int) int {
	var document map[string]json.RawMessage
	if json.Unmarshal(data, &document) != nil {
		return fallback
	}
	var priority int
	if json.Unmarshal(document["priority"], &priority) != nil {
		return fallback
	}
	return priority
}

func withPriority(data []byte, priority int) ([]byte, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil || len(document) == 0 {
		return nil, errors.New("credential document is invalid")
	}
	encoded, _ := json.Marshal(priority)
	document["priority"] = encoded
	return json.Marshal(document)
}

func sleepContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
