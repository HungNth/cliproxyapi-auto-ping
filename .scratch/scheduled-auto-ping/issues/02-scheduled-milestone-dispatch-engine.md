# 02: Scheduled Milestone Dispatch Engine

**Parent:** `.scratch/scheduled-auto-ping/spec.md`

**What to build:**
The runtime replaces the background ticker with a schedule milestone timer that sleeps until the next upcoming `HH:MM` milestone in the configured timezone. When the milestone arrives, it discovers all active, eligible Codex credentials and dispatches Auto-Ping streaming inference concurrently up to `max_concurrency`. Pre-flight usage checks are completely bypassed. Processed milestones are recorded in the state store so duplicate dispatches for the same milestone do not occur.

**Blocked by:** 01: Schedule Configuration & Manifest Schema

**Status:** resolved

**Testing Seam:**
Runtime integration tests with `fakeHost` and `fakeClock` exercising milestone calculation, timer wake-up, and concurrent dispatch without usage polling.

**Demo Path:**
`go test ./internal/autoping/... -run "TestScheduledMilestoneDispatch.*"`

**Acceptance Criteria:**
- [x] Runtime accurately calculates the duration to the next scheduled milestone in the configured timezone.
- [x] At milestone arrival, Auto-Ping streaming requests are sent to all eligible credentials without calling the upstream usage endpoint.
- [x] Credentials listed in `exclude_credentials` are skipped during milestone dispatch.
- [x] Concurrent requests are bounded by `max_concurrency`.
- [x] Processed milestone key (date and milestone time) is persisted in the state store, preventing re-execution if another scan/trigger occurs within the same minute.
- [x] When `auto_ping_disabled: true`, no milestone timer or dispatch runs.
