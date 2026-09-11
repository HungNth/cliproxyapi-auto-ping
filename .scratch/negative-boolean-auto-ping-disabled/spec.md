# Negative Boolean auto_ping_disabled Configuration

Status: ready-for-agent

## Problem Statement

In CLIProxyAPI Management Center (`Cli-Proxy-API-Management-Center`), omitted configuration fields in `buildPluginConfigDraft` are evaluated as `false` when their type is boolean (`value === true` where `value` is `undefined`). Because the host never writes plugin-owned defaults to `config.yaml` upon install or enablement, a newly activated plugin with default `auto_ping_enabled: true` appears in the "Edit Plugin Config" modal with the toggle switch turned OFF.

This creates confusion for operators:
1. The toggle visually indicates that background Auto-Ping is inactive, even though the in-memory runtime default is active.
2. If an operator saves changes in the modal, the UI writes `auto_ping_enabled: false` into `config.yaml`, unintentionally disabling the scanner.

## Solution

Replace the configuration field `auto_ping_enabled` with `auto_ping_disabled: false`.

With this change:
- When omitted from `config.yaml`, the Management Center evaluates `undefined === true` as `false`, correctly displaying the toggle as OFF (not disabled -> active).
- Turning the toggle ON explicitly sets `auto_ping_disabled: true`, opting out of background pings.
- The internal Go runtime continues to store `Config.AutoPingEnabled = !raw.AutoPingDisabled` so scheduler and activation code remain affirmative and avoid double-negative logic.
- Management API `GET /status` continues to report `auto_ping.enabled` reflecting real operational state.
- Obsolete `auto_ping_enabled` keys are completely removed without backward compatibility fallbacks per project engineering guidelines.

## User Stories

1. As a Management Center user, when I open the plugin edit config modal on a newly enabled plugin, I want the toggle to be OFF for `auto_ping_disabled`, accurately reflecting that Auto-Ping is not disabled.
2. As a plugin operator, I want to explicitly turn the toggle ON to disable Auto-Ping, and have that opt-out preserved in `config.yaml`.
3. As a plugin operator, I want the manifest description and README to clearly explain that turning `auto_ping_disabled` to `true` stops background requests.
4. As a plugin maintainer, I want internal Go runtime logic to remain affirmative (`AutoPingEnabled`), preventing accidental double-negative errors in the scanner.
5. As a plugin maintainer, I want test suites and embedded manifest drift guards to verify the `auto_ping_disabled: false` default.

## Implementation Decisions

- Field name: `auto_ping_disabled`.
- Type: `boolean`.
- Shipped default: `false`.
- Manifest description: `"Set to true to disable background Codex inference requests."`
- Runtime config parsing:
  - `rawConfig.AutoPingDisabled *bool`
  - In `configFromRaw`, if `raw.AutoPingDisabled != nil`, `cfg.AutoPingEnabled = !*raw.AutoPingDisabled`.
  - If omitted, inherits manifest default (`!false = true`).
  - `fieldValue("auto_ping_disabled")` returns `!c.AutoPingEnabled`.
- Status payload: `statusConfig.Enabled` remains `cfg.AutoPingEnabled`.
- ADR 0008 supersedes ADR 0007.
- No backward compatibility or fallback parsing for `auto_ping_enabled`.

## Testing Decisions

- Test fixture manifest updated to declare `auto_ping_disabled: false`.
- Config tests assert:
  - Omitted `auto_ping_disabled` resolves to `AutoPingEnabled == true`.
  - Explicit `auto_ping_disabled: true` resolves to `AutoPingEnabled == false`.
  - Explicit `auto_ping_disabled: false` resolves to `AutoPingEnabled == true`.
- Manifest validation tests verify required field `auto_ping_disabled` and default `false`.
- Embed tests verify the embedded `plugin.yaml` default is `false`.
- Management registration tests verify `auto_ping_disabled` field metadata and default hint `false`.
- Runtime integration tests verify scanner does not fire when `auto_ping_disabled: true`.

## Out of Scope

- Modifying upstream host SDK or host Management Center repositories.
- Backward compatibility aliases or migrations for `auto_ping_enabled`.
