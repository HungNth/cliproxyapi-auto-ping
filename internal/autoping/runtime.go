package autoping

import (
	"context"
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
	r.config = cfg
	r.store = store
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
	if !found || !isCodex(file) {
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

type ManualPingRequest struct {
	CredentialID       string `json:"credential_id"`
	Model              string `json:"model,omitempty"`
	MarkCycleProcessed bool   `json:"mark_cycle_processed,omitzero"`
}

type ManualPingResponse struct {
	Success      bool   `json:"success"`
	CredentialID string `json:"credential_id"`
	Model        string `json:"model,omitempty"`
	Transport    string `json:"transport,omitempty"`
	Error        string `json:"error,omitempty"`
}
