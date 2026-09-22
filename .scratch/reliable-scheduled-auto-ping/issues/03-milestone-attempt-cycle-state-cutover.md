# 03: Cut Over to Milestone Attempt Cycle State

**Parent:** `../spec.md`

**What to build:** Persist enough per-Credential Milestone Attempt Cycle state to distinguish an unattempted milestone, recoverable cooldown, terminal current-cycle failure, and successful Processed Milestone across restart, without treating attempted or failed work as processed.

**Blocked by:** None (can start immediately)

**Status:** resolved

**Testing seam:** Configure the plugin through the lifecycle boundary with temporary state storage, exercise attempt outcomes through the fake Host, reload the runtime, and inspect lifecycle errors and management status.

**Demo path:** Persist recoverable, terminal, and successful outcomes, restart, and show that each state is restored distinctly; then load the prior schema and show an explicit unsupported-version error.

- [x] The state schema version is incremented as a destructive cutover.
- [x] Prior state versions are rejected with a clear startup or configuration error.
- [x] No migration, compatibility decoder, alias, or fallback for prior state is added.
- [x] The per-Credential processed milestone marker remains success-only and is written only after valid upstream success and atomic persistence.
- [x] Current-cycle state separately records the attempted milestone, failure class or status, informational retry count, and pending retry time when applicable.
- [x] Retry count is retained for diagnostics but does not impose an attempt cap.
- [x] Authentication-version blocking state is removed because it no longer gates later milestones.
- [x] Any batch-level last-milestone marker remains diagnostics-only and cannot gate per-Credential dispatch.
- [x] Reloaded state and management status distinguish no attempt, in flight, recoverable cooldown, terminal failure, and processed success.
- [x] State, status, diagnostics, and logs continue to exclude credential secrets.
- [x] Operator documentation explains that an old state file must be removed or a new `state_path` selected before enabling the release.
