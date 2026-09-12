# 01: Purge max_output_tokens from manifest, config, and activation payloads

**What to build:** Completely remove `max_output_tokens` from the plugin manifest, config parser, management registration, documentation, and the request payloads sent in `directActivate` and `schedulerActivate`.

**Blocked by:** None (can start immediately)

**Status:** resolved

**Parent specification:** Fix Codex Auto-Ping HTTP 400 Bad Request and Surface Upstream Errors.

**Demo:** `go test ./...` passes with manifest validation and config parsing succeeding without `max_output_tokens`.

- [x] `plugin.yaml` removes `max_output_tokens` config field and default.
- [x] `internal/autoping/config.go` removes `MaxOutputTokens` from `Config`, `rawConfig`, `configFromRaw`, and `fieldValue`.
- [x] `internal/autoping/manifest.go` removes `max_output_tokens` from `manifestFieldTypes`.
- [x] `internal/autoping/activation.go` omits `max_output_tokens` from `directActivate` request payload.
- [x] `internal/autoping/scheduler.go` omits `max_output_tokens` from `schedulerActivate` request payload.
- [x] `README.md` removes `max_output_tokens` from config example and options table.
- [x] Config and manifest tests updated to verify clean operation without `max_output_tokens`.
