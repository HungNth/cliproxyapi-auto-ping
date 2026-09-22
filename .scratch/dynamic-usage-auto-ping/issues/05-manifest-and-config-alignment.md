# Issue 05: Plugin Manifest and Configuration Alignment

Status: ready-for-agent
Parent: `.scratch/dynamic-usage-auto-ping/spec.md`

## Required Behavior

Align `plugin.yaml`, embedded manifest (`manifest_embed.go`), and runtime configuration structures (`config.go`) with dynamic usage-anchored scheduling. Do not add redundant configuration fields: the first normalized `schedule` entry (default `05:00`) anchors initial discovery, while later entries bound terminal attempt cycles and permit fresh attempts. Ensure obsolete or unknown keys remain strictly rejected as `invalid_config`.

## Acceptance Criteria

- [ ] Initial discovery uses the first `cfg.Schedule` entry, and later entries start fresh cycles after terminal failures, without adding a new configuration field.
- [ ] Obsolete or unknown configuration keys return `invalid_config`.
- [ ] `plugin.yaml` and embedded manifest match the validated configuration schema.
- [ ] Manifest registration tests in `manifest_test.go` and `config_test.go` pass cleanly.

## Testing Seam

Unit tests in `internal/autoping/config_test.go` and `internal/autoping/manifest_test.go`.
