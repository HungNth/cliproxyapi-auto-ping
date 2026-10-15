# Google Antigravity Auto-Ping Research

## Scope

Primary-source facts needed to add Google Antigravity targets without changing existing Codex behavior. Product decisions confirmed during the design interview are labeled separately from observed protocol facts.

## Verified protocol facts

### Provider and credential identity

- CLIProxyAPI identifies the provider as `antigravity`.
- Antigravity auth storage includes Google OAuth access/refresh material and may include `project_id`; the plugin must not persist or expose those secrets.
- Sources:
  - [CLIProxyAPI Antigravity auth SDK](https://github.com/router-for-me/CLIProxyAPI/blob/main/sdk/auth/antigravity.go)
  - [CLIProxyAPI plugin auth-file contract](https://help.router-for.me/plugin/host-callbacks.html#credential-file-callbacks)

### Requested model IDs

- `gemini-3.8-flash-high` and `claude-sonnet-5-5-high` are current Antigravity model IDs in the CLIProxyAPI model catalog.
- Model availability is credential-specific; the host may reject a target that the exact credential cannot serve.
- Source: [CLIProxyAPI model catalog](https://github.com/router-for-me/CLIProxyAPI/blob/main/internal/registry/models/models.json)

### Quota summary contract

- Quota summary uses `POST https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary` with `{"project":"<project_id>"}`.
- The observed response is `groups[].buckets[]`, not `quotaInfo.buckets[]`:
  - Gemini group: `gemini-5h` and `gemini-weekly`.
  - Claude/GPT group: `3p-5h` and `3p-weekly`.
  - Each bucket may include `window`, `remainingFraction`, and RFC3339 `resetTime`.
- The selected feature scope uses only the two `5h` buckets.
- Sources:
  - [Management Center endpoint constants](https://github.com/router-for-me/Cli-Proxy-API-Management-Center/blob/main/src/utils/quota/constants.ts)
  - [Management Center request construction](https://github.com/router-for-me/Cli-Proxy-API-Management-Center/blob/main/src/features/quota/providers/antigravity/data.ts)
  - [Management Center group/bucket parser](https://github.com/router-for-me/Cli-Proxy-API-Management-Center/blob/main/src/utils/quota/builders.ts)
  - [CLIProxyAPI quota-summary issue with observed payloads](https://github.com/router-for-me/CLIProxyAPI/issues/3876)

### Project discovery is separate from quota summary

- `POST https://daily-cloudcode-pa.googleapis.com/v1internal:loadCodeAssist` is a separate project/subscription discovery call.
- It is not interchangeable with `retrieveUserQuotaSummary` and its payload must not be parsed as the selected quota-summary contract.
- Sources:
  - [Management Center endpoint constants](https://github.com/router-for-me/Cli-Proxy-API-Management-Center/blob/main/src/utils/quota/constants.ts)
  - [CLIProxyAPI Antigravity executor request/project logic](https://github.com/router-for-me/CLIProxyAPI/tree/main/internal/runtime/executor)

### Provider-aware activation

- CLIProxyAPI's Antigravity executor owns OAuth refresh, project resolution, request translation, signatures, cooldown handling, and Google internal request construction.
- Direct upstream activation would duplicate those host responsibilities and couple this plugin to the internal `generateContent` protocol.
- Sources:
  - [Antigravity execution path](https://github.com/router-for-me/CLIProxyAPI/blob/main/internal/runtime/executor/antigravity_executor_execute.go)
  - [Antigravity request construction](https://github.com/router-for-me/CLIProxyAPI/blob/main/internal/runtime/executor/antigravity_executor_request.go)
  - [Antigravity auth refresh](https://github.com/router-for-me/CLIProxyAPI/blob/main/internal/runtime/executor/antigravity_executor_auth.go)

### Exact credential pinning

- CLIProxyAPI `host.model.execute` supports `forced_provider` and exact `auth_id` pinning from v7.3.3 onward.
- The host tests fail closed for nonexistent or disabled pinned credentials instead of falling back to another credential.
- This plugin's current local `ModelExecuteRequest` omits both fields and must add them before Antigravity support can be correct.
- `scheduler.pick` nonce handling is not part of this exact-pinning contract and is not used to certify Antigravity nested execution.
- Sources:
  - [Host request fields on v7.3.3](https://github.com/router-for-me/CLIProxyAPI/blob/v7.3.3/sdk/pluginapi/types.go)
  - [Exact-pinning implementation commit](https://github.com/router-for-me/CLIProxyAPI/commit/748d5767310a2236603115345f307b9f27b366b0)
  - [Completed exact-pinning issue](https://github.com/router-for-me/CLIProxyAPI/issues/5815)

## Confirmed product decisions

1. One plugin exposes one lifecycle, global enablement setting, exclusion list, and management surface; Codex and Antigravity remain separate provider engines.
2. Every eligible Antigravity credential gets two independent targets:
   - `gemini-3.8-flash-high` for the Gemini five-hour bucket.
   - `claude-sonnet-5-5-high` for the Claude/GPT five-hour bucket.
3. Weekly Antigravity buckets are observed only as unrelated payload data and never schedule Auto-Ping.
4. An unanchored target bootstraps at the first applicable daily schedule milestone or catch-up. After anchoring, it runs only at `resetTime + antigravity_reset_margin`; the default margin is `30s`.
5. Schedule milestones restart terminal attempt cycles but do not force daily pings for healthy anchored Antigravity targets.
6. Activation requires CLIProxyAPI v7.3.3 or newer and uses `host.model.execute` with both `forced_provider: antigravity` and exact `auth_id`. Antigravity does not use direct activation, priority boost, or scheduler nonce confirmation.
7. After a successful pinned inference, the plugin re-reads the credential, resolves `project_id` directly or through `loadCodeAssist`, then calls `retrieveUserQuotaSummary`.
8. Failed or stale quota observation retries observation only; it never duplicates the successful inference and the plugin does not implement Google OAuth refresh.
9. Success means a valid inference response followed by the selected five-hour bucket reporting a future `resetTime`. It does not require `resetTime` to advance because of the ping or `remainingFraction` to decrease.
10. Authentication failure is credential-wide for the current attempt cycle; model, business, cooldown, and counters remain target-local. Terminal targets retry at the next schedule milestone.
11. Existing runtime fields are shared where their semantics match. Codex model and transport fields remain Codex-only; Antigravity adds only `antigravity_reset_margin` and uses fixed target model IDs.
12. State schema remains version 3. Existing Codex records remain unchanged; Antigravity records add optional nested Gemini/Claude five-hour target state.
13. Manual ping without a model runs both Antigravity targets; a supplied model runs one. Results are reported per target, allowing partial success without discarding the successful target's state.
14. The feature guarantees reset-aligned keepalive requests. It does not claim to increase, bypass, extend, or otherwise modify Antigravity quota.

## Remaining implementation risks, not open product decisions

- Host versions older than v7.3.3 do not satisfy the exact-pinning requirement and are unsupported for Antigravity Auto-Ping.
- Re-reading persisted auth after host execution may briefly observe stale token/project data; observation retry must not trigger another inference.
- Upstream group names, bucket IDs, and response shapes are private APIs and may change. Parsing should accept only the explicit Gemini and Claude/GPT five-hour candidates and reject ambiguity.
- A target model may exist in the global catalog but be unavailable to one credential; that target fails independently while the sibling target remains eligible.
