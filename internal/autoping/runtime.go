package autoping

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const scannerStartupDelay = 3 * time.Second

type Options struct {
	Now          func() time.Time
	StartupDelay *time.Duration
}

type Runtime struct {
	host     Host
	now      func() time.Time
	manifest Manifest

	mu            sync.RWMutex
	config        Config
	store         *StateStore
	scannerCancel context.CancelFunc
	shutdown      bool

	scannerWG   sync.WaitGroup
	scanRunning atomic.Bool
	inFlight    sync.Map

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
	r.mu.RLock()
	currentStore := r.store
	r.mu.RUnlock()
	store := currentStore
	if store == nil || store.Path() != cfg.StatePath {
		loaded, err := LoadStateStore(ctx, cfg.StatePath)
		if err != nil {
			return err
		}
		store = loaded
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.shutdown {
		return errors.New("runtime is shut down")
	}
	r.stopScannerLocked()
	r.config = cfg
	r.store = store
	if cfg.AutoPingEnabled {
		scannerCtx, cancel := context.WithCancel(context.Background())
		r.scannerCancel = cancel
		r.scannerWG.Go(func() { r.scanLoop(scannerCtx, cfg.ScanInterval) })
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
	r.stopScannerLocked()
	r.mu.Unlock()

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

func (r *Runtime) stopScannerLocked() {
	if r.scannerCancel != nil {
		r.scannerCancel()
		r.scannerCancel = nil
	}
}

func (r *Runtime) scanLoop(ctx context.Context, interval time.Duration) {
	if !sleepContext(ctx, r.startupDelay) {
		return
	}
	_ = r.ScanOnce(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = r.ScanOnce(ctx)
		}
	}
}

func (r *Runtime) ScanOnce(ctx context.Context) error {
	cfg, store, ok := r.snapshot()
	if !ok || !cfg.AutoPingEnabled {
		return nil
	}
	if !r.scanRunning.CompareAndSwap(false, true) {
		return nil
	}
	defer r.scanRunning.Store(false)

	files, err := r.host.ListAuth(ctx)
	if err != nil {
		r.log(ctx, "warn", "auto-ping credential scan failed", map[string]any{"reason": "credential_list_failed"})
		return err
	}
	workers := min(cfg.MaxConcurrency, len(files))
	if workers == 0 {
		return nil
	}
	jobs := make(chan AuthFile, len(files))
	var workersWG sync.WaitGroup
	for range workers {
		workersWG.Go(func() {
			for file := range jobs {
				r.processCredential(ctx, cfg, store, file)
			}
		})
	}
	for _, file := range files {
		select {
		case <-ctx.Done():
			close(jobs)
			workersWG.Wait()
			return ctx.Err()
		case jobs <- file:
		}
	}
	close(jobs)
	workersWG.Wait()
	return nil
}

func (r *Runtime) snapshot() (Config, *StateStore, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.config, r.store, !r.shutdown && r.host != nil && r.store != nil
}

func (r *Runtime) processCredential(ctx context.Context, cfg Config, store *StateStore, file AuthFile) {
	credentialID := file.CredentialID()
	if credentialID == "" || !isCodex(file) {
		return
	}
	if _, loaded := r.inFlight.LoadOrStore(credentialID, struct{}{}); loaded {
		return
	}
	defer r.inFlight.Delete(credentialID)

	if reason := ineligibleReason(cfg, file); reason != "" {
		r.updateStatus(ctx, store, credentialID, "skipped", reason, true)
		return
	}

	state := store.Credential(credentialID)
	credentialVersion := file.VersionKey()
	if state.BlockedCredentialVersion != "" && state.BlockedCredentialVersion == credentialVersion {
		r.updateStatus(ctx, store, credentialID, "blocked", "credential_unchanged_after_auth_failure", true)
		return
	}
	if state.BlockedCredentialVersion != "" {
		_ = store.Update(ctx, credentialID, func(current *CredentialState) {
			current.BlockedCredentialVersion = ""
			current.LastError = ""
		})
	}

	document, material, err := r.credentialMaterial(ctx, file)
	if err != nil {
		_ = store.Update(ctx, credentialID, func(current *CredentialState) {
			current.Status = "blocked"
			current.Reason = "credential_unavailable"
			current.LastError = "credential material unavailable"
			current.BlockedCredentialVersion = credentialVersion
			current.Skipped++
		})
		return
	}
	if file.Name == "" {
		file.Name = document.Name
	}

	observedAt := r.now().UTC()
	requestCtx, cancel := context.WithTimeoutCause(ctx, cfg.RequestTimeout, errors.New("quota request timeout"))
	observation, err := r.fetchObservation(requestCtx, material, observedAt)
	cancel()
	if err != nil {
		r.handleObservationError(ctx, store, credentialID, credentialVersion, err)
		return
	}

	state = store.Credential(credentialID)
	decision := EvaluateObservation(state, observation, observedAt, cfg.ActivationDelay)
	if !state.NextRetryAt.IsZero() && observedAt.Before(state.NextRetryAt) {
		decision = WindowDecision{Kind: DecisionWaiting, Reason: "cooldown"}
	}
	if !decision.Boundary.IsZero() && sameTime(state.LastProcessedResetAt, decision.Boundary) {
		decision = WindowDecision{Kind: DecisionWaiting, Reason: "cycle_already_processed", ClearPending: true}
	}
	if err := store.Update(ctx, credentialID, func(current *CredentialState) {
		applyObservationDecision(current, observation, decision)
	}); err != nil {
		r.log(ctx, "warn", "auto-ping state persistence failed", map[string]any{"credential": credentialID})
		return
	}

	switch decision.Kind {
	case DecisionExternal:
		r.log(ctx, "debug", "auto-ping skipped for external activation", map[string]any{"credential": credentialID, "reset_at": decision.Boundary})
		return
	case DecisionWaiting:
		return
	case DecisionReady:
	default:
		return
	}

	attemptAt := r.now().UTC()
	if err := store.Update(ctx, credentialID, func(current *CredentialState) {
		current.Status = "in_flight"
		current.Reason = decision.Reason
		current.LastAttemptAt = attemptAt
		current.LastAttemptStatus = "in_flight"
		current.Attempts++
	}); err != nil {
		return
	}
	r.log(ctx, "info", "Codex auto-ping started", map[string]any{"credential": credentialID, "reset_at": decision.Boundary})

	result := r.activate(ctx, cfg, file, material)
	completedAt := r.now().UTC()
	if result.Success {
		_ = store.Update(ctx, credentialID, func(current *CredentialState) {
			current.Status = "waiting"
			current.Reason = "ping_succeeded"
			current.LastProcessedResetAt = decision.Boundary.UTC()
			current.ActivationSource = "auto_ping"
			current.AwaitingStabilization = true
			current.LastPingAt = completedAt
			current.LastAttemptStatus = "success"
			current.LastError = ""
			current.NextRetryAt = time.Time{}
			current.BlockedCredentialVersion = ""
			current.SelectedModel = result.Model
			current.Transport = result.Transport
			current.Successes++
		})
		r.log(ctx, "info", "Codex auto-ping succeeded", map[string]any{
			"credential": credentialID, "reset_at": decision.Boundary, "model": result.Model, "transport": result.Transport,
		})
		return
	}

	_ = store.Update(ctx, credentialID, func(current *CredentialState) {
		current.Status = failureStatus(result.Failure)
		current.Reason = string(result.Failure)
		current.LastAttemptStatus = "failed"
		current.LastError = safeErrorMessage(result.Message)
		current.SelectedModel = result.Model
		current.Transport = result.Transport
		current.Failures++
		if result.Failure == FailureAuth {
			current.BlockedCredentialVersion = credentialVersion
			current.NextRetryAt = time.Time{}
		} else {
			current.NextRetryAt = completedAt.Add(cfg.RetryCooldown)
		}
	})
	fields := map[string]any{"credential": credentialID, "reset_at": decision.Boundary, "reason": result.Failure}
	if result.Failure != FailureAuth {
		fields["retry_at"] = completedAt.Add(cfg.RetryCooldown)
	}
	r.log(ctx, "warn", "Codex auto-ping failed", fields)
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

func (r *Runtime) handleObservationError(ctx context.Context, store *StateStore, credentialID, version string, err error) {
	failure := operationFailure(err)
	reason := "quota_unavailable"
	if errors.Is(err, ErrNoFiveHourWindow) {
		failure = FailureNone
		reason = "five_hour_window_not_found"
	} else if errors.Is(err, ErrInvalidUsage) {
		reason = "quota_payload_invalid"
	}
	_ = store.Update(ctx, credentialID, func(current *CredentialState) {
		current.Status = "skipped"
		current.Reason = reason
		current.LastError = safeErrorMessage(err.Error())
		current.Skipped++
		if failure == FailureAuth {
			current.Status = "blocked"
			current.BlockedCredentialVersion = version
		}
	})
}

func applyObservationDecision(state *CredentialState, observation Observation, decision WindowDecision) {
	state.Provider = "codex"
	state.CurrentResetAt = observation.ResetAt
	copy := observation
	state.LastObservation = &copy
	if decision.ClearPending {
		state.PendingTransition = nil
	}
	if decision.PendingTransition != nil {
		transition := *decision.PendingTransition
		state.PendingTransition = &transition
	}
	if decision.ClearStabilizing {
		state.AwaitingStabilization = false
	}
	state.Status = string(decision.Kind)
	state.Reason = decision.Reason
	switch decision.Kind {
	case DecisionExternal:
		state.LastProcessedResetAt = decision.Boundary.UTC()
		state.ActivationSource = "external"
		state.NextRetryAt = time.Time{}
		state.LastError = ""
		state.Status = "waiting"
		state.Skipped++
	case DecisionWaiting:
		state.Skipped++
	}
}

func (r *Runtime) updateStatus(ctx context.Context, store *StateStore, credentialID, status, reason string, skipped bool) {
	_ = store.Update(ctx, credentialID, func(state *CredentialState) {
		state.Status = status
		state.Reason = reason
		if skipped {
			state.Skipped++
		}
	})
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
	case file.Unavailable:
		return "unavailable"
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
	if _, loaded := r.inFlight.LoadOrStore(credentialID, struct{}{}); loaded {
		return ManualPingResponse{}, errors.New("credential already has an in-flight ping")
	}
	defer r.inFlight.Delete(credentialID)

	_, material, err := r.credentialMaterial(ctx, file)
	if err != nil {
		return ManualPingResponse{}, err
	}
	var boundary time.Time
	if request.MarkCycleProcessed {
		observation, fetchErr := r.fetchObservation(ctx, material, r.now().UTC())
		if fetchErr != nil {
			return ManualPingResponse{}, fetchErr
		}
		state := store.Credential(credentialID)
		decision := EvaluateObservation(state, observation, r.now().UTC(), cfg.ActivationDelay)
		if decision.Kind != DecisionReady {
			return ManualPingResponse{}, fmt.Errorf("current boundary is not ready: %s", decision.Reason)
		}
		boundary = decision.Boundary
		if err := store.Update(ctx, credentialID, func(current *CredentialState) {
			applyObservationDecision(current, observation, decision)
		}); err != nil {
			return ManualPingResponse{}, err
		}
	}
	if model := strings.TrimSpace(request.Model); model != "" {
		cfg.Model = model
	}
	result := r.activate(ctx, cfg, file, material)
	completedAt := r.now().UTC()
	_ = store.Update(ctx, credentialID, func(current *CredentialState) {
		current.LastAttemptAt = completedAt
		current.SelectedModel = result.Model
		current.Transport = result.Transport
		current.Attempts++
		if result.Success {
			current.LastAttemptStatus = "success"
			current.LastPingAt = completedAt
			current.LastError = ""
			current.Successes++
			if request.MarkCycleProcessed {
				current.LastProcessedResetAt = boundary.UTC()
				current.ActivationSource = "manual"
				current.AwaitingStabilization = true
			}
		} else {
			current.LastAttemptStatus = "failed"
			current.LastError = safeErrorMessage(result.Message)
			current.Failures++
		}
	})
	if !result.Success {
		return ManualPingResponse{Success: false, CredentialID: credentialID, Model: result.Model, Transport: result.Transport, Error: result.Message}, errors.New(result.Message)
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
