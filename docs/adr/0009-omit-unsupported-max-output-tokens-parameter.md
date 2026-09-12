# Omit unsupported max_output_tokens parameter and surface upstream errors

Status: accepted

The plugin previously passed `"max_output_tokens": 1` in direct HTTP and scheduler boost activation payloads sent to the Codex Responses API (`https://chatgpt.com/backend-api/codex/responses`). Upstream Codex backend rejects this parameter with `HTTP 400 Bad Request` (`{"detail":"Unsupported parameter: max_output_tokens"}`), causing auto-ping requests to fail continuously.

Output length is already strictly constrained by `instructions: "Reply with one token."`, which generates minimal tokens (~5 output tokens, 21 total tokens).

In accordance with project engineering principles (*"Do not preserve backward compatibility. Remove obsolete paths instead of adding compatibility layers, fallbacks, or migrations"*), `max_output_tokens` is removed completely from the plugin manifest, configuration schema, internal runtime, and request payloads.

Additionally, `evaluateCodexResponse` now extracts and formats upstream error details (such as `detail` or `error.message`) rather than discarding the body and emitting a generic status code message. When an HTTP 400/404 error occurs during activation in `auto` model mode, it is treated as candidate-incompatible (`FailureModel`) to allow trying subsequent configured candidates before entering retry cooldown.

## Considered Options

- Keeping `max_output_tokens` in configuration but omitting it from the request body: rejected per repository rules against dead configuration and speculative abstractions.
- Replacing `max_output_tokens` with `max_tokens`: rejected because upstream Codex Responses API also rejects `max_tokens`.
- Preserving status code only error messages: rejected because masking upstream error payloads complicates debugging and monitoring in persistent state files.
