package autoping

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	scannerStartupDelay = 3 * time.Second
	maxSleepInterval    = 15 * time.Second
)

type Options struct {
	Now          func() time.Time
	StartupDelay *time.Duration
}

type Runtime struct {
	host     Host
	now      func() time.Time
	manifest Manifest

	configMu      sync.Mutex
	mu            sync.RWMutex
	config        Config
	store         *StateStore
	scannerCancel context.CancelFunc
	shutdown      bool

	scannerWG sync.WaitGroup
	inFlight  sync.Map

	sessionMu  sync.Mutex
	sessions   map[string]*fallbackSession
	boostLocks sync.Map

	startupDelay time.Duration
}

func NewRuntime(host Host, manifestData []byte, options Options) (*Runtime, error) {
	manifest, err := ParseManifest(manifestData)
	if err != nil {
		return nil, err
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	startupDelay := scannerStartupDelay
	if options.StartupDelay != nil {
		startupDelay = *options.StartupDelay
	}
	return &Runtime{
		host:         host,
		now:          now,
		manifest:     manifest,
		config:       manifest.Defaults,
		sessions:     map[string]*fallbackSession{},
		startupDelay: startupDelay,
	}, nil
}

func (r *Runtime) Configure(ctx context.Context, cfg Config) error {
	r.configMu.Lock()
	defer r.configMu.Unlock()

	r.mu.RLock()
	currentStore := r.store
	isShutdown := r.shutdown
	r.mu.RUnlock()

	if isShutdown {
		return errors.New("runtime is shut down")
	}

	store := currentStore
	if store == nil || store.Path() != cfg.StatePath {
		loaded, err := LoadStateStore(ctx, cfg.StatePath)
		if err != nil {
			return err
		}
		store = loaded
	}

	r.mu.Lock()
	if r.shutdown {
		r.mu.Unlock()
		return errors.New("runtime is shut down")
	}
	oldCancel := r.scannerCancel
	r.mu.Unlock()

	if oldCancel != nil {
		oldCancel()
		waitDone := make(chan struct{})
		go func() {
			r.scannerWG.Wait()
			close(waitDone)
		}()
		select {
		case <-ctx.Done():
			r.mu.Lock()
			r.config.AutoPingEnabled = false
			r.mu.Unlock()
			return ctx.Err()
		case <-waitDone:
			r.mu.Lock()
			r.scannerCancel = nil
			r.mu.Unlock()
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.shutdown {
		return errors.New("runtime is shut down")
	}
	marginChanged := r.config.AntigravityResetMargin != 0 && r.config.AntigravityResetMargin != cfg.AntigravityResetMargin
	r.config = cfg
	r.store = store
	if marginChanged {
		for _, cred := range store.Accounts() {
			if cred.Provider == ProviderAntigravity && len(cred.Targets) > 0 {
				if err := store.Update(ctx, cred.CredentialID, func(current *CredentialState) {
					for k, tgt := range current.Targets {
						if tgt.Status == "waiting" && !tgt.ObservedResetAt.IsZero() {
							tgt.TargetTriggerAt = tgt.ObservedResetAt.Add(cfg.AntigravityResetMargin)
							current.Targets[k] = tgt
						}
					}
				}); err != nil {
					return fmt.Errorf("recalculate Antigravity margins: %w", err)
				}
			}
		}
	}
	if cfg.AutoPingEnabled {
		scannerCtx, cancel := context.WithCancel(context.Background())
		r.scannerCancel = cancel
		r.scannerWG.Go(func() { r.scheduleLoop(scannerCtx) })
	}
	return nil
}

func (r *Runtime) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	if r.shutdown {
		r.mu.Unlock()
		return nil
	}
	r.shutdown = true
	oldCancel := r.scannerCancel
	r.scannerCancel = nil
	r.mu.Unlock()

	if oldCancel != nil {
		oldCancel()
	}
	done := make(chan struct{})
	go func() {
		r.scannerWG.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (r *Runtime) scheduleLoop(ctx context.Context) {
	r.runDynamicSchedule(ctx)
}

func (r *Runtime) sleepUntil(ctx context.Context, target time.Time) bool {
	for r.now().Before(target) {
		remaining := target.Sub(r.now())
		if remaining <= 0 {
			break
		}
		interval := min(remaining, maxSleepInterval)
		if !sleepContext(ctx, interval) {
			return false
		}
	}
	return true
}

func (r *Runtime) snapshot() (Config, *StateStore, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.config, r.store, !r.shutdown && r.host != nil && r.store != nil
}

func (r *Runtime) credentialMaterial(ctx context.Context, file AuthFile) (AuthDocument, AuthMaterial, error) {
	if file.AuthIndex == "" {
		return AuthDocument{}, AuthMaterial{}, errors.New("auth index missing")
	}
	document, err := r.host.GetAuth(ctx, file.AuthIndex)
	if err != nil {
		return AuthDocument{}, AuthMaterial{}, err
	}
	material, err := ParseAuthMaterial(document.JSON)
	if err != nil {
		return AuthDocument{}, AuthMaterial{}, err
	}
	return document, material, nil
}

func isCodex(file AuthFile) bool {
	provider := strings.ToLower(firstNonBlank(file.Provider, file.Type))
	return provider == "codex" || strings.Contains(provider, "codex")
}

func isAntigravity(file AuthFile) bool {
	provider := strings.ToLower(firstNonBlank(file.Provider, file.Type))
	return provider == "antigravity" || strings.Contains(provider, "antigravity")
}

func ineligibleReason(cfg Config, file AuthFile) string {
	switch {
	case cfg.Excludes(file.CredentialID()):
		return "excluded"
	case file.Disabled:
		return "disabled"
	case strings.EqualFold(file.Status, "disabled"), strings.EqualFold(file.Status, "revoked"):
		return strings.ToLower(file.Status)
	default:
		return ""
	}
}

func failureStatus(failure FailureKind) string {
	if failure == FailureAuth {
		return "blocked"
	}
	return "cooldown"
}

func safeErrorMessage(message string) string {
	lowered := strings.ToLower(message)
	for _, marker := range []string{"authorization", "bearer ", "access_token", "refresh_token", "api_key", "cookie"} {
		if strings.Contains(lowered, marker) {
			return "sensitive upstream error redacted"
		}
	}
	return strings.TrimSpace(message)
}

func (r *Runtime) log(ctx context.Context, level, message string, fields map[string]any) {
	if r.host != nil {
		_ = r.host.Log(ctx, LogRequest{Level: level, Message: message, Fields: fields})
	}
}

func (r *Runtime) ManualPing(ctx context.Context, request ManualPingRequest) (ManualPingResponse, error) {
	cfg, store, ok := r.snapshot()
	if !ok {
		return ManualPingResponse{}, errors.New("runtime unavailable")
	}
	files, err := r.host.ListAuth(ctx)
	if err != nil {
		return ManualPingResponse{}, err
	}
	file, found := findCredential(files, strings.TrimSpace(request.CredentialID))
	if !found || (!isCodex(file) && !isAntigravity(file)) {
		return ManualPingResponse{}, errors.New("Codex credential not found")
	}
	credentialID := file.CredentialID()
	if reason := ineligibleReason(cfg, file); reason != "" {
		return ManualPingResponse{}, fmt.Errorf("credential is not eligible: %s", reason)
	}
	done := make(chan struct{})
	if _, loaded := r.inFlight.LoadOrStore(credentialID, done); loaded {
		return ManualPingResponse{}, errors.New("credential already has an in-flight ping")
	}
	defer func() {
		r.inFlight.Delete(credentialID)
		close(done)
	}()

	if isAntigravity(file) {
		return r.manualPingAntigravity(ctx, cfg, store, file, request)
	}
	_, material, err := r.credentialMaterial(ctx, file)
	if err != nil {
		return ManualPingResponse{}, err
	}
	if model := strings.TrimSpace(request.Model); model != "" {
		cfg.Model = model
	}
	result := r.activate(ctx, cfg, file, material)
	completedAt := r.now().UTC()
	priorReset := store.Credential(credentialID).ObservedResetAt
	var anchoredObs Observation
	var anchorFailure FailureKind
	var anchorMessage string
	if result.Success && request.MarkCycleProcessed {
		anchoredObs, anchorFailure, anchorMessage = r.fetchUsageObservation(ctx, cfg, material)
		if anchorFailure == FailureNone {
			if !priorReset.IsZero() && !anchoredObs.ResetAt.After(priorReset) {
				anchorFailure = FailureRetryable
				anchorMessage = "usage reset_at did not advance after manual ping"
			} else if priorReset.IsZero() && !anchoredObs.ResetAt.After(completedAt) {
				anchorFailure = FailureRetryable
				anchorMessage = "usage reset_at is not in the future of completed manual ping"
			}
		}
	}

	persistErr := store.Update(ctx, credentialID, func(current *CredentialState) {
		current.LastAttemptAt = completedAt
		current.SelectedModel = result.Model
		current.Transport = result.Transport
		current.Attempts++
		if !result.Success {
			current.LastAttemptStatus = "failed"
			current.LastError = safeErrorMessage(result.Message)
			current.Failures++
			return
		}

		current.LastAttemptStatus = "success"
		current.LastPingAt = completedAt
		current.LastError = ""
		current.Successes++
		if !request.MarkCycleProcessed {
			return
		}
		if anchorFailure != FailureNone {
			current.Status = "stabilizing"
			current.Reason = "usage_stabilization_retry"
			current.TargetTriggerAt = time.Time{}
			current.FailureKind = string(anchorFailure)
			current.LastError = safeErrorMessage(anchorMessage)
			if terminalDynamicFailure(anchorFailure) {
				current.NextRetryAt = time.Time{}
			} else {
				current.NextRetryAt = r.now().Add(cfg.RetryCooldown)
			}
			return
		}
		current.LastProcessedResetAt = priorReset
		current.ObservedResetAt = anchoredObs.ResetAt
		current.TargetTriggerAt = nextTargetAfterPing(completedAt, anchoredObs.ResetAt, cfg.Schedule, cfg.Location)
		current.Status = "waiting"
		current.Reason = "trigger_scheduled"
		current.FailureKind = ""
		current.NextRetryAt = time.Time{}
	})
	if persistErr != nil {
		message := fmt.Sprintf("persist manual ping state: %v", persistErr)
		return ManualPingResponse{Success: false, CredentialID: credentialID, Model: result.Model, Transport: result.Transport, Error: message}, errors.New(message)
	}
	if !result.Success {
		return ManualPingResponse{Success: false, CredentialID: credentialID, Model: result.Model, Transport: result.Transport, Error: result.Message}, errors.New(result.Message)
	}
	if anchorFailure != FailureNone {
		message := "manual ping succeeded but quota anchor failed: " + anchorMessage
		return ManualPingResponse{Success: false, CredentialID: credentialID, Model: result.Model, Transport: result.Transport, Error: message}, errors.New(message)
	}
	return ManualPingResponse{Success: true, CredentialID: credentialID, Model: result.Model, Transport: result.Transport}, nil
}
func (r *Runtime) manualPingAntigravity(ctx context.Context, cfg Config, store *StateStore, file AuthFile, request ManualPingRequest) (ManualPingResponse, error) {
	credentialID := file.CredentialID()
	var targetsToRun []struct {
		model  string
		key    string
		family string
	}

	modelInput := strings.TrimSpace(request.Model)
	if modelInput != "" {
		switch modelInput {
		case AntigravityModelGemini:
			targetsToRun = append(targetsToRun, struct {
				model  string
				key    string
				family string
			}{AntigravityModelGemini, TargetKeyGemini5h, "gemini"})
		case AntigravityModelClaude:
			targetsToRun = append(targetsToRun, struct {
				model  string
				key    string
				family string
			}{AntigravityModelClaude, TargetKeyClaude5h, "claude"})
		default:
			return ManualPingResponse{
				Success:      false,
				CredentialID: credentialID,
				Model:        modelInput,
				Error:        fmt.Sprintf("unsupported Antigravity model: %s, want %s or %s", modelInput, AntigravityModelGemini, AntigravityModelClaude),
			}, fmt.Errorf("unsupported Antigravity model: %s, want %s or %s", modelInput, AntigravityModelGemini, AntigravityModelClaude)
		}
	} else {
		targetsToRun = append(targetsToRun,
			struct {
				model  string
				key    string
				family string
			}{AntigravityModelGemini, TargetKeyGemini5h, "gemini"},
			struct {
				model  string
				key    string
				family string
			}{AntigravityModelClaude, TargetKeyClaude5h, "claude"},
		)
	}

	results := make([]ManualPingTargetResult, 0, len(targetsToRun))
	hasSuccess := false
	hasFailure := false
	var firstError string

	for _, target := range targetsToRun {
		completedAt := r.now().UTC()
		body, err := json.Marshal(map[string]any{
			"model": target.model,
			"input": cfg.Prompt,
			"store": false,
		})
		if err != nil {
			msg := safeErrorMessage(err.Error())
			results = append(results, ManualPingTargetResult{
				Target:      target.key,
				ModelFamily: target.family,
				Model:       target.model,
				Success:     false,
				Transport:   TransportHostModel,
				Error:       msg,
			})
			hasFailure = true
			if firstError == "" {
				firstError = msg
			}
			continue
		}

		requestCtx, cancel := context.WithTimeoutCause(ctx, cfg.RequestTimeout, errors.New("Antigravity request timeout"))
		resp, execErr := r.host.ModelExecute(requestCtx, ModelExecuteRequest{
			EntryProtocol:  "openai-response",
			ExitProtocol:   "openai-response",
			Model:          target.model,
			Stream:         false,
			Body:           body,
			ForcedProvider: ProviderAntigravity,
			AuthID:         credentialID,
		})
		var success bool
		var failure FailureKind
		var message string
		if execErr != nil {
			failure = FailureTransport
			if errors.Is(execErr, context.DeadlineExceeded) || errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
				failure = FailureTimeout
			}
			message = execErr.Error()
		} else {
			success, failure, message = evaluateAntigravityResponse(resp.StatusCode, resp.Body)
		}
		cancel()
		completedAt = r.now().UTC()
		sanitizedErr := safeErrorMessage(message)

		retryDelay := cfg.RetryCooldown
		if resp.Headers != nil {
			if parsedDelay := parseRetryAfter(resp.Headers.Get("Retry-After"), r.now()); parsedDelay != nil {
				retryDelay = *parsedDelay
			}
		}

		if failure == FailureAuth {
			hasFailure = true
			if firstError == "" {
				firstError = sanitizedErr
			}
			persistErr := store.Update(ctx, credentialID, func(current *CredentialState) {
				current.Provider = ProviderAntigravity
				current.Status = "blocked"
				current.FailureKind = string(FailureAuth)
				current.LastError = sanitizedErr
				current.LastAttemptAt = completedAt
				current.Failures++
				if current.Targets == nil {
					current.Targets = make(map[string]TargetState)
				}
				for _, tInfo := range targetsToRun {
					tgt := current.Targets[tInfo.key]
					tgt.Target = tInfo.key
					tgt.ModelFamily = tInfo.family
					tgt.SelectedModel = tInfo.model
					tgt.Transport = TransportHostModel
					tgt.Status = "blocked"
					tgt.FailureKind = string(FailureAuth)
					tgt.LastError = sanitizedErr
					tgt.LastAttemptAt = completedAt
					tgt.Failures++
					current.Targets[tInfo.key] = tgt
				}
			})
			results = append(results, ManualPingTargetResult{
				Target:      target.key,
				ModelFamily: target.family,
				Model:       target.model,
				Success:     false,
				Transport:   TransportHostModel,
				Error:       sanitizedErr,
			})
			if persistErr != nil {
				errMsg := fmt.Sprintf("persist manual ping state: %v", persistErr)
				return ManualPingResponse{
					Success:      false,
					CredentialID: credentialID,
					Model:        request.Model,
					Transport:    TransportHostModel,
					Error:        errMsg,
					Results:      results,
				}, errors.New(errMsg)
			}
			break
		}

		if success {
			hasSuccess = true
		} else {
			hasFailure = true
			if firstError == "" {
				firstError = sanitizedErr
			}
		}

		persistErr := store.Update(ctx, credentialID, func(current *CredentialState) {
			current.Provider = ProviderAntigravity
			if current.Targets == nil {
				current.Targets = make(map[string]TargetState)
			}
			tgt := current.Targets[target.key]
			tgt.Target = target.key
			tgt.ModelFamily = target.family
			tgt.SelectedModel = target.model
			tgt.Transport = TransportHostModel
			tgt.LastAttemptAt = completedAt
			tgt.Attempts++
			if success {
				tgt.LastAttemptStatus = "success"
				tgt.LastPingAt = completedAt
				tgt.LastError = ""
				tgt.FailureKind = ""
				tgt.Successes++
			} else {
				tgt.LastAttemptStatus = "failed"
				tgt.LastError = sanitizedErr
				tgt.FailureKind = string(failure)
				tgt.Failures++
				tgt.NextRetryAt = r.now().Add(retryDelay)
			}
			current.Targets[target.key] = tgt
		})
		if persistErr != nil {
			errMsg := fmt.Sprintf("persist manual ping state: %v", persistErr)
			results = append(results, ManualPingTargetResult{
				Target:      target.key,
				ModelFamily: target.family,
				Model:       target.model,
				Success:     false,
				Transport:   TransportHostModel,
				Error:       errMsg,
			})
			return ManualPingResponse{
				Success:      false,
				CredentialID: credentialID,
				Model:        request.Model,
				Transport:    TransportHostModel,
				Error:        errMsg,
				Results:      results,
			}, errors.New(errMsg)
		}
		results = append(results, ManualPingTargetResult{
			Target:      target.key,
			ModelFamily: target.family,
			Model:       target.model,
			Success:     success,
			Transport:   TransportHostModel,
			Error:       sanitizedErr,
		})
	}

	if request.MarkCycleProcessed && hasSuccess {
		obs, anchorFailure, anchorMessage, qRetryAfter := r.fetchAntigravityQuotaObservation(ctx, cfg, file)
		anchorErr := make(map[string]string)
		anchorFailureKind := make(map[string]FailureKind)
		anchorResets := make(map[string]time.Time)

		if anchorFailure == FailureAuth {
			_ = store.Update(ctx, credentialID, func(current *CredentialState) {
				current.Provider = ProviderAntigravity
				current.Status = "blocked"
				current.FailureKind = string(FailureAuth)
				current.LastError = safeErrorMessage(anchorMessage)
				current.LastAttemptAt = r.now().UTC()
				current.Failures++
				for _, res := range results {
					tgt := current.Targets[res.Target]
					tgt.Status = "blocked"
					tgt.FailureKind = string(FailureAuth)
					tgt.LastError = safeErrorMessage(anchorMessage)
					current.Targets[res.Target] = tgt
				}
			})
		}

		for _, res := range results {
			if !res.Success {
				continue
			}
			if anchorFailure != FailureNone {
				anchorErr[res.Target] = anchorMessage
				anchorFailureKind[res.Target] = anchorFailure
			} else {
				reset, ok := obs.ResetForTarget(res.Target)
				if !ok {
					anchorErr[res.Target] = fmt.Sprintf("no 5-hour quota bucket returned for %s", res.Target)
					anchorFailureKind[res.Target] = FailureRetryable
				} else if !reset.After(r.now().UTC()) {
					anchorErr[res.Target] = "quota reset_at is not in the future of completed manual ping"
					anchorFailureKind[res.Target] = FailureRetryable
				} else {
					anchorResets[res.Target] = reset
				}
			}
		}

		qDelay := cfg.RetryCooldown
		if qRetryAfter != nil {
			qDelay = *qRetryAfter
		}

		persistErr := store.Update(ctx, credentialID, func(current *CredentialState) {
			current.Provider = ProviderAntigravity
			if current.Targets == nil {
				current.Targets = make(map[string]TargetState)
			}
			for _, res := range results {
				if !res.Success {
					continue
				}
				tgt := current.Targets[res.Target]
				if errStr, hasErr := anchorErr[res.Target]; hasErr {
					tgt.Status = "stabilizing"
					tgt.Reason = "usage_stabilization_retry"
					tgt.TargetTriggerAt = time.Time{}
					tgt.FailureKind = string(anchorFailureKind[res.Target])
					tgt.LastError = safeErrorMessage(errStr)
					tgt.NextRetryAt = r.now().Add(qDelay)
				} else {
					tgt.Status = "waiting"
					tgt.Reason = "trigger_scheduled"
					tgt.ObservedResetAt = anchorResets[res.Target]
					tgt.TargetTriggerAt = anchorResets[res.Target].Add(cfg.AntigravityResetMargin)
					tgt.FailureKind = ""
					tgt.LastError = ""
					tgt.NextRetryAt = time.Time{}
				}
				current.Targets[res.Target] = tgt
			}
		})
		if persistErr != nil {
			errMsg := fmt.Sprintf("persist marked manual ping state: %v", persistErr)
			return ManualPingResponse{
				Success:      false,
				CredentialID: credentialID,
				Model:        request.Model,
				Transport:    TransportHostModel,
				Error:        errMsg,
				Results:      results,
			}, errors.New(errMsg)
		}

		for targetKey, errStr := range anchorErr {
			hasFailure = true
			msg := "manual ping succeeded but quota anchor failed: " + errStr
			if firstError == "" {
				firstError = msg
			}
			for i := range results {
				if results[i].Target == targetKey {
					results[i].Success = false
					results[i].Error = msg
				}
			}
		}
	}

	overallSuccess := hasSuccess && !hasFailure
	output := ManualPingResponse{
		Success:      overallSuccess,
		CredentialID: credentialID,
		Model:        request.Model,
		Transport:    TransportHostModel,
		Results:      results,
	}
	if !overallSuccess {
		output.Error = firstError
		return output, errors.New(firstError)
	}
	return output, nil
}

type ManualPingTargetResult struct {
	Target      string `json:"target,omitempty"`
	ModelFamily string `json:"model_family,omitempty"`
	Model       string `json:"model,omitempty"`
	Success     bool   `json:"success"`
	Transport   string `json:"transport,omitempty"`
	Error       string `json:"error,omitempty"`
}

type ManualPingRequest struct {
	CredentialID       string `json:"credential_id"`
	Model              string `json:"model,omitempty"`
	MarkCycleProcessed bool   `json:"mark_cycle_processed,omitzero"`
}

type ManualPingResponse struct {
	Success      bool                     `json:"success"`
	CredentialID string                   `json:"credential_id"`
	Model        string                   `json:"model,omitempty"`
	Transport    string                   `json:"transport,omitempty"`
	Error        string                   `json:"error,omitempty"`
	Results      []ManualPingTargetResult `json:"results,omitempty"`
}
