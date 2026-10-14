# 04: Advance and recover independent reset-aligned target cycles

**What to build:** Keep anchored Gemini and Claude/GPT Auto-Ping Targets running independently at their own reset times, with durable waiting, in-flight, stabilizing, cooldown, and terminal transitions across retries, restarts, Schedule Milestones, and reconfiguration. A successful inference must never be duplicated merely because quota observation is stale or unavailable.

**Blocked by:** 03: Bootstrap Antigravity targets at Schedule Milestones.

**Status:** resolved

**Parent specification:** Google Antigravity Reset-Aligned Auto-Ping Specification.

**Testing seam:** Use the Runtime fake Host and synthetic clock to advance staggered target resets, inject host/quota outcomes, restart from persisted state, and reconfigure schedule and reset margin while work is pending.

**Demo:** Anchor Gemini and Claude/GPT to different reset times, advance through both triggers, return stale quota after one successful ping, restart while stabilizing, then provide a future reset and show exactly one inference per processed boundary.

- [x] An anchored target becomes due only at its selected reset plus `antigravity_reset_margin`; healthy Antigravity targets ignore ordinary daily milestones.
- [x] A due target persists in-flight state before host execution and enters durable stabilizing state immediately after successful inference.
- [x] Stabilizing work performs quota observation only, including after restart, and never resends the successful inference until a future reset establishes a new cycle.
- [x] A future selected reset returns only that target to waiting and cannot advance the sibling target.
- [x] Recoverable transport, timeout, HTTP 429, upstream 5xx, temporary credential-read, and quota-availability failures use Retry-After when present or the shared retry cooldown otherwise.
- [x] Target-specific model and business failures are terminal only for that target's current Attempt Cycle.
- [x] Confirmed credential authentication failure marks both targets terminal and prevents queued sibling execution.
- [x] Terminal target or credential state starts a fresh Attempt Cycle at the next Schedule Milestone or Schedule Catch-Up without forcing healthy anchored targets to ping.
- [x] Waiting, cooldown, stabilizing, and terminal target state survives restart without bootstrap or inference duplication.
- [x] Changing `antigravity_reset_margin` recalculates unprocessed Antigravity triggers from their observed reset; an overdue recalculated trigger becomes immediately due.
- [x] Schedule or timezone reconfiguration changes future bootstrap and terminal-recovery milestones without converting anchored targets into daily jobs.
- [x] Scheduler replacement, queued work cancellation, global concurrency, and per-credential no-overlap retain the existing race-free lifecycle guarantees.
- [x] State writes remain atomic, version 3 remains authoritative, and existing Codex schedules and fields round-trip unchanged.

## Answer

Delivered independent reset-aligned target cycles and terminal recovery:
1. Staggered target resets fire independently at `observed_reset + margin`, with in-flight state and post-inference stabilizing observation.
2. Stabilizing state survives restart and executes observation-only without duplicate inference pings.
3. Changing `antigravity_reset_margin` recalculates active Antigravity triggers from observed resets while leaving Codex triggers intact.
4. Target-level terminal failures are isolated and reopen at the next Schedule Milestone.
5. All `store.Update` calls check and propagate errors, ensuring state durability.
6. All Ticket 04 behavioral tests pass cleanly.
