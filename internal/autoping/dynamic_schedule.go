package autoping

import (
	"context"
	"encoding/json"
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
			if file.CredentialID() != "" && (isCodex(file) || isAntigravity(file)) && ineligibleReason(cfg, file) == "" {
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
			if isAntigravity(file) {
				due, dueErr := r.isAntigravityDue(ctx, now, initialAnchor, beforeInitialAnchor, cfg, store, file)
				if dueErr != nil {
					r.log(ctx, "error", "dynamic schedule stopped", map[string]any{"error": safeErrorMessage(dueErr.Error())})
					return
				}
				if due {
					dueJobs = append(dueJobs, file)
				}
				continue
			}

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

			// Mandatory daily anchor ping: if initial daily anchor (e.g. 05:00) has arrived
			// and credential has not pinged today yet, it is immediately due.
			if !beforeInitialAnchor && (state.LastPingAt.IsZero() || state.LastPingAt.Before(initialAnchor)) {
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
			if isAntigravity(file) {
				r.calculateAntigravityWake(nowAfter, initialAnchor, beforeInitialAnchor, cfg, store, file, &nextEarliestWake)
				continue
			}
			state := store.Credential(file.CredentialID())
			if terminalDynamicFailure(FailureKind(state.FailureKind)) {
				nextAnchor, _ := NextMilestone(state.LastAttemptAt, cfg.Schedule, cfg.Location)
				target := nextAnchor
				if target.Before(nowAfter) {
					target = nowAfter
				}
				if nextEarliestWake.IsZero() || target.Before(nextEarliestWake) {
					nextEarliestWake = target
				}
				continue
			}

			// If in active retry cooldown, wake when cooldown expires
			if !state.NextRetryAt.IsZero() {
				target := state.NextRetryAt
				if target.Before(nowAfter) {
					target = nowAfter
				}
				if nextEarliestWake.IsZero() || target.Before(nextEarliestWake) {
					nextEarliestWake = target
				}
				continue
			}

			// Stabilizing without cooldown is due immediately
			if state.Status == "stabilizing" {
				nextEarliestWake = nowAfter
				continue
			}

			if beforeInitialAnchor && (state.LastPingAt.IsZero() || state.LastPingAt.Before(initialAnchor)) {
				if nextEarliestWake.IsZero() || initialAnchor.Before(nextEarliestWake) {
					nextEarliestWake = initialAnchor
				}
			}
			if state.TargetTriggerAt.IsZero() && beforeInitialAnchor {
				if nextEarliestWake.IsZero() || initialAnchor.Before(nextEarliestWake) {
					nextEarliestWake = initialAnchor
				}
			}
			if !state.TargetTriggerAt.IsZero() {
				target := state.TargetTriggerAt
				if target.Before(nowAfter) {
					target = nowAfter
				}
				if nextEarliestWake.IsZero() || target.Before(nextEarliestWake) {
					nextEarliestWake = target
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
	if isAntigravity(file) {
		return r.runDynamicAntigravityJob(ctx, cfg, store, file)
	}
	initialAnchor := firstDailyAnchor(now, cfg.Schedule, cfg.Location)
	isMandatoryDailyAnchor := !now.Before(initialAnchor) && (state.LastPingAt.IsZero() || state.LastPingAt.Before(initialAnchor))
	if !isMandatoryDailyAnchor && !state.TargetTriggerAt.IsZero() && now.Before(state.TargetTriggerAt) {
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
			newTarget := nextTargetAfterPing(r.now(), obs.ResetAt, cfg.Schedule, cfg.Location)
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

func nextTargetAfterPing(completedAt, resetAt time.Time, schedule []string, loc *time.Location) time.Time {
	dynamicTrigger := resetAt.Add(30 * time.Second)
	if len(schedule) <= 1 {
		return dynamicTrigger
	}
	if loc == nil {
		loc = time.Local
	}
	nextMilestone, _ := NextMilestone(completedAt, schedule, loc)
	cLocal := completedAt.In(loc)
	midnight := time.Date(cLocal.Year(), cLocal.Month(), cLocal.Day()+1, 0, 0, 0, 0, loc)
	if !nextMilestone.Before(midnight) {
		if dynamicTrigger.Before(nextMilestone) {
			return nextMilestone
		}
	}
	return dynamicTrigger
}

func (r *Runtime) isAntigravityDue(ctx context.Context, now, initialAnchor time.Time, beforeInitialAnchor bool, cfg Config, store *StateStore, file AuthFile) (bool, error) {
	credID := file.CredentialID()
	state := store.Credential(credID)

	if terminalDynamicFailure(FailureKind(state.FailureKind)) {
		nextAnchor, _ := NextMilestone(state.LastAttemptAt, cfg.Schedule, cfg.Location)
		if now.Before(nextAnchor) {
			return false, nil
		}
		if err := store.Update(ctx, credID, func(current *CredentialState) {
			current.FailureKind = ""
			current.Status = "waiting"
			current.Reason = "cycle_reset"
			for k, tgt := range current.Targets {
				tgt.FailureKind = ""
				tgt.Status = "waiting"
				tgt.Reason = "cycle_reset"
				current.Targets[k] = tgt
			}
		}); err != nil {
			return false, fmt.Errorf("reset Antigravity cycle: %w", err)
		}
		state = store.Credential(credID)
	}
	if !state.NextRetryAt.IsZero() && state.NextRetryAt.After(now) {
		return false, nil
	}

	hasDueTarget := false
	for _, key := range []string{TargetKeyGemini5h, TargetKeyClaude5h} {
		tgt, ok := state.Targets[key]
		if ok && terminalDynamicFailure(FailureKind(tgt.FailureKind)) {
			nextAnchor, _ := NextMilestone(tgt.LastAttemptAt, cfg.Schedule, cfg.Location)
			if !now.Before(nextAnchor) {
				if err := store.Update(ctx, credID, func(current *CredentialState) {
					curTgt := current.Targets[key]
					curTgt.FailureKind = ""
					curTgt.Status = "waiting"
					curTgt.Reason = "cycle_reset"
					current.Targets[key] = curTgt
				}); err != nil {
					return false, fmt.Errorf("reset Antigravity target cycle: %w", err)
				}
				tgt = store.Credential(credID).Targets[key]
			}
		}

		if !ok || (tgt.TargetTriggerAt.IsZero() && tgt.Status != "stabilizing" && tgt.FailureKind == "") {
			if !beforeInitialAnchor {
				hasDueTarget = true
			}
		} else if tgt.Status == "stabilizing" {
			if tgt.NextRetryAt.IsZero() || !tgt.NextRetryAt.After(now) {
				hasDueTarget = true
			}
		} else if !tgt.TargetTriggerAt.IsZero() && !now.Before(tgt.TargetTriggerAt) {
			hasDueTarget = true
		}
	}
	return hasDueTarget, nil
}

func (r *Runtime) calculateAntigravityWake(nowAfter, initialAnchor time.Time, beforeInitialAnchor bool, cfg Config, store *StateStore, file AuthFile, nextEarliestWake *time.Time) {
	state := store.Credential(file.CredentialID())
	if terminalDynamicFailure(FailureKind(state.FailureKind)) {
		nextAnchor, _ := NextMilestone(state.LastAttemptAt, cfg.Schedule, cfg.Location)
		target := nextAnchor
		if target.Before(nowAfter) {
			target = nowAfter
		}
		if nextEarliestWake.IsZero() || target.Before(*nextEarliestWake) {
			*nextEarliestWake = target
		}
		return
	}
	if !state.NextRetryAt.IsZero() {
		target := state.NextRetryAt
		if target.Before(nowAfter) {
			target = nowAfter
		}
		if nextEarliestWake.IsZero() || target.Before(*nextEarliestWake) {
			*nextEarliestWake = target
		}
		return
	}

	geminiTarget, hasGemini := state.Targets[TargetKeyGemini5h]
	claudeTarget, hasClaude := state.Targets[TargetKeyClaude5h]
	unanchored := !hasGemini || !hasClaude ||
		(geminiTarget.TargetTriggerAt.IsZero() && geminiTarget.Status != "stabilizing" && geminiTarget.FailureKind == "") ||
		(claudeTarget.TargetTriggerAt.IsZero() && claudeTarget.Status != "stabilizing" && claudeTarget.FailureKind == "")
	if unanchored && beforeInitialAnchor {
		if nextEarliestWake.IsZero() || initialAnchor.Before(*nextEarliestWake) {
			*nextEarliestWake = initialAnchor
		}
	}

	for _, tgt := range []TargetState{geminiTarget, claudeTarget} {
		if terminalDynamicFailure(FailureKind(tgt.FailureKind)) {
			nextAnchor, _ := NextMilestone(tgt.LastAttemptAt, cfg.Schedule, cfg.Location)
			target := nextAnchor
			if target.Before(nowAfter) {
				target = nowAfter
			}
			if nextEarliestWake.IsZero() || target.Before(*nextEarliestWake) {
				*nextEarliestWake = target
			}
			continue
		}
		if tgt.Status == "stabilizing" {
			if tgt.NextRetryAt.IsZero() || tgt.NextRetryAt.Before(nowAfter) {
				*nextEarliestWake = nowAfter
			} else if nextEarliestWake.IsZero() || tgt.NextRetryAt.Before(*nextEarliestWake) {
				*nextEarliestWake = tgt.NextRetryAt
			}
			continue
		}
		if !tgt.NextRetryAt.IsZero() {
			target := tgt.NextRetryAt
			if target.Before(nowAfter) {
				target = nowAfter
			}
			if nextEarliestWake.IsZero() || target.Before(*nextEarliestWake) {
				*nextEarliestWake = target
			}
			continue
		}
		if !tgt.TargetTriggerAt.IsZero() {
			target := tgt.TargetTriggerAt
			if target.Before(nowAfter) {
				target = nowAfter
			}
			if nextEarliestWake.IsZero() || target.Before(*nextEarliestWake) {
				*nextEarliestWake = target
			}
		}
	}
}

func (r *Runtime) runDynamicAntigravityJob(ctx context.Context, cfg Config, store *StateStore, file AuthFile) error {
	credID := file.CredentialID()
	state := store.Credential(credID)
	now := r.now()

	if terminalDynamicFailure(FailureKind(state.FailureKind)) {
		return nil
	}
	if !state.NextRetryAt.IsZero() && state.NextRetryAt.After(now) {
		return nil
	}

	hasUnanchored := false
	for _, key := range []string{TargetKeyGemini5h, TargetKeyClaude5h} {
		tgt, ok := state.Targets[key]
		if !ok || (tgt.TargetTriggerAt.IsZero() && tgt.Status != "stabilizing" && tgt.FailureKind == "") {
			hasUnanchored = true
			break
		}
	}

	if hasUnanchored {
		if err := r.bootstrapAntigravityCredential(ctx, cfg, store, file); err != nil {
			return err
		}
	}

	return r.executeDynamicAntigravityTargets(ctx, cfg, store, file)
}

func (r *Runtime) bootstrapAntigravityCredential(ctx context.Context, cfg Config, store *StateStore, file AuthFile) error {
	credID := file.CredentialID()
	targets := []struct {
		model  string
		key    string
		family string
	}{
		{AntigravityModelGemini, TargetKeyGemini5h, "gemini"},
		{AntigravityModelClaude, TargetKeyClaude5h, "claude"},
	}

	var successfulTargets []string
	var authFailed bool

	for _, t := range targets {
		if authFailed {
			break
		}
		currentTgt, exists := store.Credential(credID).Targets[t.key]
		if exists && (!currentTgt.TargetTriggerAt.IsZero() || currentTgt.Status == "stabilizing" || terminalDynamicFailure(FailureKind(currentTgt.FailureKind))) {
			continue
		}

		completedAt := r.now().UTC()
		body, err := json.Marshal(map[string]any{
			"model": t.model,
			"input": cfg.Prompt,
			"store": false,
		})
		if err != nil {
			if updateErr := store.Update(ctx, credID, func(current *CredentialState) {
				current.Provider = ProviderAntigravity
				if current.Targets == nil {
					current.Targets = make(map[string]TargetState)
				}
				tgt := current.Targets[t.key]
				tgt.Target = t.key
				tgt.ModelFamily = t.family
				tgt.SelectedModel = t.model
				tgt.Transport = TransportHostModel
				tgt.LastAttemptStatus = "failed"
				tgt.LastError = safeErrorMessage(err.Error())
				tgt.FailureKind = string(FailureBusiness)
				tgt.Failures++
				tgt.LastAttemptAt = completedAt
				current.Targets[t.key] = tgt
			}); updateErr != nil {
				return fmt.Errorf("persist bootstrap target failure: %w", updateErr)
			}
			continue
		}

		requestCtx, cancel := context.WithTimeoutCause(ctx, cfg.RequestTimeout, errors.New("Antigravity bootstrap timeout"))
		resp, execErr := r.host.ModelExecute(requestCtx, ModelExecuteRequest{
			EntryProtocol:  "openai-response",
			ExitProtocol:   "openai-response",
			Model:          t.model,
			Stream:         false,
			Body:           body,
			ForcedProvider: ProviderAntigravity,
			AuthID:         credID,
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

		if failure == FailureAuth {
			authFailed = true
			if updateErr := store.Update(ctx, credID, func(current *CredentialState) {
				current.Provider = ProviderAntigravity
				current.Status = "blocked"
				current.FailureKind = string(FailureAuth)
				current.LastError = sanitizedErr
				current.LastAttemptAt = completedAt
				current.Failures++
				if current.Targets == nil {
					current.Targets = make(map[string]TargetState)
				}
				for _, tgtInfo := range targets {
					tgt := current.Targets[tgtInfo.key]
					tgt.Target = tgtInfo.key
					tgt.ModelFamily = tgtInfo.family
					tgt.SelectedModel = tgtInfo.model
					tgt.Transport = TransportHostModel
					tgt.Status = "blocked"
					tgt.FailureKind = string(FailureAuth)
					tgt.LastError = sanitizedErr
					tgt.LastAttemptAt = completedAt
					tgt.Failures++
					current.Targets[tgtInfo.key] = tgt
				}
			}); updateErr != nil {
				return fmt.Errorf("persist bootstrap auth failure: %w", updateErr)
			}
			break
		}

		if updateErr := store.Update(ctx, credID, func(current *CredentialState) {
			current.Provider = ProviderAntigravity
			if current.Targets == nil {
				current.Targets = make(map[string]TargetState)
			}
			tgt := current.Targets[t.key]
			tgt.Target = t.key
			tgt.ModelFamily = t.family
			tgt.SelectedModel = t.model
			tgt.Transport = TransportHostModel
			tgt.LastAttemptAt = completedAt
			tgt.Attempts++
			if success {
				tgt.LastAttemptStatus = "success"
				tgt.LastPingAt = completedAt
				tgt.LastError = ""
				tgt.FailureKind = ""
				tgt.Successes++
				tgt.Status = "stabilizing"
				tgt.Reason = "awaiting_new_reset"
			} else {
				tgt.LastAttemptStatus = "failed"
				tgt.LastError = sanitizedErr
				tgt.FailureKind = string(failure)
				tgt.Failures++
				tgt.Status = "cooldown"
				tgt.Reason = "bootstrap_failure"
				tgt.NextRetryAt = r.now().Add(cfg.RetryCooldown)
			}
			current.Targets[t.key] = tgt
		}); updateErr != nil {
			return fmt.Errorf("persist bootstrap target status: %w", updateErr)
		}
		if success {
			successfulTargets = append(successfulTargets, t.key)
		}
	}

	if len(successfulTargets) > 0 {
		obs, qFail, qMsg, qRetryAfter := r.fetchAntigravityQuotaObservation(ctx, cfg, file)
		qDelay := cfg.RetryCooldown
		if qRetryAfter != nil {
			qDelay = *qRetryAfter
		}
		if updateErr := store.Update(ctx, credID, func(current *CredentialState) {
			current.Provider = ProviderAntigravity
			if current.Targets == nil {
				current.Targets = make(map[string]TargetState)
			}
			for _, targetKey := range successfulTargets {
				tgt := current.Targets[targetKey]
				if qFail != FailureNone {
					tgt.Status = "stabilizing"
					tgt.Reason = "usage_stabilization_retry"
					tgt.FailureKind = string(qFail)
					tgt.LastError = safeErrorMessage(qMsg)
					tgt.NextRetryAt = r.now().Add(qDelay)
				} else {
					reset, ok := obs.ResetForTarget(targetKey)
					if !ok || !reset.After(r.now().UTC()) {
						tgt.Status = "stabilizing"
						tgt.Reason = "usage_stabilization_retry"
						tgt.FailureKind = string(FailureRetryable)
						tgt.LastError = "quota reset_at is not in the future of completed bootstrap"
						tgt.NextRetryAt = r.now().Add(qDelay)
					} else {
						tgt.ObservedResetAt = reset
						tgt.TargetTriggerAt = reset.Add(cfg.AntigravityResetMargin)
						tgt.Status = "waiting"
						tgt.Reason = "trigger_scheduled"
						tgt.FailureKind = ""
						tgt.LastError = ""
						tgt.NextRetryAt = time.Time{}
					}
				}
				current.Targets[targetKey] = tgt
			}
		}); updateErr != nil {
			return fmt.Errorf("persist bootstrap quota anchoring: %w", updateErr)
		}
	}
	return nil
}

func (r *Runtime) executeDynamicAntigravityTargets(ctx context.Context, cfg Config, store *StateStore, file AuthFile) error {
	credID := file.CredentialID()
	now := r.now()
	targets := []struct {
		model  string
		key    string
		family string
	}{
		{AntigravityModelGemini, TargetKeyGemini5h, "gemini"},
		{AntigravityModelClaude, TargetKeyClaude5h, "claude"},
	}

	for _, t := range targets {
		state := store.Credential(credID)
		if terminalDynamicFailure(FailureKind(state.FailureKind)) {
			return nil
		}
		tgt, exists := state.Targets[t.key]
		if !exists {
			continue
		}
		if terminalDynamicFailure(FailureKind(tgt.FailureKind)) {
			continue
		}
		if !tgt.NextRetryAt.IsZero() && tgt.NextRetryAt.After(now) {
			continue
		}

		if tgt.Status == "stabilizing" {
			obs, qFail, qMsg, qRetryAfter := r.fetchAntigravityQuotaObservation(ctx, cfg, file)
			qDelay := cfg.RetryCooldown
			if qRetryAfter != nil {
				qDelay = *qRetryAfter
			}
			if updateErr := store.Update(ctx, credID, func(current *CredentialState) {
				current.Provider = ProviderAntigravity
				if current.Targets == nil {
					current.Targets = make(map[string]TargetState)
				}
				curTgt := current.Targets[t.key]
				if qFail != FailureNone {
					curTgt.FailureKind = string(qFail)
					curTgt.LastError = safeErrorMessage(qMsg)
					curTgt.NextRetryAt = r.now().Add(qDelay)
				} else {
					reset, ok := obs.ResetForTarget(t.key)
					if ok && reset.After(curTgt.ObservedResetAt) && reset.After(r.now().UTC()) {
						curTgt.LastProcessedResetAt = curTgt.ObservedResetAt
						curTgt.ObservedResetAt = reset
						curTgt.TargetTriggerAt = reset.Add(cfg.AntigravityResetMargin)
						curTgt.Status = "waiting"
						curTgt.Reason = "trigger_scheduled"
						curTgt.FailureKind = ""
						curTgt.LastError = ""
						curTgt.NextRetryAt = time.Time{}
					} else {
						curTgt.FailureKind = string(FailureRetryable)
						curTgt.LastError = "stabilization reset_at did not advance"
						curTgt.NextRetryAt = r.now().Add(qDelay)
					}
				}
				current.Targets[t.key] = curTgt
			}); updateErr != nil {
				return fmt.Errorf("persist stabilizing target observation: %w", updateErr)
			}
			continue
		}

		if tgt.TargetTriggerAt.IsZero() || now.Before(tgt.TargetTriggerAt) {
			continue
		}

		completedAt := r.now().UTC()
		body, err := json.Marshal(map[string]any{
			"model": t.model,
			"input": cfg.Prompt,
			"store": false,
		})
		if err != nil {
			continue
		}

		if updateErr := store.Update(ctx, credID, func(current *CredentialState) {
			current.Provider = ProviderAntigravity
			if current.Targets == nil {
				current.Targets = make(map[string]TargetState)
			}
			curTgt := current.Targets[t.key]
			curTgt.Status = "in_flight"
			curTgt.Reason = "dynamic_trigger"
			curTgt.LastAttemptAt = completedAt
			curTgt.LastAttemptStatus = "in_flight"
			curTgt.Attempts++
			current.Targets[t.key] = curTgt
		}); updateErr != nil {
			return fmt.Errorf("persist in_flight dynamic target: %w", updateErr)
		}
		requestCtx, cancel := context.WithTimeoutCause(ctx, cfg.RequestTimeout, errors.New("Antigravity execution timeout"))
		resp, execErr := r.host.ModelExecute(requestCtx, ModelExecuteRequest{
			EntryProtocol:  "openai-response",
			ExitProtocol:   "openai-response",
			Model:          t.model,
			Stream:         false,
			Body:           body,
			ForcedProvider: ProviderAntigravity,
			AuthID:         credID,
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
			if updateErr := store.Update(ctx, credID, func(current *CredentialState) {
				current.Provider = ProviderAntigravity
				current.Status = "blocked"
				current.FailureKind = string(FailureAuth)
				current.LastError = sanitizedErr
				current.LastAttemptAt = completedAt
				current.Failures++
				if current.Targets == nil {
					current.Targets = make(map[string]TargetState)
				}
				for _, tgtInfo := range targets {
					tgt := current.Targets[tgtInfo.key]
					tgt.Status = "blocked"
					tgt.FailureKind = string(FailureAuth)
					tgt.LastError = sanitizedErr
					tgt.LastAttemptAt = completedAt
					current.Targets[tgtInfo.key] = tgt
				}
			}); updateErr != nil {
				return fmt.Errorf("persist dynamic auth failure: %w", updateErr)
			}
			return nil
		}

		if updateErr := store.Update(ctx, credID, func(current *CredentialState) {
			current.Provider = ProviderAntigravity
			if current.Targets == nil {
				current.Targets = make(map[string]TargetState)
			}
			curTgt := current.Targets[t.key]
			curTgt.LastAttemptAt = completedAt
			if success {
				curTgt.LastAttemptStatus = "success"
				curTgt.LastPingAt = completedAt
				curTgt.LastError = ""
				curTgt.FailureKind = ""
				curTgt.Successes++
				curTgt.Status = "stabilizing"
				curTgt.Reason = "awaiting_new_reset"
				curTgt.TargetTriggerAt = time.Time{}
			} else {
				curTgt.LastAttemptStatus = "failed"
				curTgt.LastError = sanitizedErr
				curTgt.FailureKind = string(failure)
				curTgt.Failures++
				curTgt.Status = "cooldown"
				curTgt.Reason = "execution_failure"
				curTgt.NextRetryAt = r.now().Add(retryDelay)
			}
			current.Targets[t.key] = curTgt
		}); updateErr != nil {
			return fmt.Errorf("persist dynamic execution outcome: %w", updateErr)
		}
		if success {
			obs, qFail, qMsg, qRetryAfter := r.fetchAntigravityQuotaObservation(ctx, cfg, file)
			qDelay := cfg.RetryCooldown
			if qRetryAfter != nil {
				qDelay = *qRetryAfter
			}
			if updateErr := store.Update(ctx, credID, func(current *CredentialState) {
				current.Provider = ProviderAntigravity
				if current.Targets == nil {
					current.Targets = make(map[string]TargetState)
				}
				curTgt := current.Targets[t.key]
				if qFail != FailureNone {
					curTgt.FailureKind = string(qFail)
					curTgt.LastError = safeErrorMessage(qMsg)
					curTgt.NextRetryAt = r.now().Add(qDelay)
				} else {
					reset, ok := obs.ResetForTarget(t.key)
					if ok && reset.After(curTgt.ObservedResetAt) && reset.After(r.now().UTC()) {
						curTgt.LastProcessedResetAt = curTgt.ObservedResetAt
						curTgt.ObservedResetAt = reset
						curTgt.TargetTriggerAt = reset.Add(cfg.AntigravityResetMargin)
						curTgt.Status = "waiting"
						curTgt.Reason = "trigger_scheduled"
						curTgt.FailureKind = ""
						curTgt.LastError = ""
						curTgt.NextRetryAt = time.Time{}
					} else {
						curTgt.FailureKind = string(FailureRetryable)
						curTgt.LastError = "stabilization reset_at did not advance"
						curTgt.NextRetryAt = r.now().Add(qDelay)
					}
				}
				current.Targets[t.key] = curTgt
			}); updateErr != nil {
				return fmt.Errorf("persist post-ping quota observation: %w", updateErr)
			}
		}
	}
	return nil
}
