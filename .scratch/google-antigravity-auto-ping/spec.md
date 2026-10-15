# Google Antigravity Reset-Aligned Auto-Ping Specification

Status: ready-for-agent
Parent ADRs: ADR-0013, ADR-0014, ADR-0015

## Problem Statement

The Auto-Ping plugin currently serves only Codex Credentials. Operators with Google Antigravity Credentials cannot use the same plugin to send reset-aligned keepalive requests for the Gemini and Claude/GPT five-hour Quota Windows, cannot invoke those targets through the existing manual management route, and cannot inspect independent scheduling and failure state for those targets.

Antigravity differs materially from Codex. One Antigravity Credential exposes separate Gemini and Claude/GPT five-hour and weekly quota buckets, uses Google OAuth and provider-specific request translation, and requires exact credential routing to avoid sending a request through another Antigravity or native Claude credential. Treating one credential as one schedule would merge unrelated reset times, retries, counters, and outcomes. Reimplementing the Google executor inside this plugin would duplicate host-owned OAuth refresh, project resolution, translation, signature, and cooldown behavior.

The feature must add Antigravity behavior without changing existing Codex eligibility, request payloads, dynamic scheduling, model fallback, transports, persisted fields, management responses, or the prohibition on Codex `max_output_tokens`.

## Solution

Extend the existing plugin with an isolated Antigravity provider engine under the same lifecycle, global enablement setting, exclusion list, shared runtime limits, and management surface.

For every Eligible Antigravity Credential, maintain two independent Auto-Ping Targets:

- `gemini-3.8-flash-high` for the Gemini five-hour Quota Window;
- `claude-sonnet-5-5-high` for the Claude/GPT five-hour Quota Window.

An unanchored target bootstraps at the first applicable Schedule Milestone or Schedule Catch-Up. Bootstrap sends the minimal inference through CLIProxyAPI's provider-aware model execution path, pinned to the exact credential and forced to the Antigravity provider. After a successful inference, the plugin re-reads the credential, obtains the selected five-hour reset from `retrieveUserQuotaSummary`, and schedules the next request at `resetTime + antigravity_reset_margin`.

Once anchored, an Antigravity target follows only its own reset timestamp. Daily Schedule Milestones do not force extra pings for healthy anchored targets; they only bootstrap missing target state or start a fresh Attempt Cycle after terminal failure.

A successful inference followed by a stale, missing, ambiguous, or non-future quota observation enters an observe-only stabilizing state. The plugin retries quota observation without resending inference until a future reset is observed or a credential-wide authentication failure is established.

The feature is a reset-aligned keepalive. It does not claim to increase, bypass, extend, or otherwise modify Antigravity quota.

## User Stories

1. As a CLIProxyAPI operator, I want the existing Auto-Ping plugin to support Antigravity Credentials, so that I do not need a second plugin for reset-aligned keepalive requests.
2. As a CLIProxyAPI operator, I want Codex and Antigravity to share one plugin enablement switch, so that enabling Auto-Ping activates all supported providers consistently.
3. As a CLIProxyAPI operator, I want an explicit global Auto-Ping opt-out to continue disabling every supported provider, so that my existing operational control remains authoritative.
4. As a CLIProxyAPI operator, I want `exclude_credentials` to apply to Antigravity Credentials, so that I can suppress selected credentials without learning a second exclusion mechanism.
5. As a CLIProxyAPI operator, I want disabled and revoked Antigravity Credentials skipped, so that Auto-Ping does not use credentials the host has taken out of service.
6. As a CLIProxyAPI operator, I want temporary host unavailability not to permanently remove an Antigravity Credential from eligibility, so that later Attempt Cycles can recover automatically.
7. As an Antigravity user, I want Gemini and Claude/GPT quota behavior tracked independently, so that one Model Family cannot advance or block the other.
8. As an Antigravity user, I want `gemini-3.8-flash-high` targeted for the Gemini five-hour Quota Window, so that the keepalive exercises the requested Gemini model.
9. As an Antigravity user, I want `claude-sonnet-5-5-high` targeted for the Claude/GPT five-hour Quota Window, so that the keepalive exercises the requested Claude model.
10. As an Antigravity user, I want weekly quota buckets ignored by scheduling, so that the plugin does not create extra requests at weekly resets.
11. As a CLIProxyAPI operator, I want a new Antigravity target to wait for the configured daily anchor, so that first activation follows the plugin's established schedule discipline.
12. As a CLIProxyAPI operator, I want startup after the daily anchor to perform Schedule Catch-Up, so that an unanchored target does not wait until the next day.
13. As a CLIProxyAPI operator, I want healthy anchored Antigravity targets to run only after their own five-hour reset, so that daily milestones do not create redundant inference requests.
14. As a CLIProxyAPI operator, I want terminal Antigravity targets to become eligible again at the next Schedule Milestone, so that authentication, model, or business failures do not disable them forever.
15. As a CLIProxyAPI operator, I want the reset margin configurable independently from Codex, so that Antigravity boundary timing can be calibrated without changing Codex behavior.
16. As a CLIProxyAPI operator, I want the Antigravity reset margin to default to 30 seconds, so that requests do not fire exactly on the observed upstream boundary.
17. As a CLIProxyAPI operator, I want a changed Antigravity reset margin to apply after reconfiguration without overlapping schedulers, so that timing updates are safe and predictable.
18. As a CLIProxyAPI operator, I want Antigravity inference routed through the host's native provider executor, so that OAuth refresh and protocol translation remain host responsibilities.
19. As a CLIProxyAPI operator, I want every Antigravity inference pinned to the exact credential, so that a keepalive cannot consume another credential's quota.
20. As a CLIProxyAPI operator, I want Antigravity execution forced to the Antigravity provider, so that `claude-sonnet-5-5-high` cannot route through a native Claude credential.
21. As a CLIProxyAPI operator, I want invalid, disabled, unavailable, or removed pinned credentials to fail closed, so that the host never silently falls back to another credential.
22. As a plugin maintainer, I want CLIProxyAPI v7.3.3 or newer to be an explicit prerequisite, so that exact `auth_id` and `forced_provider` semantics are part of the supported host contract.
23. As a plugin maintainer, I want no priority mutation or scheduler-boost fallback for Antigravity, so that the plugin does not rewrite credential state to achieve exact routing.
24. As a plugin maintainer, I want no direct Google activation implementation, so that the plugin does not copy OAuth refresh, project discovery, signatures, cooldowns, or internal request envelopes.
25. As a CLIProxyAPI operator, I want quota observation to use `retrieveUserQuotaSummary`, so that scheduling follows the observed Antigravity group-and-bucket contract.
26. As a CLIProxyAPI operator, I want missing project metadata resolved through `loadCodeAssist`, so that credentials without a stored project can still establish quota state.
27. As a plugin maintainer, I want `loadCodeAssist` and `retrieveUserQuotaSummary` treated as separate contracts, so that project discovery payloads cannot be misparsed as quota summaries.
28. As a plugin maintainer, I want quota parsing limited to explicit Gemini and Claude/GPT five-hour buckets, so that unrelated recursive fields or weekly buckets cannot be selected accidentally.
29. As a plugin maintainer, I want duplicate or ambiguous matching five-hour buckets rejected, so that scheduling never depends on map order or guessing.
30. As a CLIProxyAPI operator, I want an inference success followed by a future selected reset to establish the next schedule, so that the plugin has an observable scheduling anchor.
31. As a CLIProxyAPI operator, I want a stale reset after successful inference to retry observation only, so that eventual consistency cannot cause a duplicate inference.
32. As a CLIProxyAPI operator, I want a missing selected bucket after successful inference to retry observation only, so that a temporary quota API inconsistency cannot cause a duplicate inference.
33. As a CLIProxyAPI operator, I want a non-future reset after successful inference to remain stabilizing, so that the scheduler cannot immediately resend the same target.
34. As a CLIProxyAPI operator, I want stabilizing state persisted across restart, so that a process restart cannot turn observation retry into another inference.
35. As a CLIProxyAPI operator, I want recoverable Antigravity failures to honor `Retry-After` or the configured retry cooldown, so that retries respect upstream backpressure.
36. As a CLIProxyAPI operator, I want model and business failures isolated to one Auto-Ping Target, so that Gemini can continue when Claude fails and vice versa.
37. As a CLIProxyAPI operator, I want credential authentication failure applied to both Antigravity targets, so that the same invalid authorization is not retried twice.
38. As a CLIProxyAPI operator, I want a credential authentication failure to skip queued sibling work immediately, so that no post-revocation request is sent for the second target.
39. As a CLIProxyAPI operator, I want requests for one credential serialized, so that Gemini and Claude cannot race token refresh, target state, or quota observation.
40. As a CLIProxyAPI operator, I want `max_concurrency` to remain a global limit across Codex and Antigravity credential jobs, so that adding a provider does not bypass my configured concurrency bound.
41. As a CLIProxyAPI operator, I want existing Codex Credentials to keep their current state and schedules after upgrade, so that adding Antigravity does not force a state reset.
42. As a CLIProxyAPI operator, I want Antigravity target state nested beneath its credential, so that both targets remain visibly associated with the same identity.
43. As a CLIProxyAPI operator, I want each Antigravity target to expose its own reset, trigger, retry, model, failure, and counters, so that one target's status cannot hide the other.
44. As a CLIProxyAPI operator, I want state schema version 3 to remain loadable, so that current Codex state requires no migration or deletion.
45. As an API consumer, I want existing Codex status and manual-ping responses unchanged, so that current integrations continue to parse them.
46. As an API consumer, I want Antigravity status to add nested target data without exposing tokens or raw credential JSON, so that diagnostics remain safe.
47. As an API consumer, I want a manual Antigravity ping without a model to run both targets, so that I can verify the credential with one operation.
48. As an API consumer, I want a manual Antigravity ping with a model to run only that target, so that I can diagnose Gemini or Claude independently.
49. As an API consumer, I want manual results reported per target, so that partial success is visible and successful target state is not discarded.
50. As an API consumer, I want all-success manual Antigravity requests to return HTTP 200, so that ordinary success remains simple.
51. As an API consumer, I want mixed manual Antigravity results to return HTTP 207, so that automation cannot mistake partial failure for complete success.
52. As an API consumer, I want all-failed manual Antigravity requests to return HTTP 502, so that complete upstream failure is visible at the HTTP layer.
53. As an API consumer, I want unmarked manual pings to leave automatic schedules unchanged, so that diagnostics do not silently advance an Attempt Cycle.
54. As an API consumer, I want marked manual pings to update only successful selected targets after quota observation, so that partial success preserves independent target state.
55. As a security-conscious operator, I want Google access tokens, refresh tokens, authorization headers, and raw credential documents excluded from state, logs, status, and diagnostics, so that Auto-Ping does not expand secret exposure.
56. As a plugin maintainer, I want Codex request construction to continue omitting `max_output_tokens` and `max_tokens`, so that Antigravity work cannot reintroduce the prior Codex HTTP 400 regression.
57. As a plugin maintainer, I want existing Codex direct transport and scheduler fallback semantics unchanged, so that Antigravity does not redefine Codex failure or routing behavior.
58. As a plugin maintainer, I want model and transport configuration fields to remain Codex-only, so that the fixed Antigravity targets cannot accidentally consume Codex configuration.
59. As a CLIProxyAPI operator, I want the shared prompt, timeout, retry, schedule, timezone, exclusions, and concurrency settings to apply consistently, so that common runtime behavior does not require duplicated configuration.
60. As a CLIProxyAPI operator, I want documentation to state that Antigravity Auto-Ping is a reset-aligned keepalive rather than a quota extension, so that its operational guarantee is not overstated.

## Implementation Decisions

1. **Provider boundary**
   - The plugin remains one artifact and one lifecycle surface.
   - Codex and Antigravity use isolated provider engines for eligibility dispatch, credential parsing, quota observation, activation, response evaluation, scheduling, and failure transitions.
   - Shared runtime infrastructure is limited to lifecycle ownership, configuration snapshotting, global concurrency, per-credential no-overlap, persistence, logging, and management routing.

2. **Eligibility and provider classification**
   - Antigravity Credentials are identified by a normalized provider or type value of `antigravity`.
   - The existing exclusion, disabled, revoked, and temporary-unavailability rules apply unchanged.
   - The global Auto-Ping setting controls both providers. No Antigravity-specific enable/disable field is added.
   - Existing installations with eligible Antigravity Credentials begin processing them after upgrade when global Auto-Ping is enabled; this is a deliberate consequence of the shared switch.

3. **Fixed Antigravity targets**
   - Each Eligible Antigravity Credential owns exactly two Auto-Ping Targets: Gemini five-hour using `gemini-3.8-flash-high`, and Claude/GPT five-hour using `claude-sonnet-5-5-high`.
   - Model IDs are fixed release behavior, not configurable candidates.
   - The targets have independent reset observations, trigger times, retry timing, terminal state, selected model, attempts, successes, and failures.
   - Weekly quota buckets never create Auto-Ping Targets and never gate the selected five-hour targets.

4. **Configuration contract**
   - Add `antigravity_reset_margin` as a duration field with default `30s`.
   - The field accepts non-negative durations and affects only Antigravity target triggers.
   - `schedule`, `timezone`, `retry_cooldown`, `request_timeout`, `prompt`, `max_concurrency`, `exclude_credentials`, and `state_path` remain shared.
   - `model`, `model_candidates`, `transport`, and `scheduler_boost_fallback` remain Codex-only. Their current parsing, defaults, status representation, and behavior do not change.
   - Manifest metadata and field descriptions become provider-neutral where the underlying setting is shared and remain Codex-specific where the setting is Codex-only.

5. **Host execution prerequisite and request contract**
   - Antigravity requires CLIProxyAPI v7.3.3 or newer, whose model-execution callback supports exact `auth_id` pinning and `forced_provider`.
   - Every Antigravity activation uses non-streaming host model execution with the exact credential ID and `forced_provider: antigravity`.
   - The host request uses the shared prompt through the OpenAI Responses entry/exit protocol already supported by the host translator, disables storage, and omits output-token limit fields.
   - The host's exact pin is the routing guarantee. Antigravity does not use scheduler priority boost, scheduler nonce confirmation, direct Google activation, or fallback to another credential.
   - Older hosts are unsupported for Antigravity Auto-Ping. No compatibility path silently degrades to unpinned execution.

6. **Quota observation contract**
   - After successful host execution, the plugin persists `stabilizing` before performing quota observation.
   - The plugin re-reads the credential document so quota observation uses the latest host-persisted access token and project metadata.
   - If project metadata remains absent, the plugin may call `loadCodeAssist` only to obtain the project identifier for the current observation. It does not parse that response as quota data and does not implement OAuth refresh.
   - Quota is fetched from `retrieveUserQuotaSummary` using the resolved project.
   - The accepted quota wire contract is `groups[].buckets[]`. Alternate `models[].quotaInfo`, flat bucket collections, and unrelated recursive branches are rejected rather than merged.
   - Gemini selection requires one unambiguous Gemini five-hour bucket; Claude selection requires one unambiguous Claude/GPT five-hour bucket. Explicit bucket identifiers and `window: 5h` are authoritative. Weekly buckets are ignored.
   - `resetTime` must be a valid RFC3339 timestamp. `remainingFraction` is diagnostic and does not determine success.
   - A selected reset must be in the future when observation is accepted for scheduling.

7. **Antigravity scheduling state machine**
   - An unanchored target waits until the first normalized Schedule Milestone. Startup after that milestone performs Schedule Catch-Up.
   - Bootstrap executes each target under the credential lock. Model/business failure on one target does not skip the sibling; credential authentication failure marks both terminal and skips any queued sibling target.
   - After all bootstrap executions that can safely run, one quota summary may anchor every successfully pinged target. A failed target is not advanced by its sibling's observation.
   - An anchored target becomes due at `observed reset + antigravity_reset_margin`.
   - A due target persists in-flight state before host execution. A successful inference persists `stabilizing` and clears its trigger before quota observation.
   - Stale, missing, ambiguous, malformed, or non-future selected quota observations keep the target in `stabilizing`. Observation retries after the applicable cooldown and never resend inference.
   - Restart restores `stabilizing` as observe-only work and restores waiting/retry/terminal target state without a bootstrap duplicate.
   - A future selected reset transitions the target to waiting and establishes the next trigger.
   - Healthy anchored Antigravity targets ignore daily Schedule Milestones. Schedule Milestones only bootstrap unanchored targets and reopen terminal Attempt Cycles.
   - Exactly-once inference across process crashes is not claimed because upstream offers no idempotency key. As with Codex, a crash after upstream success but before durable state replacement may produce a rare duplicate.

8. **Concurrency and locking**
   - The existing global `max_concurrency` limit applies across Codex and Antigravity credential jobs.
   - The existing per-credential no-overlap invariant applies across automatic and manual work and across both Antigravity targets.
   - Gemini and Claude target state is independent, but their inference and observation operations for one credential execute serially.
   - Reconfiguration cancels queued old-runtime work, waits for existing handoff semantics, and starts one replacement scheduler without overlap.

9. **Failure semantics**
   - Transport errors, timeouts, HTTP 429, Retry-After responses, upstream 5xx responses, temporary credential reads, and quota-observation availability failures are recoverable.
   - Retry-After takes precedence when supplied; otherwise shared `retry_cooldown` applies.
   - Model-not-supported and target-specific business failures are terminal only for that target's current Attempt Cycle.
   - Confirmed credential authentication failure is terminal for both targets' current Attempt Cycle. Because one credential is serialized, queued sibling work is skipped and no sibling request is allowed to begin after that transition.
   - A quota HTTP 401/403 immediately after successful host execution first re-reads credential material and retries observation without inference, allowing host persistence lag to settle. Authentication is marked credential-wide only after fresh material continues to fail.
   - Terminal targets start a fresh Attempt Cycle at the next Schedule Milestone or Schedule Catch-Up. They are not permanently disabled.

10. **Persistent state version 3**
    - State schema version remains `3`; version 1 and version 2 rejection remains unchanged.
    - Existing Codex credential JSON remains valid and unchanged.
    - Antigravity credential records add an optional `targets` object keyed by stable Gemini-five-hour and Claude-five-hour identities.
    - Each target persists status, reason, failure class, observed reset, next trigger, processed boundary when applicable, last attempt, last ping, sanitized error, next retry, retry count, selected model, transport, and counters.
    - Target state is authoritative for Antigravity scheduling. Credential-level mutable schedule fields are not used to collapse the two targets.
    - State writes retain the existing atomic replacement and secret-exclusion guarantees.

11. **Manual management API**
    - Existing Codex manual request and response behavior remains unchanged.
    - For an Antigravity Credential, an omitted model runs both fixed targets; a supplied model must match one fixed target and runs only that target.
    - Manual target execution uses the same exact host pinning, credential lock, response classification, and secret handling as automatic execution.
    - Without cycle marking, manual execution updates attempt/result counters but does not observe quota or alter the automatic schedule.
    - With cycle marking, every successful selected target enters stabilizing and quota is observed once after executable targets finish. Successful targets may advance independently; failed targets retain their own failure state.
    - Antigravity responses include per-target results with model, Model Family, success, transport, and sanitized error where applicable.
    - HTTP status is 200 when every selected target succeeds, 207 when selected targets have mixed outcomes, and 502 when every selected target fails. Existing not-found, ineligible, and in-flight conflict behavior remains unchanged.

12. **Status and diagnostics**
    - The existing credential list remains the top-level management representation.
    - Codex entries retain their current fields and JSON shape.
    - Antigravity entries expose nested Gemini and Claude/GPT target states, including reset, trigger, retry, failure, selected model, transport, and counters.
    - Diagnostics state the minimum supported host version, exact-pinning dependency, quota-summary endpoint, and reset-aligned keepalive guarantee.
    - Access tokens, refresh tokens, authorization headers, raw credential JSON, and unsanitized upstream errors are never persisted, logged, or returned.

13. **Reconfiguration and restart behavior**
    - Reconfiguration preserves all persisted Codex and Antigravity target state while atomically replacing the running scheduler.
    - Changing `antigravity_reset_margin` recalculates unprocessed Antigravity triggers from their observed reset without changing Codex triggers.
    - A recalculated overdue target becomes immediately due, subject to credential locking and global concurrency.
    - Changing schedule or timezone changes future bootstrap and terminal-recovery milestones without converting healthy anchored Antigravity targets into daily jobs.
    - Restart uses persisted target status as authority: waiting restores the trigger, cooldown restores the retry, stabilizing performs observation only, and terminal waits for the next applicable Schedule Milestone.

14. **Codex non-regression boundary**
    - Codex credential classification, auth parsing, `/wham/usage` parsing, `reset_at + 30s` behavior, mandatory Codex daily-anchor semantics, stabilization, state fields, model candidate fallback, direct transport, scheduler fallback, and response evaluation remain unchanged.
    - Codex direct and host-executed payloads continue to omit `max_output_tokens` and `max_tokens`.
    - Antigravity parsing and headers never enter Codex request paths, and Codex headers never enter Antigravity quota calls.
    - Existing Codex tests remain contract tests; they are updated only where provider-neutral manifest wording or additive management output requires it, never to pin incidental implementation details.

15. **Documentation and release contract**
    - Operator documentation explains the two fixed Antigravity targets, five-hour-only scope, shared global enablement, reset margin, minimum host version, status shape, manual partial results, and quota non-guarantee.
    - The changelog records Antigravity support and the CLIProxyAPI minimum-version prerequisite.
    - Research and ADRs remain the source for upstream protocol evidence and durable architectural rationale; operational response examples and parser fixtures belong in the specification and tests.

## Testing Decisions

- **Confirmed highest seam:** exercise the Runtime through its lifecycle/configuration boundary using the existing fake Host and synthetic clock. The same seam drives automatic scheduling, exact host model requests, outbound quota HTTP, persistence, restart, reconfiguration, manual management handling, and status output. No new production testing interface is introduced.
- Good permanent tests assert operator-visible behavior, durable state transitions, upstream request contracts, and management responses. They do not assert private helper calls, source text, copied fields, mock echoes, goroutine counts, or incidental ordering unless ordering is itself observable and required.
- The fake Host is extended only with behavior already present in the real host contract: mixed credential listings/documents, captured model execution requests, configurable model responses, quota/project HTTP responses, and errors. The synthetic clock continues to control Schedule Milestones, reset margins, cooldowns, and restart timing deterministically.
- Strict quota-parser cases are injected as fake HTTP responses through the Runtime seam. A separate parser test seam is not required unless implementation proves the runtime setup cannot express a malformed or ambiguous payload clearly.
- Existing dynamic-schedule tests provide prior art for pre-anchor sleep, Schedule Catch-Up, due dispatch, stabilization, retry, terminal recovery, staggered targets, and synthetic-time control.
- Existing manual-ping and management tests provide prior art for marked versus unmarked cycles, management HTTP status, status serialization, reconfiguration handoff, and secret-exposure guards.
- Existing state tests provide prior art for version enforcement, atomic round-trip persistence, restart restoration, and schema cutover behavior.
- Existing activation tests provide prior art for captured host requests, response classification, Retry-After behavior, model failures, fallback restrictions, and Codex unsupported-parameter regression protection.

The Runtime seam must cover at least these behaviors:

1. Mixed Codex and Antigravity credentials are classified into the correct provider engines without cross-routing.
2. Existing Codex automatic and manual scenarios produce unchanged requests, states, and responses.
3. Startup before the first milestone does not ping an unanchored Antigravity target early.
4. Startup after the first milestone performs Antigravity Schedule Catch-Up.
5. Bootstrap sends both fixed models through host execution with exact `auth_id` and `forced_provider: antigravity`.
6. Bootstrap model failure remains target-local and still allows the sibling target to execute.
7. Bootstrap authentication failure marks both targets terminal and skips queued sibling execution.
8. One quota summary anchors both successfully pinged bootstrap targets to their respective five-hour resets.
9. Gemini and Claude/GPT targets schedule at different reset times without advancing each other.
10. Weekly buckets are ignored even when they reset sooner than the five-hour bucket.
11. Duplicate matching Gemini or Claude/GPT five-hour buckets are rejected without guessing.
12. Alternate `models[].quotaInfo` or unrelated nested reset data is rejected as unsupported wire shape.
13. A successful inference followed by stale, missing, malformed, ambiguous, or past reset data persists stabilizing state and retries observation only.
14. Restart from stabilizing performs no inference and resumes quota observation.
15. A later future reset exits stabilizing and establishes the next trigger.
16. Retry-After overrides the shared retry cooldown; transport and 5xx failures use recoverable target cooldown.
17. Target-specific model/business failure does not block the sibling target.
18. Repeated quota authentication failure after fresh credential reread blocks both targets for the current Attempt Cycle.
19. The next Schedule Milestone reopens terminal target or credential state without forcing healthy anchored targets to ping.
20. Changing the Antigravity reset margin recalculates only Antigravity triggers and does not alter Codex targets.
21. Global concurrency remains bounded across providers, and automatic/manual work for one credential never overlaps.
22. State version 3 loads an existing Codex-only document unchanged and round-trips additive Antigravity target state.
23. Manual Antigravity ping with no model returns two results; an explicit fixed model returns one result.
24. Manual all-success, mixed, and all-failed outcomes return HTTP 200, 207, and 502 respectively and persist successful target state independently.
25. Unmarked manual Antigravity ping leaves automatic triggers unchanged; marked ping enters observe-only stabilization for successful targets.
26. Status and diagnostics expose target state and minimum-host requirements while excluding all credential secrets.
27. The canonical manifest exposes `antigravity_reset_margin` with default `30s`, and runtime configuration inherits or overrides it correctly.
28. Codex payloads still omit unsupported output-token fields after all Antigravity changes.

## Out of Scope

- Antigravity weekly-window scheduling or activation.
- Any claim that Antigravity Auto-Ping increases, bypasses, extends, or starts quota in the same way as Codex.
- Configurable Antigravity model IDs, model candidates, or model fallback.
- Provider-specific enablement switches, schedules, prompts, retry cooldowns, timeouts, concurrency limits, exclusion lists, or state paths.
- Direct Google `generateContent` activation from the plugin.
- Google OAuth refresh or refresh-token persistence in the plugin.
- Changes to CLIProxyAPI host APIs, auth providers, executors, translators, Management Center, or model catalogs.
- Compatibility with CLIProxyAPI versions older than v7.3.3.
- Scheduler priority mutation, scheduler-boost transport, or scheduler nonce certification for Antigravity.
- A new quota-provider capability or a user-facing quota dashboard.
- Parsing `loadCodeAssist` as quota summary data.
- Accepting undocumented alternative quota payload shapes beyond the explicit `groups[].buckets[]` contract.
- State schema version 4, migration of state version 3, or a second Antigravity state file.
- Changes to the independent `quota-activation` plugin.
- New retry caps, telemetry systems, alerting, or speculative provider abstractions.
- Live-account integration tests in the permanent suite.

## Further Notes

- The protocol research is recorded beside this specification and distinguishes `retrieveUserQuotaSummary` from `loadCodeAssist`.
- ADR-0013 defines the single-plugin/provider-isolated boundary, ADR-0014 defines exact host-pinned Antigravity execution, and ADR-0015 defines additive target state within version 3.
- The exact target model IDs are valid in the current CLIProxyAPI Antigravity catalog, but availability remains credential-specific. Unsupported targets fail independently and retry only in a later Attempt Cycle.
- The quota endpoints and payloads are private upstream contracts. Errors must remain diagnosable and sanitized, and unsupported shapes must fail visibly rather than silently selecting a different window.
- This specification is ready for ticket decomposition after user review and approval. No implementation or commit is authorized by this document.
