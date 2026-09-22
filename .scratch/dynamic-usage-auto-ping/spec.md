# Dynamic Usage-Anchored Auto-Ping Specification

Status: draft
Parent ADR: `docs/adr/0012-upstream-reset-observation-and-dynamic-rolling-schedule.md`

## 1. Problem Statement & Hypotheses

At fixed wall-clock milestones (`05:00, 10:00, 15:00, 20:00`), auto-ping requests dispatch at millisecond boundaries (`15:00:00.015`). While returning HTTP 200, the 5-hour rolling quota window remained sliding (`in 5h 0m 100%`). 

- *Working hypothesis*: The 5-hour quota window reset is tied to the server-side acceptance/completion timestamp of the prior request rather than the exact wall-clock milestone.
- *Risk*: Fixed milestone schedules cannot account for variable upstream processing latency or queue delays under bounded concurrency.

## 2. Architecture & Wire Contract

### 2.1 Initial Milestone Anchor Rule (05:00 Anchor)
- The plugin preserves the configured first daily milestone (`05:00` by default) as the initial anchor point.
- **Unanchored Startup**: When the plugin initializes or an eligible credential lacks an active dynamic trigger in `state.json`:
  - If startup occurs before `05:00`: the engine does NOT query `/wham/usage` or ping immediately; it sleeps until `05:00`.
  - At `05:00`: the engine dispatches the first observation to `GET https://chatgpt.com/backend-api/wham/usage` for all eligible credentials.
  - If startup occurs after `05:00` on the current calendar day: it performs catch-up by querying `/wham/usage` immediately for unanchored credentials to establish their initial `reset_at`.
- **Pre-Anchor Usage Failure Policy**:
  - If the initial `/wham/usage` call at `05:00` fails with a transient network/HTTP error (e.g. 500/502/503/429/timeout), the engine retries with backoff bounded by `retry_cooldown`. It does NOT trigger a blind inference ping.
  - Consistent with ADR-0011, authentication failures (HTTP 401/403) are terminal only for the current attempt cycle. The credential persists `status: blocked` with `failure_kind: auth`, is not permanently suspended, and starts fresh at the next configured anchor milestone.

### 2.2 Wire Payload Parsing Contract
The parser decodes JSON strictly using `json.NewDecoder` with `UseNumber()`:
- **Primary Contract (Confirmed in Production)**:
  - `"reset_at": 1790084449` (Unix timestamp in integer seconds). Note that existing documentation carrying RFC3339 strings is outdated.
  - Evaluated strictly via `json.Number.Int64()`, converted to `time.Unix(sec, 0).UTC()`.
  - Floating-point representations or truncated values are strictly rejected as invalid payloads (`ErrInvalidUsage`) to prevent silent precision loss.
- **Defensive Fallback Contract (Requires Fixture Tests)**:
  - Millisecond Unix timestamp (`>= 1_000_000_000_000`) converted via `time.UnixMilli`.
  - RFC3339 string format (`time.RFC3339Nano` / `time.RFC3339`).
- **5-Hour Window Selection**:
  - Match `limit_window_seconds == 18000` only at the explicit roots `rate_limits_by_limit_id.codex`, `rate_limit`, `rate_limits`, or the root mapping, including their direct child window objects.
  - Do not recursively inspect unrelated payload branches. Reject a candidate containing multiple matching direct windows as `ErrInvalidUsage` rather than guessing by key name or map order.

### 2.3 Dynamic Trigger & Risk Margin
- For each credential: `target = reset_at + 30s`.
- The 30-second buffer is an operational risk margin designed to avoid firing before server-side window expiration. It is not an absolute mathematical guarantee against arbitrary upstream clock skew or extended outages.

### 2.4 Upstream Staleness & Post-Ping Stabilization
Because `/wham/usage` may be cached or eventually consistent:
- After a successful inference ping, the engine polls `/wham/usage` with a short stabilization delay (e.g. 5s) to observe the updated window.
- The next cycle is anchored only when a strictly advanced `new_reset_at > previous_reset_at` is confirmed.
- If `/wham/usage` repeatedly returns the stale `reset_at` past a bounded timeout (e.g. 2 minutes), the engine marks the cycle failed-retryable and retries via `retry_cooldown`.

## 3. Persistent State Schema & Version 3 Cutover

- **State Schema Version 3**:
  `state.json` updates to version 3.
  ```json
  {
    "version": 3,
    "credentials": {
      "codex-account-a": {
        "credential_id": "codex-account-a",
        "provider": "codex",
        "status": "waiting",
        "reason": "trigger_scheduled",
        "observed_reset_at": "2026-09-22T08:00:03Z",
        "target_trigger_at": "2026-09-22T08:00:33Z",
        "last_processed_reset_at": "2026-09-22T03:00:03Z",
        "last_ping_at": "2026-09-22T03:00:05Z",
        "last_attempt_status": "success",
        "attempts": 1,
        "successes": 1
      }
    }
  }
  ```
- **Strict Rejection of Prior Versions**:
  - Per project engineering principles (no compatibility layers or silent data destruction), existing `version: 1` and `version: 2` state files are **strictly rejected** on startup with an actionable error (`unsupported state schema version: please remove stale state file`).
  - The runtime does not silently overwrite or reset prior state files.
  - Active version 3 files restore persisted `target_trigger_at` without duplicate initial queries.

## 4. Verification & Testing Seams

1. **Parser Tests**: Lossless decoding of `"reset_at": 1790084449` and rejection of float precision loss.
2. **Initial Anchor (05:00) Tests**: Verifying that startup before 05:00 waits until 05:00 before querying usage, and post-05:00 catch-up triggers immediately.
3. **Usage Failure Handling**: Verifying transient `/wham/usage` errors back off via `retry_cooldown`, and 401/403 errors terminate only the current cycle per ADR-0011 without permanent suspension.
4. **Scheduler Synctest**: Verification that dispatches trigger at `observed_reset_at + 30s` and handle post-ping stabilization cleanly.
5. **State Cutover Tests**: Verifying that version 1 and version 2 state files return explicit rejection errors, and version 3 state files round-trip losslessly.
6. **Management & Lifecycle Tests**: Verifying `/status`, `/diagnostics`, and `/ping` behavior under dynamic triggers, plus race-free reconfiguration and shutdown.
