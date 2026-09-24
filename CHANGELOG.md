# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.7] - 2026-09-24

### Fixed

- Clamp overdue dynamic target triggers and retry cooldowns to immediate wake times, preventing staggered credential schedules from being starved by later future targets.

## [0.2.6] - 2026-09-23

### Fixed

- Clamp dynamic target trigger after the final daily schedule milestone (20:00) to the next calendar day's initial anchor milestone (`05:00`).
- Enforce mandatory 05:00 AM daily anchor ping for credentials that have not pinged yet today, preventing overnight drift from delaying daytime quota resets.

## [0.2.5] - 2026-09-21

### Added

- Dynamic per-credential five-hour scheduling anchored to observed `GET /backend-api/wham/usage` reset timestamps.
- Lossless integer Unix-second parsing for production `reset_at` payloads, with explicit candidate paths and stale-observation stabilization.
- Public Linux plugin installer script (`scripts/install/linux.sh`) to automatically resolve the latest release, verify SHA-256 checksums, and atomically install the plugin library to `~/cliproxyapi/plugins/linux/<arch>/cliproxyapi-auto-ping.so`.

### Changed

- Automatic requests run at `reset_at + 30s`; the margin mitigates boundary timing risk but does not guarantee upstream quota behavior.
- The first configured schedule entry anchors initial discovery; later entries start fresh cycles after terminal authentication, model, or business failures.
- Recoverable usage/inference failures retry after `retry_cooldown`; stale post-ping usage observations retry without sending duplicate inference requests.
- Reconfiguration preserves persisted targets, cooldowns, and terminal-cycle state while atomically replacing the scheduler.
- Plugin configuration enforces strict known-field decoding and rejects obsolete `scan_interval` and `activation_delay` settings.

### Removed

- Fixed wall-clock inference dispatch and milestone-specific retry/state bookkeeping.
- State schema versions 1 and 2; state v3 is a destructive cutover without migration shims.

## [0.2.2] - 2026-09-19

### Fixed

- Preserve scheduled milestones when a preceding batch or retry runs past the next milestone instead of recalculating from the completion time and skipping it.
- Retry temporary credential discovery/read failures without losing the milestone or permanently authentication-blocking a readable credential.
- Wait for overlapping manual pings and recheck their processed milestone before automatic dispatch, preventing both missed and duplicate pings.
- Cancel queued work on reconfiguration and retain catch-up when an older dispatch is still in flight.

## [0.1.2] - 2026-09-12

### Fixed

- Omit unsupported `max_output_tokens` parameter from direct HTTP and scheduler activation payloads, resolving upstream Codex Responses API `HTTP 400 Bad Request` failures (`{"detail":"Unsupported parameter: max_output_tokens"}`).
- Surface detailed upstream error messages (`detail` and `error.message`) in error logs and `state.json` instead of generic `Codex returned HTTP <status>`.
- Classify HTTP 400 and 404 responses as candidate model failures (`FailureModel`) during `auto` model selection mode, allowing the runtime to fall back to subsequent configured candidates (e.g. `gpt-5.6-luna`) before entering retry cooldown.

### Removed

- Remove `max_output_tokens` configuration field and manifest defaults across manifest, schema, parser, and documentation.

## [0.1.1] - 2026-09-11

### Changed

- Switch configuration field from `auto_ping_enabled` to negative boolean `auto_ping_disabled` (default: `false`) to ensure proper toggle presentation in CLIProxyAPI Management Center Web UI without host config pollution.

## [0.1.0] - 2026-09-10

### Added

- Initial release of Codex 5h Auto-Ping plugin for CLIProxyAPI (`cliproxyapi-auto-ping`).
- Automatic detection of Codex rolling 5-hour quota windows (`limit_window_seconds: 18000`).
- Background quota scanner and prompt trigger when reset boundaries are reached.
- Direct HTTP activation transport calling upstream Codex responses endpoint with account credentials.
- Scheduler boost fallback transport for temporary priority boosting.
- Persistent state tracking across restarts (`state.json`).
- Management API routes and status reporting with manifest identity.
