# Auto-Ping enabled by default

Status: superseded by [ADR 0008](0008-negative-boolean-auto-ping-disabled.md)

A freshly installed plugin that is enabled starts with `auto_ping_enabled: true`, so no manual configuration edit is required before the scanner runs. ADR 0003 required an explicit `auto_ping_enabled: true` on top of the host-owned `enabled` switch; that second opt-in is dropped because the operator already performs an explicit, host-owned enablement and the plugin manifest is the single source of defaults (ADR 0006).

The host never writes plugin-owned keys into its configuration file, so the default only applies while the key is absent. An `auto_ping_enabled: false` written by the operator is authoritative: re-enabling the plugin preserves it, and only replacing the whole instance configuration without the key restores the default. Existing installations are not migrated — a stored `false` cannot be distinguished from a deliberate opt-out, so it is left untouched.

## Considered Options

- Migrating existing instances to `true`: rejected, it would override a deliberate opt-out.
- Seeding `auto_ping_enabled` host-side at install time: rejected, it duplicates the manifest default source and was already out of scope for ADR 0006.
