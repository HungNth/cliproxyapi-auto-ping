# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Retry recoverable Auto-Ping failures (timeouts, transport, HTTP 429, 5xx) with 1-minute default cooldown and upstream `Retry-After` header support until success, the next milestone, midnight in the configured timezone, or reconfiguration.
- Distinguish current-cycle attempted milestone and failure classification from success-only processed milestones in state document v2.

### Changed

- Default `retry_cooldown` changed from `2m` to `1m`.
- Remove fixed 3-attempt retry cap on recoverable failures within an active milestone cycle.
- Prior authentication failures and temporary cooldowns no longer block credentials on subsequent schedule milestones.
- Startup catch-up threshold (< 1 hour) removed: startup now always catches up the latest elapsed milestone of the current calendar day in the configured schedule timezone.
- Reconfiguration performs atomic handoff by draining old in-flight scheduler execution before starting replacement schedules.
- Plugin configuration enforces strict known-field decoding and rejects obsolete `scan_interval` and `activation_delay` settings.

### Removed

- State schema version 1 is unsupported; state v2 destructive cutover is enforced without legacy migration shims.

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
