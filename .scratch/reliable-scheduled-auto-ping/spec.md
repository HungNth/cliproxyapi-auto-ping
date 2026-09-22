# Reliable Scheduled Auto-Ping Cycles

Status: resolved

## Problem Statement

The operator configures predictable daily Schedule Milestones such as `05:00`, `10:00`, `15:00`, and `20:00` to start Codex Five-Hour Windows at useful times. A successful earlier Auto-Ping, a prior authentication failure, exhausted retries, reconfiguration, process suspension, or scheduler contention must not silently prevent a later milestone from making its own attempt.

The current runtime does not fully provide that contract. It caps each credential at three attempts per milestone, defaults retries to two minutes, auth-blocks unchanged credentials across later milestones, can start a replacement scheduler before the canceled scheduler has exited, and silently accepts obsolete configuration keys. These behaviors can delay or omit the Auto-Ping that the operator expects at the next configured milestone.

## Solution

Every representable Schedule Milestone starts a fresh Milestone Attempt Cycle for every Eligible Credential. Explicit opt-outs remain authoritative, but temporary availability, cooldown, and prior failure state do not suppress a later milestone.

A Recoverable Auto-Ping Failure retries after the later of the configured `retry_cooldown` and a valid upstream `Retry-After`. The default cooldown becomes one minute and remains configurable. Retries have no attempt-count cap, but the cycle ends at whichever occurs first: success, the next Schedule Milestone, a configured-timezone date change, or schedule/timezone reconfiguration. Authentication, model, and business failures end the current cycle immediately and wait for the next milestone.

Startup and wake-up catch up only the latest elapsed valid milestone of the current calendar day in the configured schedule timezone. Reconfiguration performs an atomic scheduler handoff: cancel old automatic work, wait for the old scanner to exit, then evaluate and start the replacement schedule. Same-credential requests never overlap. Processed Milestone state remains success-only.

## User Stories

1. As a plugin operator, I want every configured Schedule Milestone to start a new attempt cycle, so that a successful `05:00` Auto-Ping never suppresses the `10:00` Auto-Ping.
2. As a plugin operator, I want the default milestones to remain `05:00`, `10:00`, `15:00`, and `20:00`, so that existing daytime scheduling intent remains clear.
3. As a plugin operator, I want schedule evaluation to use the configured timezone, so that wall-clock milestones match my working location.
4. As a plugin operator, I want precise milestone timers instead of continuous minute polling while the system is healthy, so that reliability does not require unnecessary wake-ups.
5. As a plugin operator, I want a temporarily unavailable credential to be attempted at each milestone, so that stale host availability state cannot suppress a real Codex request.
6. As a plugin operator, I want a credential in cooldown from an earlier cycle to receive a fresh attempt at the next milestone, so that old retry state cannot move later Five-Hour Windows.
7. As a plugin operator, I want a credential that previously returned 401 or 403 to be attempted again at the next milestone, so that authentication failure state does not permanently suppress scheduled work.
8. As a plugin operator, I want `auto_ping_disabled: true` to stop all automatic work, so that the explicit plugin opt-out remains authoritative.
9. As a plugin operator, I want `exclude_credentials` to prevent automatic requests for named credentials, so that per-credential opt-out remains authoritative.
10. As a plugin operator, I want disabled and revoked credentials to remain ineligible, so that host and operator safety controls are respected.
11. As a plugin operator, I want recoverable credential-discovery and credential-read failures to retry, so that temporary host failures do not lose a milestone.
12. As a plugin operator, I want network, timeout, stream, HTTP 429, and upstream 5xx failures to retry, so that temporary upstream failures can recover within the current cycle.
13. As a plugin operator, I want the default retry delay to be one minute, so that temporary failures recover promptly.
14. As a plugin operator, I want `retry_cooldown` to remain configurable, so that deployments can tune retry pressure without changing code.
15. As a plugin operator, I want a valid `Retry-After` value to delay the next retry when it is later than `retry_cooldown`, so that the plugin respects upstream rate limits.
16. As a plugin operator, I want invalid or absent `Retry-After` values to fall back to `retry_cooldown`, so that malformed upstream metadata cannot disable retries.
17. As a plugin operator, I want recoverable failures to keep retrying without a fixed attempt-count cap, so that a fourth or later attempt can still activate the current window.
18. As a plugin operator, I want retries to stop when the next milestone arrives, so that old work cannot delay the newer intended window.
19. As a plugin operator, I want retries to stop when the configured schedule timezone's calendar date changes, so that the final milestone of one day never retries through the night into the next day.
20. As a plugin operator, I want the first milestone of the next day to start a fresh cycle with no retry carry-over, so that each calendar day in the configured schedule timezone begins from the configured schedule.
21. As a plugin operator, I want authentication failures to avoid minute-by-minute retries, so that unchanged invalid credentials do not spam upstream APIs.
22. As a plugin operator, I want model and business failures to avoid minute-by-minute retries, so that requests that require model, credential, quota, or configuration changes do not repeat uselessly.
23. As a plugin operator, I want automatic model candidates to continue being tried immediately within one Auto-Ping attempt, so that candidate fallback remains distinct from a Milestone Retry.
24. As a plugin operator, I want a new milestone to supersede pending retries from the old milestone, so that only the newest schedule intent remains active.
25. As a plugin operator, I want at most one request per Codex Credential in flight, so that exact scheduling never creates overlapping duplicate inference requests.
26. As a plugin operator, I want a new milestone to wait for an already-running request for the same credential and then run immediately, so that no-overlap is preserved even if the new milestone starts slightly late.
27. As a plugin operator, I want startup to catch up the latest elapsed milestone of the current calendar day in the configured schedule timezone regardless of how close the next milestone is, so that restart does not leave the current window inactive.
28. As a plugin operator, I want startup before the first milestone of the calendar day in the configured schedule timezone to wait for that first milestone, so that the plugin does not replay the prior day's final milestone.
29. As a plugin operator, I want multiple missed milestones to coalesce to the latest elapsed milestone, so that wake-up does not send several stale requests back-to-back.
30. As a plugin operator, I accept that startup at `09:59` can catch up `05:00` and then delay the `10:00` request until the catch-up request finishes, so that catch-up and no-overlap remain consistent.
31. As a plugin operator, I want restart during a recoverable cooldown to honor the persisted next retry time, so that restart cannot bypass rate limiting.
32. As a plugin operator, I want restart after an authentication, model, or business failure to wait for the next milestone, so that restart cannot bypass a terminal result for the current cycle.
33. As a plugin operator, I want schedule or timezone reconfiguration to cancel the old cycle and pending retries, so that only the new configuration remains authoritative.
34. As a plugin operator, I want reconfiguration to wait for canceled automatic work to exit before starting the replacement scheduler, so that scheduler contention cannot skip a due milestone.
35. As a plugin operator, I want a milestone that becomes due during reconfiguration handoff to be evaluated under the new configuration, so that the handoff does not lose current work.
36. As a plugin operator, I want a successful manual ping with `mark_cycle_processed: true` to satisfy the current milestone, so that the automatic scheduler avoids an intentional duplicate.
37. As a plugin operator, I want an unmarked manual ping not to satisfy a milestone, so that ordinary manual diagnostics do not alter automatic scheduling.
38. As a plugin operator, I want a Processed Milestone recorded only after a valid Codex success response is persisted, so that attempted or failed requests are never mistaken for success.
39. As a plugin operator, I want obsolete `scan_interval` and `activation_delay` keys to fail validation, so that removed behavior cannot appear to remain supported.
40. As a plugin operator, I want unknown plugin configuration fields to fail validation, so that misspelled settings cannot be silently ignored.
41. As a plugin operator, I want status and diagnostics to expose the active milestone, next retry time, last failure class, and success-only processed milestone, so that I can distinguish pending, terminal, and successful cycles.
42. As a plugin operator, I want tokens and raw credential data to remain absent from state, status, diagnostics, and logs, so that scheduling reliability does not weaken secret handling.
43. As a plugin operator, I want a clear error for an unsupported state schema, so that a destructive release cutover fails visibly instead of guessing at old state semantics.
44. As a host process, I want shutdown and reconfiguration waits to be bounded by their caller context, so that lifecycle operations cannot hang indefinitely.

## Implementation Decisions

- **Scheduling model**
  - Continue using precise timers for Schedule Milestones and Milestone Retries; do not add a permanent one-minute scanner.
  - Continue resolving wall-clock milestones with the existing configured `time.Location` behavior; this specification does not redefine DST gap or fold semantics.
  - When several milestones have elapsed while the runtime could not schedule work, only the latest valid elapsed milestone of the current calendar day in the configured schedule timezone is considered.

- **Eligibility**
  - Eligible Credentials exclude explicit `exclude_credentials`, host-disabled credentials, revoked credentials, and all credentials when Auto-Ping is globally disabled.
  - Temporary unavailability, cooldown, exhausted historical retries, and prior authentication failure do not remove eligibility at a later Schedule Milestone.
  - Remove authentication-version blocking as a scheduling gate. Authentication failure remains observable as the last result but never suppresses the next milestone.

- **Milestone Attempt Cycle**
  - Each credential gets an independent cycle keyed by the Schedule Milestone.
  - The cycle ends at whichever occurs first: successful persisted Auto-Ping, the next Schedule Milestone, a calendar-date change in the configured schedule timezone, or schedule/timezone reconfiguration.
  - Recoverable failures have no fixed maximum number of attempts.
  - Recoverable failures are temporary credential discovery/read failures, transport failures, request timeouts, stream failures, HTTP 429, and HTTP 5xx.
  - Authentication failures, exhausted model-candidate failures, other HTTP 4xx/business rejections, empty or invalid success payloads, and explicit Codex business errors are terminal for the current cycle.
  - Automatic model candidate fallback remains immediate within one attempt and does not consume a scheduled retry interval between candidates.

- **Retry timing**
  - Change the Plugin Manifest default `retry_cooldown` from `2m` to `1m`; retain the field and positive-duration validation.
  - Schedule the next retry from completion of the failed attempt.
  - For HTTP 429 or any response carrying `Retry-After`, parse the standard delay-seconds and HTTP-date forms. Use the later of the parsed time and `completion + retry_cooldown`.
  - Invalid or past `Retry-After` values do not fail the request classifier; they fall back to `retry_cooldown`.
  - Do not schedule a retry at or after the earliest cycle boundary.

- **Concurrency and reconfiguration**
  - Preserve bounded cross-credential concurrency through `max_concurrency`.
  - Preserve per-credential serialization across automatic milestone attempts, retries, and manual pings.
  - A newer milestone clears pending retry work from the older milestone. If an older request is already running for the same credential, let cancellation propagate and wait for that request to exit before starting the newer request; never overlap them.
  - Reconfiguration immediately cancels the old automatic scheduler context, including an automatic request already running under that context.
  - The replacement scheduler must not start until the canceled scanner and its automatic dispatch have fully exited. Do not rely on `scanRunning` contention as a handoff mechanism.
  - The caller context bounds the drain. If the drain cannot complete before cancellation/deadline, return a configuration failure and leave the automatic scheduler stopped rather than starting overlapping schedulers.
  - After a successful handoff, evaluate the latest valid milestone under the new schedule and timezone. This catches a milestone that became due during the drain without replaying obsolete schedule work.

- **Startup, wake-up, and restart**
  - Remove the prior one-hour catch-up threshold.
  - At startup or enablement, catch up the latest valid elapsed milestone of the current calendar day in the configured schedule timezone. Before that configured-timezone day's first valid milestone, wait.
  - If startup occurs immediately before the next milestone, catch-up still runs. The next milestone supersedes pending retries and waits for any same-credential catch-up request already in flight; it may therefore start after its wall-clock boundary.
  - Restart must resume a recoverable cycle at its persisted `next_retry_at`, or immediately if that time is overdue and the cycle is still active.
  - Restart must not repeat a terminal authentication, model, or business failure for the same milestone.
  - Pending work from a previous calendar date in the configured schedule timezone never carries into the new date.

- **Configuration validation**
  - Decode plugin instance YAML with strict known-field validation.
  - Reject `scan_interval`, `activation_delay`, and every other unknown field as `invalid_config`.
  - Do not add compatibility aliases or silently discard obsolete keys.

- **Persistent state**
  - Keep each credential's processed milestone marker success-only; do not rename it to an evaluated or attempted marker.
  - Persist current-cycle metadata separately: the attempted milestone key, failure class/status, retry count for diagnostics, and `next_retry_at` when a recoverable retry is pending.
  - Retry count is informational and never caps attempts.
  - Clear current-cycle retry metadata on success, cycle supersession, configured-timezone date change, or reconfiguration.
  - Remove obsolete authentication-block version state because it no longer gates later milestones.
  - A batch-level last-milestone value is diagnostics only and must never gate per-credential dispatch.
  - Perform a destructive state schema version cutover. Increment the schema version and reject prior state versions; do not migrate, alias, or compatibility-decode old state.
  - Unsupported state must produce a clear configuration/startup error. The operator must remove the old state file or select a new `state_path` before enabling the new release.

- **Processed state and manual pings**
  - Record a Processed Milestone only after a valid Codex success response and successful atomic state persistence.
  - A successful manual ping satisfies the latest elapsed milestone only when `mark_cycle_processed` is explicitly true.
  - A crash after upstream success but before state persistence can still cause a duplicate; the existing success-first persistence trade-off remains accepted.

- **Status and diagnostics**
  - Expose enough per-credential state to distinguish no attempt, in flight, recoverable cooldown, terminal current-cycle failure, and processed success.
  - Continue sanitizing error text and never expose tokens, authorization headers, cookies, or raw credential documents.

## Testing Decisions

- Tests must assert externally observable behavior: host requests sent, credential IDs selected, request timing/order, lifecycle responses, persisted success/retry state after reload, and management status. They must not assert private timer channels, goroutine counts, lock ownership, or source text.
- The primary seam is the plugin lifecycle boundary: drive registration and reconfiguration through the runtime's lifecycle handler, use the existing fake Host to control credentials and upstream responses, and use Go's synthetic-time support to exercise the real scheduler, workers, cancellation, and retry loop.
- Retain the existing pure schedule-resolver seam for ordinary milestone calculations, configured-timezone date rollover, and latest-elapsed-milestone selection; do not add DST-specific acceptance tests in this effort.
- Exercise persistent-state cutover through lifecycle configuration with temporary state files, proving that the new schema reloads and the old schema is explicitly rejected.
- Extend the existing scheduled milestone, catch-up, retry, reconfiguration, manual-ping overlap, configuration, state persistence, and management status tests rather than creating a second test harness.
- Required behavioral scenarios:
  - `05:00` success followed by an independent `10:00` request.
  - More than three recoverable failures followed by success before the cycle boundary.
  - A recoverable retry superseded by the next milestone.
  - Final-milestone retries stopping when the configured schedule timezone's calendar date changes, with a fresh first-milestone attempt the next day.
  - HTTP 429 with shorter, longer, invalid, and HTTP-date `Retry-After` values.
  - 401/403, model, and business failures producing no minute retry but allowing the next milestone.
  - Restart before `next_retry_at`, after `next_retry_at`, and after a terminal current-cycle failure.
  - Startup before the first milestone, between milestones, at `09:59`, and after several missed milestones.
  - Reconfiguration while a timer waits, while a retry is pending, while an automatic request is in flight, and while a new milestone becomes due during drain.
  - The replacement loop never loses the due milestone because an old loop still owns the dispatch guard.
  - Same-credential automatic/manual and old/new milestone work never overlaps.
  - Explicit opt-out, exclusion, disabled, and revoked states remain respected.
  - Temporary unavailable state and prior authentication failure do not suppress a later milestone.
  - `scan_interval`, `activation_delay`, and an arbitrary misspelled field are rejected through the lifecycle configuration boundary.
  - Processed Milestone state is written only after success; attempted and terminal failures remain unprocessed.
  - A marked successful manual ping satisfies the milestone; an unmarked manual ping does not.
  - State schema version 1 is rejected and the new schema round-trips all required current-cycle metadata.

## Out of Scope

- Guaranteeing that Codex changes or resets quota; the plugin guarantees request attempts and records valid upstream success, not upstream quota policy.
- Pre-flight quota polling or parsing dynamic upstream `reset_at` values.
- Cron expressions or day-of-week/month scheduling.
- Replaying every missed milestone after downtime or suspension.
- Overlapping requests for the same Codex Credential to achieve exact wall-clock start time.
- Minute-by-minute retries for authentication, model, or business failures.
- Compatibility aliases for removed configuration keys.
- Migration or compatibility decoding for the prior state schema.
- Management Center UI changes.
- Changes to transport selection or scheduler-boost credential confirmation beyond the retry classification described here.
- Defining new behavior for daylight-saving gap or fold transitions; existing Go and configured-timezone behavior remains unchanged.

## Further Notes

- This specification refines ADR-0010's catch-up behavior and is governed by ADR-0011 and the project domain glossary.
- The current release already preserves milestones after many slow-batch and retry cases, but the reconfiguration handoff still requires an explicit cancel-and-drain boundary before the replacement loop starts.
- DST gap/fold behavior is intentionally unchanged and outside this specification; no new product guarantee is inferred from Go's ambiguous transition handling.
- The current configuration parser uses non-strict YAML decoding, so obsolete and misspelled keys are presently ignored. Implementation must switch the plugin instance parser to strict known-field decoding.
- The state schema cutover is intentionally destructive. Release notes and operator documentation must state that existing state must be removed or a new `state_path` selected before enabling the release.
