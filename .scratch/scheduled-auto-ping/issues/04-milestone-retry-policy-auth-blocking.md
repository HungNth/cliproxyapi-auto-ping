# 04: Milestone Retry Policy & Auth Blocking

**Parent:** `.scratch/scheduled-auto-ping/spec.md`

**What to build:**
When an Auto-Ping fails at a milestone due to transient transport or server errors (timeout, connection reset, 5xx), the runtime retries the request using `retry_cooldown` up to 3 attempts for that credential before abandoning the milestone. When an Auto-Ping fails due to authentication error (401/403), retrying stops immediately, the credential version is recorded as blocked, and future milestone dispatches skip the credential until its credential version changes.

**Blocked by:** 02: Scheduled Milestone Dispatch Engine

**Status:** resolved

**Testing Seam:**
Runtime integration tests with `fakeHost` asserting retry schedule, max attempt limits, and auth blocking behavior.

**Demo Path:**
`go test ./internal/autoping/... -run "TestMilestoneRetry.*|TestMilestoneAuthBlock.*"`

**Acceptance Criteria:**
- [x] Transient failures trigger retry after `retry_cooldown`.
- [x] Retries are limited to a maximum of 3 attempts per credential per milestone.
- [x] If a subsequent milestone arrives while retries are pending, the new milestone supersedes pending retries.
- [x] Authentication failure (401/403) immediately marks the credential as blocked with reason `credential_unchanged_after_auth_failure` and zero retries.
- [x] Updating the credential's version key unblocks the credential on subsequent milestone dispatches.
