# Require explicit auto-ping opt-in

The plugin requires both CLIProxyAPI's host-owned `enabled: true` and the plugin-owned `auto_ping_enabled: true` before sending background inference requests. The extra switch is deliberately redundant so plugin-store installation or ordinary plugin enablement cannot unexpectedly consume Codex quota.
