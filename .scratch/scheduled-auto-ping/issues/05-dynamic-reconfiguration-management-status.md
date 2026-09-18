# 05: Dynamic Reconfiguration & Management Status Reporting

**Parent:** `.scratch/scheduled-auto-ping/spec.md`

**What to build:**
When `Configure` is called on a running runtime with updated `schedule` or `timezone`, the active timer cleanly stops and reschedules for the new settings without losing existing processed milestone history. The management status payload (`GET /status`) reports the active `schedule`, `timezone`, next milestone timestamp, and last processed milestone. Plugin shutdown halts any active timers and waits for in-flight requests cleanly.

**Blocked by:** 03: Startup Catch-Up & Milestone Window Threshold, 04: Milestone Retry Policy & Auth Blocking

**Status:** resolved

**Testing Seam:**
Management and runtime lifecycle tests verifying `/status` response contents, reconfiguration rescheduling, and clean shutdown.

**Demo Path:**
`go test ./internal/autoping/... -run "TestManagement.*|TestReconfiguration.*"`

**Acceptance Criteria:**
- [x] Updating config with a new schedule (e.g. from 4 milestones to 3) reschedules the next milestone trigger accurately.
- [x] `GET /status` returns the list of scheduled milestones, configured timezone, next upcoming milestone time, and last milestone completed.
- [x] Calling `Shutdown` cancels any pending timers and waits cleanly for active goroutines without leaks.
- [x] Management registration registers plugin metadata with the updated schema and fields.
