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

func (r *Runtime) scheduleLoop(ctx context.Context) {
	if !sleepContext(ctx, r.startupDelay) {
		return
	}
	r.startupCatchUp(ctx)

	for {
		cfg, store, ok := r.snapshot()
		if !ok || !cfg.AutoPingEnabled {
			return
		}
		target, key := NextMilestone(r.now(), cfg.Schedule, cfg.Location)
		if retryTime, hasRetry := r.nextRetryTime(store, r.now(), target); hasRetry {
			for r.now().Before(retryTime) {
				if !sleepContext(ctx, retryTime.Sub(r.now())) {
					return
				}
			}
			_ = r.dispatchRetries(ctx)
			continue
		}
		for r.now().Before(target) {
			if !sleepContext(ctx, target.Sub(r.now())) {
				return
			}
		}
		_ = r.DispatchMilestone(ctx, key, target)
	}
}

func (r *Runtime) startupCatchUp(ctx context.Context) {
	cfg, _, ok := r.snapshot()
	if !ok || !cfg.AutoPingEnabled {
		return
	}
	latestTime, latestKey, hasLatest := CurrentOrLatestMilestone(r.now(), cfg.Schedule, cfg.Location)
	if !hasLatest {
		return
	}
	nextTime, _ := NextMilestone(r.now(), cfg.Schedule, cfg.Location)
	if nextTime.Sub(r.now()) < 1*time.Hour {
		r.log(ctx, "info", "startup catch-up skipped: next milestone is less than 1 hour away", map[string]any{
			"latest_milestone": latestKey,
			"next_milestone":   nextTime.Format(time.RFC3339),
		})
		return
	}
	r.log(ctx, "info", "startup catch-up triggered", map[string]any{
		"milestone": latestKey,
	})
	_ = r.DispatchMilestone(ctx, latestKey, latestTime)
}

func (r *Runtime) ScanOnce(ctx context.Context) error {
	cfg, _, ok := r.snapshot()
	if !ok || !cfg.AutoPingEnabled {
		return nil
	}
	target, key, ok := CurrentOrLatestMilestone(r.now(), cfg.Schedule, cfg.Location)
	if !ok {
		target, key = NextMilestone(r.now().Add(-time.Second), cfg.Schedule, cfg.Location)
	}
	return r.DispatchMilestone(ctx, key, target)
}

func (r *Runtime) DispatchMilestone(ctx context.Context, milestoneKey string, milestoneTime time.Time) error {
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
		r.log(ctx, "warn", "auto-ping credential listing failed", map[string]any{"reason": "credential_list_failed"})
		return err
	}

	eligible := make([]AuthFile, 0, len(files))
	for _, file := range files {
		credentialID := file.CredentialID()
		if credentialID == "" || !isCodex(file) {
			continue
		}
		if reason := ineligibleReason(cfg, file); reason != "" {
			r.updateStatus(ctx, store, credentialID, "skipped", reason, true)
			continue
		}
		state := store.Credential(credentialID)
		credentialVersion := file.VersionKey()
		if state.BlockedCredentialVersion != "" && state.BlockedCredentialVersion == credentialVersion {
			r.updateStatus(ctx, store, credentialID, "blocked", "credential_unchanged_after_auth_failure", true)
			continue
		}
		if state.BlockedCredentialVersion != "" {
			_ = store.Update(ctx, credentialID, func(current *CredentialState) {
				current.BlockedCredentialVersion = ""
				current.LastError = ""
			})
		}
		if state.LastProcessedMilestone == milestoneKey {
			continue
		}
		eligible = append(eligible, file)
	}

	if len(eligible) == 0 {
		_ = store.SetLastMilestone(ctx, milestoneKey)
		return nil
	}

	workers := min(cfg.MaxConcurrency, len(eligible))
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan AuthFile, len(eligible))
	var workersWG sync.WaitGroup
	for range workers {
		workersWG.Go(func() {
			for file := range jobs {
				r.dispatchCredentialMilestone(ctx, cfg, store, file, milestoneKey, milestoneTime, false)
			}
		})
	}
	for _, file := range eligible {
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

	_ = store.SetLastMilestone(ctx, milestoneKey)
	return nil
}

func (r *Runtime) nextRetryTime(store *StateStore, now, milestoneTarget time.Time) (time.Time, bool) {
	if store == nil {
		return time.Time{}, false
	}
	var earliest time.Time
	found := false
	for _, account := range store.Accounts() {
		if account.NextRetryAt.IsZero() || account.RetryCount < 1 || account.RetryCount >= 3 {
			continue
		}
		if account.BlockedCredentialVersion != "" {
			continue
		}
		if !account.NextRetryAt.Before(milestoneTarget) {
			continue
		}
		if !found || account.NextRetryAt.Before(earliest) {
			earliest = account.NextRetryAt
			found = true
		}
	}
	if !found {
		return time.Time{}, false
	}
	if earliest.Before(now) {
		return now, true
	}
	return earliest, true
}

func (r *Runtime) dispatchRetries(ctx context.Context) error {
	return r.DispatchRetries(ctx)
}

func (r *Runtime) DispatchRetries(ctx context.Context) error {
	cfg, store, ok := r.snapshot()
	if !ok || !cfg.AutoPingEnabled {
		return nil
	}
	if !r.scanRunning.CompareAndSwap(false, true) {
		return nil
	}
	defer r.scanRunning.Store(false)

	target, key, hasLatest := CurrentOrLatestMilestone(r.now(), cfg.Schedule, cfg.Location)
	if !hasLatest {
		key = store.LastProcessedMilestone()
		target = r.now()
	}

	files, err := r.host.ListAuth(ctx)
	if err != nil {
		r.log(ctx, "warn", "auto-ping credential listing failed during retry", map[string]any{"reason": "credential_list_failed"})
		return err
	}

	now := r.now()
	due := make([]AuthFile, 0, len(files))
	for _, file := range files {
		credentialID := file.CredentialID()
		if credentialID == "" || !isCodex(file) {
			continue
		}
		if reason := ineligibleReason(cfg, file); reason != "" {
			r.updateStatus(ctx, store, credentialID, "skipped", reason, true)
			continue
		}
		state := store.Credential(credentialID)
		credentialVersion := file.VersionKey()
		if state.BlockedCredentialVersion != "" && state.BlockedCredentialVersion == credentialVersion {
			r.updateStatus(ctx, store, credentialID, "blocked", "credential_unchanged_after_auth_failure", true)
			continue
		}
		if state.BlockedCredentialVersion != "" {
			_ = store.Update(ctx, credentialID, func(current *CredentialState) {
				current.BlockedCredentialVersion = ""
				current.LastError = ""
			})
		}
		if state.LastProcessedMilestone == key {
			continue
		}
		if state.NextRetryAt.IsZero() || state.NextRetryAt.After(now) {
			continue
		}
		if state.RetryCount < 1 || state.RetryCount >= 3 {
			continue
		}
		due = append(due, file)
	}

	if len(due) == 0 {
		return nil
	}

	workers := min(cfg.MaxConcurrency, len(due))
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan AuthFile, len(due))
	var workersWG sync.WaitGroup
	for range workers {
		workersWG.Go(func() {
			for file := range jobs {
				r.dispatchCredentialMilestone(ctx, cfg, store, file, key, target, true)
			}
		})
	}
	for _, file := range due {
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

func (r *Runtime) dispatchCredentialMilestone(ctx context.Context, cfg Config, store *StateStore, file AuthFile, milestoneKey string, milestoneTime time.Time, isRetry bool) {
	credentialID := file.CredentialID()
	if _, loaded := r.inFlight.LoadOrStore(credentialID, struct{}{}); loaded {
		return
	}
	defer r.inFlight.Delete(credentialID)

	credentialVersion := file.VersionKey()
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

	attemptAt := r.now().UTC()
	_ = store.Update(ctx, credentialID, func(current *CredentialState) {
		current.Status = "in_flight"
		current.Reason = "milestone_triggered"
		current.LastAttemptAt = attemptAt
		current.LastAttemptStatus = "in_flight"
		current.Attempts++
		current.NextRetryAt = time.Time{}
		if !isRetry {
			current.RetryCount = 0
		}
	})
	r.log(ctx, "info", "Codex scheduled auto-ping started", map[string]any{"credential": credentialID, "milestone": milestoneKey, "is_retry": isRetry})

	result := r.activate(ctx, cfg, file, material)
	completedAt := r.now().UTC()
	if result.Success {
		_ = store.Update(ctx, credentialID, func(current *CredentialState) {
			current.Status = "waiting"
			current.Reason = "milestone_ping_succeeded"
			current.LastProcessedMilestone = milestoneKey
			current.LastProcessedMilestoneAt = milestoneTime.UTC()
			current.LastPingAt = completedAt
			current.LastAttemptStatus = "success"
			current.LastError = ""
			current.NextRetryAt = time.Time{}
			current.RetryCount = 0
			current.BlockedCredentialVersion = ""
			current.SelectedModel = result.Model
			current.Transport = result.Transport
			current.Successes++
		})
		r.log(ctx, "info", "Codex scheduled auto-ping succeeded", map[string]any{
			"credential": credentialID, "milestone": milestoneKey, "model": result.Model, "transport": result.Transport,
		})
		return
	}

	_ = store.Update(ctx, credentialID, func(current *CredentialState) {
		current.Status = failureStatus(result.Failure)
		if result.Failure == FailureAuth {
			current.Reason = "credential_unchanged_after_auth_failure"
		} else {
			current.Reason = string(result.Failure)
		}
		current.LastAttemptStatus = "failed"
		current.LastError = safeErrorMessage(result.Message)
		current.SelectedModel = result.Model
		current.Transport = result.Transport
		current.Failures++
		if result.Failure == FailureAuth {
			current.BlockedCredentialVersion = credentialVersion
			current.NextRetryAt = time.Time{}
			current.RetryCount = 0
		} else {
			current.RetryCount++
			if current.RetryCount < 3 {
				current.NextRetryAt = completedAt.Add(cfg.RetryCooldown)
			} else {
				current.NextRetryAt = time.Time{}
			}
		}
	})
	fields := map[string]any{"credential": credentialID, "milestone": milestoneKey, "reason": result.Failure}
	if result.Failure != FailureAuth {
		fields["retry_at"] = completedAt.Add(cfg.RetryCooldown)
	}
	r.log(ctx, "warn", "Codex scheduled auto-ping failed", fields)
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
				if target, key, ok := CurrentOrLatestMilestone(completedAt, cfg.Schedule, cfg.Location); ok {
					current.LastProcessedMilestone = key
					current.LastProcessedMilestoneAt = target.UTC()
				}
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
