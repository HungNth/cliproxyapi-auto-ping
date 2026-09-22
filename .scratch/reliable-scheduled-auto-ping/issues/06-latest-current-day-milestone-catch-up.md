# 06: Catch Up Only the Latest Current-Day Milestone

**Parent:** `../spec.md`

**What to build:** On startup, enablement, wake-up, or successful reconfiguration, catch up only the latest elapsed Schedule Milestone from the current calendar day in the configured schedule timezone, while preserving fresh-cycle, retry, and no-overlap guarantees.

**Blocked by:** 02: Make Scheduler Reconfiguration an Atomic Handoff; 05: Start a Fresh Attempt at Every Schedule Milestone

**Status:** resolved

**Testing seam:** Use the plugin lifecycle boundary with the fake Host and synthetic time for full startup, reconfiguration, restart, and wake-up scenarios; retain the existing pure schedule-resolver tests for ordinary configured-timezone calculations without adding DST-specific acceptance policy.

**Demo path:** Start before the first milestone, between milestones, at `09:59`, and after multiple missed milestones; show the expected catch-up request, next-milestone handoff, persisted state, and management status.

- [x] The prior one-hour catch-up threshold is removed.
- [x] Startup and enablement catch up the latest elapsed milestone of the current calendar day in the configured schedule timezone.
- [x] Startup before that day's first milestone performs no prior-day catch-up and waits for the first milestone.
- [x] Multiple elapsed milestones coalesce to the latest one instead of replaying every missed milestone.
- [x] Startup at `09:59` may run catch-up for `05:00`; the `10:00` cycle supersedes pending old retries and waits for any same-credential catch-up request already in flight.
- [x] Pending work from a previous configured-timezone calendar date never carries into the new date.
- [x] A recoverable current-cycle retry resumes at its persisted time when still valid.
- [x] A terminal current-cycle failure is not repeated merely because the plugin restarted.
- [x] A milestone that becomes due during reconfiguration drain is evaluated under the replacement configuration.
- [x] Management status accurately reports the configured schedule, timezone, next milestone, active cycle, retry timing, and success-only processed milestone.
- [x] Existing Go and configured-timezone DST behavior remains unchanged and is not promoted to a new acceptance policy.
- [x] README, changelog, ADR references, and operator state-cutover instructions describe the delivered behavior consistently.
- [x] The complete focused lifecycle suite, race-sensitive scheduler checks, and shared-library build verification pass.
