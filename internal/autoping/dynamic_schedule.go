package autoping

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

func (r *Runtime) runDynamicSchedule(ctx context.Context) {
	if !sleepContext(ctx, r.startupDelay) {
		return
	}
	cfg, store, ok := r.snapshot()
	if !ok || !cfg.AutoPingEnabled {
		return
	}
	// The main loop gates only unanchored credentials until the first daily milestone.
	// Persisted targets, retries, and stabilization work remain authoritative after restart.

	// Dynamic loop
	for ctx.Err() == nil {
		cfg, store, ok = r.snapshot()
		if !ok || !cfg.AutoPingEnabled {
			return
		}

		files, err := r.host.ListAuth(ctx)
		if err != nil {
			r.log(ctx, "warn", "dynamic schedule list auth failed", map[string]any{"error": err.Error()})
			if !sleepContext(ctx, cfg.RetryCooldown) {
				return
			}
			continue
		}

		eligible := make([]AuthFile, 0, len(files))
		for _, file := range files {
			if file.CredentialID() != "" && isCodex(file) && ineligibleReason(cfg, file) == "" {
				eligible = append(eligible, file)
			}
		}

		if len(eligible) == 0 {
			if !sleepContext(ctx, cfg.RetryCooldown) {
				return
			}
			continue
		}
		now := r.now()
		initialAnchor := firstDailyAnchor(now, cfg.Schedule, cfg.Location)
		beforeInitialAnchor := now.Before(initialAnchor)
		dueJobs := make([]AuthFile, 0, len(eligible))

		for _, file := range eligible {
			credID := file.CredentialID()
			state := store.Credential(credID)

			// Terminal failures end only the current configured milestone cycle.
			if terminalDynamicFailure(FailureKind(state.FailureKind)) {
				nextAnchor, _ := NextMilestone(state.LastAttemptAt, cfg.Schedule, cfg.Location)
				if now.Before(nextAnchor) {
					continue
				}
				if err := store.Update(ctx, credID, func(current *CredentialState) {
					current.FailureKind = ""
					current.Status = "waiting"
					current.Reason = "cycle_reset"
				}); err != nil {
					r.log(ctx, "error", "dynamic schedule stopped", map[string]any{"error": safeErrorMessage(err.Error())})
					return
				}
				state = store.Credential(credID)
			}

			// Active retry cooldown
			if !state.NextRetryAt.IsZero() && state.NextRetryAt.After(now) {
				continue
			}

			// In stabilizing state: ping succeeded, waiting to re-observe usage
			if state.Status == "stabilizing" {
				dueJobs = append(dueJobs, file)
				continue
			}

			// Only never-anchored credentials wait for the first daily anchor.
			// Persisted retries remain authoritative across restart.
			if state.TargetTriggerAt.IsZero() {
				if !beforeInitialAnchor || !state.NextRetryAt.IsZero() {
					dueJobs = append(dueJobs, file)
				}
				continue
			}

			// Anchored credential, check if TargetTriggerAt has arrived
			if !now.Before(state.TargetTriggerAt) {
				dueJobs = append(dueJobs, file)
			}
		}

		// Dispatch due jobs bounded by MaxConcurrency. Stop the loop if durable state cannot be updated.
		if len(dueJobs) > 0 {
			if err := r.dispatchDueDynamicJobs(ctx, cfg, store, dueJobs); err != nil {
				r.log(ctx, "error", "dynamic schedule stopped", map[string]any{"error": safeErrorMessage(err.Error())})
				return
			}
		}

		// Recalculate next earliest wake time across all eligible credentials
		nowAfter := r.now()
		var nextEarliestWake time.Time

		for _, file := range eligible {
			state := store.Credential(file.CredentialID())
			if terminalDynamicFailure(FailureKind(state.FailureKind)) {
				nextAnchor, _ := NextMilestone(state.LastAttemptAt, cfg.Schedule, cfg.Location)
				if nextEarliestWake.IsZero() || nextAnchor.Before(nextEarliestWake) {
					nextEarliestWake = nextAnchor
				}
				continue
			}

			if state.TargetTriggerAt.IsZero() && state.Status != "stabilizing" && state.NextRetryAt.IsZero() && beforeInitialAnchor {
				if nextEarliestWake.IsZero() || initialAnchor.Before(nextEarliestWake) {
					nextEarliestWake = initialAnchor
				}
			}
			if !state.TargetTriggerAt.IsZero() && state.TargetTriggerAt.After(nowAfter) {
				if nextEarliestWake.IsZero() || state.TargetTriggerAt.Before(nextEarliestWake) {
					nextEarliestWake = state.TargetTriggerAt
				}
			}
			if !state.NextRetryAt.IsZero() && state.NextRetryAt.After(nowAfter) {
				if nextEarliestWake.IsZero() || state.NextRetryAt.Before(nextEarliestWake) {
					nextEarliestWake = state.NextRetryAt
				}
			}
		}

		if nextEarliestWake.IsZero() {
			nextEarliestWake = r.now().Add(cfg.RetryCooldown)
		}

		if !r.sleepUntil(ctx, nextEarliestWake) {
			return
		}
	}
}
func firstDailyAnchor(now time.Time, schedule []string, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.Local
	}
	localNow := now.In(loc)
	var hour, minute int
	_, _ = fmt.Sscanf(schedule[0], "%d:%d", &hour, &minute)
	return time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, minute, 0, 0, loc)
}

func (r *Runtime) dispatchDueDynamicJobs(ctx context.Context, cfg Config, store *StateStore, dueJobs []AuthFile) error {
	workers := min(cfg.MaxConcurrency, len(dueJobs))
	if workers < 1 {
		workers = 1
	}

	jobs := make(chan AuthFile, len(dueJobs))
	errs := make(chan error, len(dueJobs))
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for file := range jobs {
				if err := r.runDynamicJob(ctx, cfg, store, file); err != nil {
					errs <- err
				}
			}
		})
	}

	for _, file := range dueJobs {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			close(errs)
			return errors.Join(ctx.Err(), collectErrors(errs))
		case jobs <- file:
		}
	}
	close(jobs)
	wg.Wait()
	close(errs)
	return collectErrors(errs)
}

func collectErrors(errs <-chan error) error {
	var result error
	for err := range errs {
		result = errors.Join(result, err)
	}
	return result
}

func (r *Runtime) runDynamicJob(ctx context.Context, cfg Config, store *StateStore, file AuthFile) error {
	credID := file.CredentialID()
	done := make(chan struct{})
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		active, loaded := r.inFlight.LoadOrStore(credID, done)
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
		r.inFlight.Delete(credID)
		close(done)
	}()

	state := store.Credential(credID)
	now := r.now()
	if terminalDynamicFailure(FailureKind(state.FailureKind)) {
		return nil
	}
	if !state.NextRetryAt.IsZero() && state.NextRetryAt.After(now) {
		return nil
	}
	if !state.TargetTriggerAt.IsZero() && now.Before(state.TargetTriggerAt) {
		return nil
	}
	if state.Status == "stabilizing" {
		_, material, err := r.credentialMaterial(ctx, file)
		if err != nil {
			return r.recordStabilizationFailure(ctx, cfg, store, credID, FailureRetryable, err.Error())
		}
		return r.stabilizePostPing(ctx, cfg, store, file, material, state.ObservedResetAt)
	}
	if state.TargetTriggerAt.IsZero() {
		return r.discoverCredentialAnchor(ctx, cfg, store, file)
	}
	return r.executeDynamicPing(ctx, cfg, store, file)
}

func (r *Runtime) discoverCredentialAnchor(ctx context.Context, cfg Config, store *StateStore, file AuthFile) error {
	credID := file.CredentialID()
	_, material, err := r.credentialMaterial(ctx, file)
	if err != nil {
		return r.recordDynamicFailure(ctx, store, credID, FailureRetryable, fmt.Sprintf("credential material: %v", err), cfg.RetryCooldown)
	}

	obs, failure, message := r.fetchUsageObservation(ctx, cfg, material)
	if failure != FailureNone {
		return r.recordDynamicFailure(ctx, store, credID, failure, message, cfg.RetryCooldown)
	}

	target := obs.ResetAt.Add(30 * time.Second)
	if err := store.Update(ctx, credID, func(current *CredentialState) {
		current.Status = "waiting"
		current.Reason = "trigger_scheduled"
		current.ObservedResetAt = obs.ResetAt
		current.TargetTriggerAt = target
		current.FailureKind = ""
		current.LastError = ""
		current.NextRetryAt = time.Time{}
		current.RetryCount = 0
	}); err != nil {
		return fmt.Errorf("persist dynamic anchor: %w", err)
	}
	r.log(ctx, "info", "dynamic anchor discovered", map[string]any{
		"credential": credID,
		"reset_at":   obs.ResetAt.Format(time.RFC3339),
		"target":     target.Format(time.RFC3339),
	})
	return nil
}

func (r *Runtime) executeDynamicPing(ctx context.Context, cfg Config, store *StateStore, file AuthFile) error {
	credID := file.CredentialID()
	attemptAt := r.now().UTC()
	if err := store.Update(ctx, credID, func(current *CredentialState) {
		current.Status = "in_flight"
		current.Reason = "dynamic_trigger"
		current.LastAttemptAt = attemptAt
		current.LastAttemptStatus = "in_flight"
		current.Attempts++
	}); err != nil {
		return fmt.Errorf("persist dynamic in-flight state: %w", err)
	}

	_, material, err := r.credentialMaterial(ctx, file)
	if err != nil {
		return r.recordDynamicFailure(ctx, store, credID, FailureRetryable, err.Error(), cfg.RetryCooldown)
	}

	result := r.activate(ctx, cfg, file, material)
	completedAt := r.now().UTC()
	if !result.Success {
		return r.recordDynamicFailure(ctx, store, credID, result.Failure, result.Message, cfg.RetryCooldown)
	}

	priorReset := store.Credential(credID).ObservedResetAt
	if err := store.Update(ctx, credID, func(current *CredentialState) {
		current.Status = "stabilizing"
		current.Reason = "awaiting_new_reset"
		current.TargetTriggerAt = time.Time{}
		current.LastAttemptStatus = "success"
		current.LastPingAt = completedAt
		current.SelectedModel = result.Model
		current.Transport = result.Transport
		current.Successes++
		current.FailureKind = ""
		current.LastError = ""
	}); err != nil {
		return fmt.Errorf("persist dynamic ping success: %w", err)
	}

	return r.stabilizePostPing(ctx, cfg, store, file, material, priorReset)
}

func (r *Runtime) stabilizePostPing(ctx context.Context, cfg Config, store *StateStore, file AuthFile, material AuthMaterial, priorReset time.Time) error {
	credID := file.CredentialID()
	lastFailure := FailureRetryable
	lastMessage := "post-ping reset_at did not advance"

	for range 3 {
		if !sleepContext(ctx, 5*time.Second) {
			return ctx.Err()
		}
		obs, failure, message := r.fetchUsageObservation(ctx, cfg, material)
		if failure == FailureNone && obs.ResetAt.After(priorReset) {
			newTarget := obs.ResetAt.Add(30 * time.Second)
			if err := store.Update(ctx, credID, func(current *CredentialState) {
				current.Status = "waiting"
				current.Reason = "trigger_scheduled"
				current.LastProcessedResetAt = priorReset
				current.ObservedResetAt = obs.ResetAt
				current.TargetTriggerAt = newTarget
				current.FailureKind = ""
				current.LastError = ""
				current.NextRetryAt = time.Time{}
				current.RetryCount = 0
			}); err != nil {
				return fmt.Errorf("persist advanced dynamic cycle: %w", err)
			}
			r.log(ctx, "info", "dynamic cycle advanced", map[string]any{
				"credential": credID,
				"reset_at":   obs.ResetAt.Format(time.RFC3339),
				"target":     newTarget.Format(time.RFC3339),
			})
			return nil
		}
		if failure != FailureNone {
			lastFailure = failure
			lastMessage = message
			if terminalDynamicFailure(failure) {
				break
			}
		}
	}

	return r.recordStabilizationFailure(ctx, cfg, store, credID, lastFailure, lastMessage)
}

func (r *Runtime) recordStabilizationFailure(ctx context.Context, cfg Config, store *StateStore, credID string, failure FailureKind, message string) error {
	if err := store.Update(ctx, credID, func(current *CredentialState) {
		current.Status = "stabilizing"
		current.Reason = "usage_stabilization_retry"
		current.TargetTriggerAt = time.Time{}
		current.FailureKind = string(failure)
		current.LastError = safeErrorMessage(message)
		current.LastAttemptAt = r.now().UTC()
		if terminalDynamicFailure(failure) {
			current.NextRetryAt = time.Time{}
		} else {
			current.NextRetryAt = r.now().Add(cfg.RetryCooldown)
		}
	}); err != nil {
		return fmt.Errorf("persist usage stabilization failure: %w", err)
	}
	r.log(ctx, "warn", "usage stabilization deferred", map[string]any{
		"credential": credID,
		"failure":    failure,
		"message":    safeErrorMessage(message),
	})
	return nil
}

func terminalDynamicFailure(failure FailureKind) bool {
	return failure == FailureAuth || failure == FailureModel || failure == FailureBusiness
}

func (r *Runtime) recordDynamicFailure(ctx context.Context, store *StateStore, credID string, failure FailureKind, message string, cooldown time.Duration) error {
	if err := store.Update(ctx, credID, func(current *CredentialState) {
		current.Status = failureStatus(failure)
		current.Reason = "failure"
		current.FailureKind = string(failure)
		current.LastAttemptStatus = "failed"
		current.LastError = safeErrorMessage(message)
		current.LastAttemptAt = r.now().UTC()
		current.Failures++
		if terminalDynamicFailure(failure) {
			current.TargetTriggerAt = time.Time{}
			current.NextRetryAt = time.Time{}
		} else {
			current.RetryCount++
			current.NextRetryAt = r.now().Add(cooldown)
		}
	}); err != nil {
		return fmt.Errorf("persist dynamic failure: %w", err)
	}
	r.log(ctx, "warn", "dynamic operation failure", map[string]any{
		"credential": credID,
		"failure":    failure,
		"message":    safeErrorMessage(message),
	})
	return nil
}
