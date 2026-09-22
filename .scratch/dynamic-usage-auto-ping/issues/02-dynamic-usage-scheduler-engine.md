# Issue 02: Initial 05:00 Anchor, Usage Failure Policy, and Dynamic Scheduler Loop

Status: ready-for-agent
Parent: `.scratch/dynamic-usage-auto-ping/spec.md`

## Required Behavior

Update the scheduler loop to use the initial 05:00 anchor rule and dynamic rolling trigger loop. At startup before 05:00, sleep until 05:00. At 05:00 (or startup catch-up after 05:00), query `GET https://chatgpt.com/backend-api/wham/usage` for unanchored credentials. If usage fails with a transient error, retry after `retry_cooldown` without blind pinging. When usage succeeds, set `target = reset_at + 30s` and sleep until `target`. Dispatch inference at `target`, then verify post-ping stabilization to schedule the next cycle.

## Acceptance Criteria

- [ ] Startup before 05:00 waits until 05:00 before querying `/wham/usage`.
- [ ] Startup after 05:00 on the current day performs immediate catch-up query on `/wham/usage`.
- [ ] Transient usage errors enter cooldown without firing blind inference pings.
- [ ] Trigger fires at `reset_at + 30s`.
- [ ] Post-ping verification requires `new_reset_at > old_reset_at` before anchoring the next cycle.

## Testing Seam

Integration tests with `synctest` in `internal/autoping/runtime_test.go`.
