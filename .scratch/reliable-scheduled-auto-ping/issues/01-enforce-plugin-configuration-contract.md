# 01: Enforce the Plugin Configuration Contract

**Parent:** `../spec.md`

**What to build:** Make plugin registration and reconfiguration reject obsolete or misspelled settings instead of silently ignoring them, while changing the default Milestone Retry cooldown to one minute and preserving explicit operator overrides.

**Blocked by:** None (can start immediately)

**Status:** resolved

**Testing seam:** Drive configuration through the plugin lifecycle boundary and inspect the lifecycle response, registration metadata, effective status, and resulting scheduled behavior.

**Demo path:** Register with defaults, register with an explicit cooldown, and reconfigure with obsolete and unknown keys; show valid configuration succeeding and invalid configuration returning `invalid_config`.

- [x] Plugin instance YAML is decoded with strict known-field validation.
- [x] `scan_interval` is rejected as `invalid_config`.
- [x] `activation_delay` is rejected as `invalid_config`.
- [x] An arbitrary unknown or misspelled field is rejected as `invalid_config`.
- [x] No compatibility alias or silent discard path is introduced for removed keys.
- [x] The Plugin Manifest default for `retry_cooldown` is `1m`.
- [x] An explicit positive `retry_cooldown` continues to override the default.
- [x] Registration metadata, effective status, tests, and operator documentation agree on the `1m` default.
