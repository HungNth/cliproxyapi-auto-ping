# 02: Surface upstream error details and candidate model fallback on HTTP 400

**What to build:** Enhance Codex response evaluation to parse and surface upstream error details (such as `detail` or `error.message`) in error messages, and treat HTTP 400/404 as candidate-incompatible (`FailureModel`) so `auto` mode iterates to subsequent candidates before entering cooldown.

**Blocked by:** 01: Purge max_output_tokens from manifest, config, and activation payloads

**Status:** resolved

**Parent specification:** Fix Codex Auto-Ping HTTP 400 Bad Request and Surface Upstream Errors.

**Demo:** `go test ./internal/autoping -run TestDirectActivation` passes, verifying error messages include upstream `detail` and candidate fallback advances past HTTP 400.

- [x] `evaluateCodexResponse` extracts `detail` or `error.message` from non-2xx JSON response bodies, formatting as `Codex returned HTTP <status>: <detail>`.
- [x] `evaluateCodexResponse` classifies HTTP 400 and 404 as `FailureModel` when in `auto` model selection mode.
- [x] Unit tests in `activation_test.go` assert HTTP 400 with `{"detail":"..."}` surfaces the detail and falls back to the next candidate model.
