# Fix Codex Auto-Ping HTTP 400 Bad Request and Surface Upstream Errors

Status: ready-for-agent

## Problem Statement

When the Auto-Ping plugin attempts to ping Codex accounts upon reset boundary arrival, upstream Codex Responses API (`https://chatgpt.com/backend-api/codex/responses`) rejects the request with `HTTP 400 Bad Request` (`{"detail":"Unsupported parameter: max_output_tokens"}`). Because the plugin sent `"max_output_tokens": 1` in its inference payload, all auto-ping attempts fail continuously.

Furthermore, when this failure occurs:
1. The error message stored in `state.json` and logs is collapsed to `"Codex returned HTTP 400"`, discarding the actual upstream error message (`detail`).
2. The failure is classified as a generic business failure rather than a candidate model/parameter failure, preventing the runtime from trying subsequent configured candidate models (e.g. `gpt-5.6-luna`) and forcing the credential directly into a 15-minute cooldown.
3. Credentials remain stuck in continuous failures (e.g. 29 attempts, 29 failures) without ever successfully activating new rolling five-hour quota windows.

## Solution

1. Completely eliminate `max_output_tokens` across the entire plugin: manifest definition, config parser, runtime model execute payloads, and documentation. Upstream Codex output tokens are already kept to a minimum (~5 output tokens) via instructions prompting for a single token.
2. Update Codex response evaluation to parse upstream error payloads (such as `detail` or `error.message`), appending the detail to the status code in error reports so diagnostics in `state.json` and logs are immediately actionable.
3. Treat HTTP 400 / 404 client errors as candidate-level failures (`FailureModel`) when running in `auto` model selection mode, allowing the runtime to fall back to subsequent candidate models before entering retry cooldown.

## User Stories

1. As a CLIProxyAPI operator, I want auto-ping inference requests to use only valid upstream parameters, so that Codex accepts the ping request and starts a new five-hour quota window immediately after reset.
2. As a CLIProxyAPI operator, I want upstream error messages (like parameter rejections or quota notices) visible in `state.json` and logs, so that I can immediately understand why an attempt failed without packet inspection.
3. As a CLIProxyAPI operator, when `model: auto` is configured and one candidate model returns an HTTP 400/404 client error, I want the plugin to try the next model candidate, so that a retired or unsupported model does not prevent auto-pinging.
4. As a plugin maintainer, I want no dead configuration parameters (`max_output_tokens`) in the manifest or schema, so that the plugin interface remains lean and accurate.
5. As a plugin maintainer, I want regression and unit tests to verify that direct and scheduler requests omit unsupported parameters and properly format upstream error messages.

## Implementation Decisions

- Remove `max_output_tokens` field from the plugin manifest defaults and config field list.
- Remove `MaxOutputTokens` from runtime configuration structures, validation logic, and getters.
- Omit `max_output_tokens` from both direct HTTP request bodies and scheduler boost request bodies.
- Enhance response evaluation logic:
  - If status code is an error (HTTP non-2xx), attempt to decode JSON containing `detail` or `error.message`.
  - Format error messages as `Codex returned HTTP <status>: <detail>` when available, falling back to `Codex returned HTTP <status>`.
  - Classify HTTP 400 and 404 responses as candidate model failures so that candidate progression in `auto` mode is attempted.
- Update ADR 0009 to record this architectural rationale.
- No backward compatibility or deprecated aliases for `max_output_tokens`.

## Testing Decisions

- Unit tests for configuration and manifest parsing:
  - Verify manifest validation passes without `max_output_tokens`.
  - Verify embedded manifest reflects the updated schema.
  - Verify management registration metadata omits `max_output_tokens`.
- Unit tests for direct activation and response evaluation:
  - Verify request payload sent to the Codex stream bridge does not contain `max_output_tokens`.
  - Verify HTTP 400 with `{"detail":"Unsupported parameter: ..."}` formats the error message with the detail string and advances to the next model candidate in `auto` mode.
  - Verify successful response continues to trigger successful window activation.
- Integration tests:
  - Verify multi-account scanning and activation survive restarts and properly mark boundaries upon success.

## Out of Scope

- Changing upstream Codex API behavior or specifications.
- Preserving deprecated `max_output_tokens` in configuration schemas.

## Further Notes

- Verified against live upstream Codex API: omitting `max_output_tokens` succeeds with HTTP 200 OK and generates only 21 total tokens (5 output tokens), flawlessly triggering a new 5-hour quota window.
