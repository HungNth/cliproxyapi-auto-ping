# Negative boolean auto_ping_disabled configuration

Status: accepted (supersedes [ADR 0007](0007-auto-ping-enabled-by-default.md))

The plugin configuration field is named `auto_ping_disabled` with manifest default `false`. When a plugin is freshly installed and enabled, the key is omitted from the host configuration file (`config.yaml`), so the Management Center Web UI receives `undefined` and initializes the toggle to `false` (OFF). Because the switch represents disabling the feature, an OFF toggle accurately reflects that background Auto-Ping is active without requiring any manual edit.

ADR 0007 attempted to establish default enablement with `auto_ping_enabled: true`. However, the Management Center frontend defaults omitted boolean fields to `false` (`undefined === true` evaluates to `false`), causing the UI toggle to render as OFF despite the runtime default being enabled. Neither the CLIProxyAPI host SDK nor the management API conveys a `default_value` property for configuration fields, and the host never seeds plugin-owned defaults into `config.yaml`. Using the negative boolean `auto_ping_disabled: false` aligns the runtime default with the host UI presentation without requiring host-side schema or frontend modifications.

In the internal Go runtime, the operational state remains represented affirmatively as `Config.AutoPingEnabled = !raw.AutoPingDisabled` to keep scheduling and activation decisions free of double-negative expressions. Management API status endpoints continue to report `auto_ping.enabled`.

In accordance with project engineering principles, obsolete `auto_ping_enabled` references and compatibility fallbacks are removed entirely rather than preserved.

## Considered Options

- Modifying `CLIProxyAPI` and `Cli-Proxy-API-Management-Center` to support `default_value` on `ConfigField`: rejected for current delivery because it introduces upstream dependencies across separate host repositories.
- Seeding `config.yaml` host-side at install time: rejected per ADR 0007; duplicates manifest defaults and pollutes host configuration.
- Backward compatibility fallback parsing `auto_ping_enabled`: rejected per repository rule against maintaining obsolete paths or compatibility layers.
