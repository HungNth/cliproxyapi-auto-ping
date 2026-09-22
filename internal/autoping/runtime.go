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
	reconfigured := currentStore != nil
	if store != nil && reconfigured {
		if err := store.ResetCurrentCycles(ctx); err != nil {
			return err
		}
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
	if !sleepContext(ctx, r.startupDelay) {
		return
	}
	// Configure cancels this loop and starts another with the new configuration.
	cfg, store, ok := r.snapshot()
	if !ok || !cfg.AutoPingEnabled {
		return
	}
	now := r.now()
	loc := cfg.Location
	if loc == nil {
		loc = time.Local
	}
	nextTarget, nextKey := NextMilestone(now, cfg.Schedule, cfg.Location)
	target, key := nextTarget, nextKey
	activeAttemptedKey := key
	priorAttemptedDate := now.In(loc).Format("2006-01-02")
	if store != nil {
		if err := store.ResetStaleCycles(ctx, priorAttemptedDate); err != nil {
			r.log(ctx, "warn", "reset stale milestone cycles failed", map[string]any{"reason": safeErrorMessage(err.Error())})
			return
		}
	}
	if latestTime, latestKey, hasLatest := CurrentOrLatestMilestone(now, cfg.Schedule, cfg.Location); hasLatest {
		target, key = latestTime, latestKey
		activeAttemptedKey = latestKey
		priorAttemptedDate = latestTime.In(loc).Format("2006-01-02")
		r.log(ctx, "info", "startup catch-up triggered", map[string]any{"milestone": latestKey})
	}

	for ctx.Err() == nil {
		if retryTime, hasRetry := r.nextRetryTime(store, r.now(), nextTarget, activeAttemptedKey); hasRetry && r.now().Before(nextTarget) {
			if !r.sleepUntil(ctx, retryTime) {
				return
			}
			if !r.now().Before(nextTarget) {
				continue
			}
			retryCtx, cancelRetry := context.WithTimeoutCause(ctx, max(nextTarget.Sub(r.now()), 0), errors.New("retry superseded by next milestone"))
			err := r.DispatchRetries(retryCtx)
			cancelRetry()
			if err != nil {
				if !r.now().Before(nextTarget) {
					target, key = nextTarget, nextKey
					continue
				}
				if !sleepContext(ctx, cfg.RetryCooldown) {
					return
				}
			}
			continue
		}

		nowLocal := r.now().In(loc)
		nextMidnight := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day()+1, 0, 0, 0, 0, loc)
		wakeTarget := target
		isMidnightWake := false
		if nextMidnight.Before(target) {
			wakeTarget = nextMidnight
			isMidnightWake = true
		}
		if !r.sleepUntil(ctx, wakeTarget) {
			return
		}

		nowAfterSleep := r.now()
		currentDate := nowAfterSleep.In(loc).Format("2006-01-02")
		if currentDate != priorAttemptedDate && store != nil {
			if err := store.ResetCurrentCycles(ctx); err != nil {
				r.log(ctx, "warn", "reset milestone cycles at date rollover failed", map[string]any{"reason": safeErrorMessage(err.Error())})
				return
			}
			priorAttemptedDate = currentDate
		}
		if isMidnightWake && r.now().Before(target) {
			continue
		}
		if latestTime, latestKey, hasLatest := CurrentOrLatestMilestone(nowAfterSleep, cfg.Schedule, cfg.Location); hasLatest && latestTime.After(target) {
			target, key = latestTime, latestKey
		}
		activeAttemptedKey = key
		priorAttemptedDate = target.In(loc).Format("2006-01-02")
		successorTarget, successorKey := NextMilestone(target, cfg.Schedule, cfg.Location)
		cycleDeadline := successorTarget
		targetLocal := target.In(loc)
		cycleMidnight := time.Date(targetLocal.Year(), targetLocal.Month(), targetLocal.Day()+1, 0, 0, 0, 0, loc)
		if cycleMidnight.Before(cycleDeadline) {
			cycleDeadline = cycleMidnight
		}
		milestoneCtx, cancelMilestone := context.WithTimeoutCause(ctx, max(cycleDeadline.Sub(r.now()), 0), errors.New("milestone cycle ended"))
		err := r.DispatchMilestone(milestoneCtx, key, target)
		cancelMilestone()
		if err != nil {
			nextTarget, nextKey = successorTarget, successorKey
			if !r.now().Before(cycleDeadline) {
				target, key = successorTarget, successorKey
				continue
			}
			retryDue := r.now().Add(cfg.RetryCooldown)
			if !retryDue.Before(cycleDeadline) {
				target, key = successorTarget, successorKey
				continue
			}
			if !r.sleepUntil(ctx, retryDue) {
				return
			}
			if !r.now().Before(cycleDeadline) {
				target, key = successorTarget, successorKey
			}
			continue
		}
		target, key = successorTarget, successorKey
		nextTarget, nextKey = target, key
	}
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
		return errors.New("milestone dispatch already in progress")
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
			r.log(ctx, "info", "milestone credential skipped", map[string]any{"credential": credentialID, "milestone": milestoneKey, "reason": reason})
			continue
		}
		state := store.Credential(credentialID)
		if state.LastProcessedMilestone == milestoneKey {
			r.log(ctx, "info", "milestone credential already processed", map[string]any{"credential": credentialID, "milestone": milestoneKey})
			continue
		}
		if state.AttemptedMilestone == milestoneKey {
			// Same-cycle terminal failures and active cooldowns do not repeat. A stale in-flight
			// marker means the prior worker exited before persisting an outcome, so retry it.
			if state.Status != "in_flight" && (state.NextRetryAt.IsZero() || state.NextRetryAt.After(r.now())) {
				continue
			}
		}
		eligible = append(eligible, file)
	}

	if len(eligible) == 0 {
		if err := store.SetLastMilestone(ctx, milestoneKey); err != nil {
			return err
		}
		r.log(ctx, "warn", "auto-ping milestone evaluated with zero eligible credentials", map[string]any{
			"milestone": milestoneKey, "eligible": 0, "total": len(files),
		})
		return nil
	}

	r.log(ctx, "info", "auto-ping milestone dispatching credentials", map[string]any{
		"milestone": milestoneKey, "eligible": len(eligible), "total": len(files),
	})

	workers := min(cfg.MaxConcurrency, len(eligible))
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan AuthFile, len(eligible))
	errs := make(chan error, len(eligible))
	var workersWG sync.WaitGroup
	for range workers {
		workersWG.Go(func() {
			for file := range jobs {
				if err := r.dispatchCredentialMilestone(ctx, cfg, store, file, milestoneKey, milestoneTime, false); err != nil {
					errs <- err
				}
			}
		})
	}
	for _, file := range eligible {
		select {
		case <-ctx.Done():
			close(jobs)
			workersWG.Wait()
			close(errs)
			resultErr := ctx.Err()
			for err := range errs {
				resultErr = errors.Join(resultErr, err)
			}
			return resultErr
		case jobs <- file:
		}
	}
	close(jobs)
	workersWG.Wait()
	close(errs)
	var resultErr error
	for err := range errs {
		resultErr = errors.Join(resultErr, err)
	}
	if err := store.SetLastMilestone(ctx, milestoneKey); err != nil {
		resultErr = errors.Join(resultErr, err)
	}
	return resultErr
}

func (r *Runtime) nextRetryTime(store *StateStore, now, milestoneTarget time.Time, milestoneKey string) (time.Time, bool) {
	if store == nil || milestoneKey == "" {
		return time.Time{}, false
	}
	var earliest time.Time
	found := false
	for _, account := range store.Accounts() {
		if account.NextRetryAt.IsZero() || account.RetryCount < 1 {
			continue
		}
		if account.AttemptedMilestone != milestoneKey {
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
		if state.LastProcessedMilestone == key {
			continue
		}
		if state.AttemptedMilestone != key {
			continue
		}
		if state.NextRetryAt.IsZero() || state.NextRetryAt.After(now) {
			continue
		}
		if state.RetryCount < 1 {
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
	errs := make(chan error, len(due))
	var workersWG sync.WaitGroup
	for range workers {
		workersWG.Go(func() {
			for file := range jobs {
				if err := r.dispatchCredentialMilestone(ctx, cfg, store, file, key, target, true); err != nil {
					errs <- err
				}
			}
		})
	}
	for _, file := range due {
		select {
		case <-ctx.Done():
			close(jobs)
			workersWG.Wait()
			close(errs)
			resultErr := ctx.Err()
			for err := range errs {
				resultErr = errors.Join(resultErr, err)
			}
			return resultErr
		case jobs <- file:
		}
	}
	close(jobs)
	workersWG.Wait()
	close(errs)
	var resultErr error
	for err := range errs {
		resultErr = errors.Join(resultErr, err)
	}
	return resultErr
}

func (r *Runtime) dispatchCredentialMilestone(ctx context.Context, cfg Config, store *StateStore, file AuthFile, milestoneKey string, milestoneTime time.Time, isRetry bool) error {
	credentialID := file.CredentialID()
	done := make(chan struct{})
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		active, loaded := r.inFlight.LoadOrStore(credentialID, done)
		if !loaded {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-active.(chan struct{}):
		}
	}
	defer func() {
		r.inFlight.Delete(credentialID)
		close(done)
	}()
	if store.Credential(credentialID).LastProcessedMilestone == milestoneKey {
		return nil
	}

	attemptAt := r.now().UTC()
	if err := store.Update(ctx, credentialID, func(current *CredentialState) {
		current.Status = "in_flight"
		current.Reason = "milestone_triggered"
		current.AttemptedMilestone = milestoneKey
		current.LastAttemptAt = attemptAt
		current.LastAttemptStatus = "in_flight"
		current.Attempts++
		current.NextRetryAt = time.Time{}
		if !isRetry {
			current.RetryCount = 0
		}
	}); err != nil {
		return fmt.Errorf("persist in-flight milestone state: %w", err)
	}
	r.log(ctx, "info", "Codex scheduled auto-ping started", map[string]any{"credential": credentialID, "milestone": milestoneKey, "is_retry": isRetry})

	document, material, err := r.credentialMaterial(ctx, file)
	result := ActivationResult{Failure: FailureRetryable, Message: "credential material unavailable"}
	if err == nil {
		if file.Name == "" {
			file.Name = document.Name
		}
		result = r.activate(ctx, cfg, file, material)
	}
	completedAt := r.now().UTC()
	if result.Success {
		if err := store.Update(ctx, credentialID, func(current *CredentialState) {
			current.Status = "waiting"
			current.Reason = "milestone_ping_succeeded"
			current.AttemptedMilestone = milestoneKey
			current.FailureKind = ""
			current.LastProcessedMilestone = milestoneKey
			current.LastProcessedMilestoneAt = milestoneTime.UTC()
			current.LastPingAt = completedAt
			current.LastAttemptStatus = "success"
			current.LastError = ""
			current.NextRetryAt = time.Time{}
			current.RetryCount = 0
			current.SelectedModel = result.Model
			current.Transport = result.Transport
			current.Successes++
		}); err != nil {
			r.log(ctx, "warn", "persist milestone success state failed", map[string]any{"credential": credentialID, "milestone": milestoneKey, "error": safeErrorMessage(err.Error())})
			return fmt.Errorf("persist milestone success state: %w", err)
		}
		r.log(ctx, "info", "Codex scheduled auto-ping succeeded", map[string]any{
			"credential": credentialID, "milestone": milestoneKey, "model": result.Model, "transport": result.Transport,
		})
		return nil
	}

	if err := store.Update(ctx, credentialID, func(current *CredentialState) {
		current.Status = failureStatus(result.Failure)
		if result.Failure == FailureAuth {
			current.Reason = "credential_authentication_failed"
		} else {
			current.Reason = string(result.Failure)
		}
		current.AttemptedMilestone = milestoneKey
		current.FailureKind = string(result.Failure)
		current.LastAttemptStatus = "failed"
		current.LastError = safeErrorMessage(result.Message)
		current.SelectedModel = result.Model
		current.Transport = result.Transport
		current.Failures++

		isRecoverable := result.Failure == FailureTransport || result.Failure == FailureTimeout || result.Failure == FailureRetryable
		if !isRecoverable {
			current.NextRetryAt = time.Time{}
		} else {
			current.RetryCount++
			cooldown := cfg.RetryCooldown
			if result.RetryAfter != nil && *result.RetryAfter > cooldown {
				cooldown = *result.RetryAfter
			}
			retryTarget := completedAt.Add(cooldown)
			nextMilestone, _ := NextMilestone(milestoneTime, cfg.Schedule, cfg.Location)
			loc := cfg.Location
			if loc == nil {
				loc = time.Local
			}
			milestoneLocal := milestoneTime.In(loc)
			midnight := time.Date(milestoneLocal.Year(), milestoneLocal.Month(), milestoneLocal.Day()+1, 0, 0, 0, 0, loc)
			cycleEnd := nextMilestone
			if midnight.Before(cycleEnd) {
				cycleEnd = midnight
			}
			if retryTarget.Before(cycleEnd) {
				current.NextRetryAt = retryTarget
			} else {
				current.NextRetryAt = time.Time{}
			}
		}
	}); err != nil {
		return fmt.Errorf("persist failed milestone state: %w", err)
	}
	fields := map[string]any{"credential": credentialID, "milestone": milestoneKey, "reason": result.Failure}
	if state := store.Credential(credentialID); !state.NextRetryAt.IsZero() {
		fields["retry_at"] = state.NextRetryAt
	}
	r.log(ctx, "warn", "Codex scheduled auto-ping failed", fields)
	return nil
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
