# 01: Manifest and runtime config cutover

**What to build:** Replace the `auto_ping_enabled` configuration field with `auto_ping_disabled` (boolean, default `false`) across the plugin manifest, config parser, and management registration metadata, while keeping the internal runtime affirmative as `Config.AutoPingEnabled`.

**Blocked by:** none

**Status:** resolved

**Parent specification:** Negative Boolean auto_ping_disabled Configuration.

**Demo:** Run `go test ./internal/autoping/...` showing manifest validation, config parsing, and management registration passing with `auto_ping_disabled`.

- [x] `plugin.yaml` declares config field `auto_ping_disabled` of type `boolean` with default `false` and description `"Set to true to disable background Codex inference requests."`.
- [x] `internal/autoping/manifest.go` replaces `"auto_ping_enabled"` with `"auto_ping_disabled"` in `manifestFieldTypes`.
- [x] `internal/autoping/config.go` uses `AutoPingDisabled *bool` in `rawConfig`, sets `cfg.AutoPingEnabled = !*raw.AutoPingDisabled` in `configFromRaw`, and returns `!c.AutoPingEnabled` for `fieldValue("auto_ping_disabled")`.
- [x] `internal/autoping/management.go` outputs registration config field `auto_ping_disabled` with default `false` and hint `false`, and retains `statusConfig.Enabled = cfg.AutoPingEnabled`.
- [x] Core config and manifest unit tests assert omitted `auto_ping_disabled` yields `AutoPingEnabled: true`, while explicit `true` yields `AutoPingEnabled: false`.
