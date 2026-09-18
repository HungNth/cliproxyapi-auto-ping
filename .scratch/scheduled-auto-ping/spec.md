# Scheduled Auto-Ping Milestones

Status: resolved

## Problem Statement

Currently, the auto-ping plugin continuously polls the upstream Codex usage endpoint every 60 seconds to observe dynamic `reset_at` timestamps and detect sliding windows. This creates unnecessary background network traffic, exposes the plugin to upstream usage endpoint downtime and payload schema changes, and produces unpredictable activation timings across different credentials.

Operators want predictable, fixed activation times throughout the day (such as 05:00, 10:00, 15:00, and 20:00) so that their Five-Hour Windows are freshly activated during daytime working hours, without constant background polling.

## Solution

Replace the continuous dynamic observation loop with a deterministic Scheduled Auto-Ping engine.

The plugin provides a daily schedule list (defaulting to `["05:00", "10:00", "15:00", "20:00"]`) and a timezone setting (defaulting to `"Local"`). At each scheduled milestone, the runtime activates all eligible Codex Credentials concurrently using minimal inference requests without pre-flight quota queries.

If the plugin starts up between milestones, Milestone Catch-Up activates the latest elapsed milestone if it was not already processed today and the next milestone is at least 1 hour away. Failed activations retry up to 3 times with a configurable cooldown.

Obsolete configuration keys (`scan_interval`, `activation_delay`) and usage polling logic are completely removed per engineering guidelines.

## User Stories

1. As a plugin operator, I want the plugin to trigger Auto-Ping by default at 05:00, 10:00, 15:00, and 20:00 daily, so that my Codex Five-Hour Windows are ready throughout active working hours.
2. As a plugin operator, I want to configure a custom list of daily milestones (e.g. `["06:00", "12:00", "18:00"]`) in `HH:MM` 24-hour format, so that activations match my team's specific working hours.
3. As a plugin operator, I want to configure the timezone for the schedule (e.g. `"Local"` or `"Asia/Ho_Chi_Minh"`), so that milestone triggers respect the correct wall-clock time regardless of host server UTC settings.
4. As a plugin operator, I want the plugin to dispatch Auto-Ping to all active, non-excluded credentials concurrently up to `max_concurrency`, so that activations complete promptly at each milestone.
5. As a plugin operator, I want excluded credentials listed in `exclude_credentials` to be skipped during milestone activations, so that specific accounts are never auto-pinged.
6. As a plugin operator, I want credentials blocked due to authentication failure (401/403) to be skipped at milestones, so that invalid credentials do not spam upstream APIs.
7. As a plugin operator, I want an auth-blocked credential to automatically become eligible again when its credential version changes, so that refreshed tokens are picked up without restarting the plugin.
8. As a plugin operator, when CLIProxyAPI or the plugin starts up between milestones, I want it to perform Milestone Catch-Up for the latest elapsed milestone of today if it has not yet been processed, so that quota is available after a reboot.
9. As a plugin operator, when CLIProxyAPI starts up less than 1 hour before the next upcoming milestone, I want it to skip Milestone Catch-Up, so that two Auto-Ping batches are not executed back-to-back.
10. As a plugin operator, if an Auto-Ping request fails due to transient network or 5xx errors, I want it to retry up to 3 times after `retry_cooldown`, so that transient failures do not leave quota unactivated.
11. As a plugin operator, if an Auto-Ping request fails due to invalid auth (401/403), I want it to fail fast without retrying and mark the credential blocked, preventing repeated auth rejections.
12. As a plugin operator, I want processed milestones to be persisted in the state store, so that restarting the host service or plugin never duplicates a milestone that was already pinged.
13. As a plugin operator, when `auto_ping_disabled: true` is configured, I want all scheduled timers and catch-up pings to remain completely inactive.
14. As an operator checking plugin health, I want `GET /status` to report the configured schedule, timezone, next upcoming milestone time, and per-credential status.
15. As a plugin operator, when I update the schedule or timezone in configuration, I want the runtime to recalculate the next milestone timer immediately without dropping persisted milestone state.
16. As a host process, when shutting down the plugin, I want running timers or in-flight requests to terminate cleanly and boundedly without deadlocks or hanging goroutines.

## Implementation Decisions

- Configuration Schema Changes:
  - Add `schedule`: list of strings formatted as `"HH:MM"` in 24-hour time. Validated on load (hours 0-23, minutes 0-59). Default shipped in manifest: `["05:00", "10:00", "15:00", "20:00"]`. Duplicates removed; milestones sorted chronologically.
  - Add `timezone`: string specifying timezone location. Validated with `time.LoadLocation`. Default shipped in manifest: `"Local"`. Supports `"Local"`, `"UTC"`, or valid IANA location names (e.g. `"Asia/Ho_Chi_Minh"`).
  - Remove obsolete configuration fields: `scan_interval` and `activation_delay` are completely removed from manifest defaults, schema validation, and runtime structs without backward compatibility aliases.
- Runtime Scheduling Engine:
  - Replaces the periodic ticker-based scanner loop.
  - Computes the next upcoming `Schedule Milestone` wall-clock time in the configured timezone.
  - Uses a timer-based wait (`time.NewTimer` / channel select with context cancellation) to wake up precisely at the milestone.
  - Supports dynamic reconfiguration: when `Configure` is called, any existing timer is stopped and rescheduled for the new settings.
- Dispatch Logic:
  - At each milestone, discovers all `Eligible Credentials` from the host.
  - Directly dispatches Auto-Ping streaming inference requests using existing transport mechanisms (`direct_http` with optional `scheduler_boost_fallback`, or `scheduler_boost`).
  - Completely bypasses pre-flight `codexUsageURL` requests.
  - Concurrency is bounded by `max_concurrency`.
- Milestone Catch-Up on Startup:
  - When the runtime initializes or enables auto-ping:
    - Finds the latest milestone that should have occurred earlier on the current calendar day (in the configured timezone).
    - If no milestone has occurred yet today, catch-up is not applicable.
    - If a milestone occurred, checks whether the state store records that milestone as already processed for the day.
    - Calculates the duration remaining until the *next* upcoming milestone.
    - If remaining duration is < 1 hour, catch-up is skipped.
    - If remaining duration is >= 1 hour, catch-up dispatches Auto-Ping for all Eligible Credentials.
- Retry Policy:
  - Transient errors (transport failure, timeout, 5xx server errors) retry with delay defined by `retry_cooldown`.
  - Max retry count is capped at 3 attempts per credential per milestone.
  - Next retry timer is evaluated within the active milestone cycle; if the next milestone arrives before all retries complete, the newer milestone takes precedence.
  - Authentication errors (401, 403) immediately block the credential version and perform zero retries.
- State Persistence:
  - `CredentialState` records the last processed milestone identifier (e.g. `2026-09-18#05:00`) and timestamp.
  - State file continues to persist atomically via `StateStore`.
- Management Center & Status API:
  - `GET /status` returns active schedule details (`schedule`, `timezone`, `next_milestone_at`, `last_milestone_at`) alongside credential summaries.

## Testing Decisions

- Test External Behavior: Tests exercise the public `Runtime` methods (`NewRuntime`, `Configure`, `Shutdown`, and scheduler execution) against `fakeHost` and `fakeClock`. Tests assert requests made, credentials pinged, and persisted state rather than private internal timer state.
- Modules Tested:
  - `Runtime`: Milestone triggers, startup catch-up logic (>= 1h vs < 1h), restart persistence without duplicate pings, retry cooldown with max 3 attempts, auth blocking, and graceful shutdown.
  - `Config`: Parsing of `schedule` (`HH:MM` format) and `timezone`, default fallback, validation errors on invalid time strings or unknown timezones, and rejection of obsolete keys.
  - `Manifest`: Default values, schema validation, and drift tests for embedded plugin manifest.
  - `Management`: Registration and status endpoint payload verification with schedule metadata.
- Prior Art:
  - Existing tests in `runtime_test.go`, `config_test.go`, and `manifest_test.go` serve as reference fixtures.

## Out of Scope

- Cron syntax support (`*/5 * * * *`, cron expressions with day/month wildcards).
- Backward compatibility or migration fallbacks for `scan_interval` and `activation_delay`.
- Host-level UI modifications in `Cli-Proxy-API-Management-Center`.

## Further Notes

- Governed by ADR-0010 (`Scheduled auto-ping milestones`) and the domain glossary in `CONTEXT.md`.
