# 03: Startup Catch-Up & Milestone Window Threshold

**Parent:** `.scratch/scheduled-auto-ping/spec.md`

**What to build:**
When the runtime starts up or enables auto-ping, it evaluates whether the most recent milestone of the day has already been processed. If the milestone was not processed today and the time until the next milestone is at least 1 hour away, it immediately dispatches Milestone Catch-Up to all eligible credentials. If the time until the next milestone is less than 1 hour away, it skips catch-up and waits for the next scheduled milestone.

**Blocked by:** 02: Scheduled Milestone Dispatch Engine

**Status:** resolved

**Testing Seam:**
Runtime integration tests with `fakeHost` and `fakeClock` starting at various times of day across the 1-hour boundary.

**Demo Path:**
`go test ./internal/autoping/... -run "TestStartupCatchUp.*"`

**Acceptance Criteria:**
- [x] If plugin starts up after a milestone (e.g. at 07:30 after 05:00) with >= 1 hour until the next milestone (10:00 is 2.5h away), and 05:00 was not processed today, catch-up dispatches Auto-Ping to all eligible credentials.
- [x] If plugin starts up within 1 hour of the next milestone (e.g. at 09:15 before 10:00), catch-up is skipped, and the engine waits until 10:00.
- [x] If the latest milestone was already processed earlier today (recorded in state store), catch-up is skipped upon restart.
- [x] If startup occurs before the first milestone of the day (e.g. 04:00 before 05:00), catch-up does not trigger.
