# Upstream reset observation and dynamic rolling schedule

Status: accepted (supersedes ADR-0010 and the milestone-timing portions of ADR-0011; retains ADR-0011 failure classification)

## Context

The fixed wall-clock milestone schedule (`05:00, 10:00, 15:00, 20:00`) introduced in ADR-0010 assumed five-hour rolling quota windows reset exactly at wall-clock boundaries. In production, requests dispatched at `15:00:00.015` succeeded upstream with HTTP 200, yet the subsequent 5-hour window remained unactivated (`in 5h 0m 100%`, sliding). The leading hypothesis is that the upstream window ends relative to server-side processing of the prior ping, though server clock skew and local client timing differences remain unverified inferences.

Furthermore, static buffers cannot ensure synchronization across variable upstream network delays and serial credential execution. Existing repo documentation also carried stale RFC3339 examples, whereas production observations confirm numeric integer Unix seconds.

## Decision

1. **Upstream Reset Observation via `/wham/usage`**:
   - The plugin retrieves the current quota window state from upstream `GET https://chatgpt.com/backend-api/wham/usage` for eligible Codex credentials.
   - The parser contract explicitly targets the observed production wire contract: integer Unix seconds (`"reset_at": 1790084449`) using `json.Number` decoding into `time.Unix(seconds, 0).UTC()`.
   - Defensive millisecond and RFC3339 timestamp formats remain fixture-tested. Candidate selection is limited to `rate_limits_by_limit_id.codex`, `rate_limit`, `rate_limits`, the root mapping, and direct child windows; unrelated recursive branches are ignored.
   - Matching requires `limit_window_seconds == 18000`. Floating timestamps, trailing JSON, and ambiguous multiple matches are rejected instead of guessed.

2. **Dynamic Rolling Trigger (`reset_at + 30s`)**:
   - For each credential, the target auto-ping trigger timestamp is computed as `target = reset_at.Add(30 * time.Second)`.
   - The initial anchor time is derived from the first normalized entry of `schedule` (`05:00` by default).
   - The 30-second offset is an operational risk margin, not a formal guarantee, intended to dispatch after the observed boundary.
   - Following an inference ping, the plugin re-observes `/wham/usage`. Because `/wham/usage` may be cached or eventually consistent, the next cycle is anchored only when a strictly advanced `reset_at > previous_reset_at` is confirmed (or bounded retry on stale observations).

3. **Per-Credential Independence and Persistence**:
   - Each credential maintains independent `observed_reset_at`, `target_trigger_at`, and `last_processed_reset_at` fields in `state.json`.
   - Restarts restore per-credential schedules directly from persistent state without duplicate pings.
   - Recoverable errors on usage observation or inference ping retry with backoff bounded by the configured `retry_cooldown`.
   - Authentication failures (HTTP 401/403) terminate the current cycle per ADR-0011 without permanent suspension and start fresh at the next anchor milestone.

## Consequences

- Reintroduces a dependency on the private `/wham/usage` endpoint.
- Eliminates fixed wall-clock alignment; schedules naturally roll and drift over time.
- Mitigates the boundary race condition under the working hypothesis of server-side reset boundaries, subject to upstream eventual consistency.
