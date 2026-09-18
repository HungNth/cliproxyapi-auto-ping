# 01: Schedule Configuration & Manifest Schema

**Parent:** `.scratch/scheduled-auto-ping/spec.md`

**What to build:**
Operators can configure `schedule` as a list of 24-hour `HH:MM` daily milestones and `timezone` as a timezone location (defaulting to `["05:00", "10:00", "15:00", "20:00"]` and `"Local"`). Obsolete `scan_interval` and `activation_delay` configuration fields are completely removed from manifest defaults, schema validation, and config structs without backward compatibility fallbacks.

**Blocked by:** None (can start immediately)

**Status:** resolved

**Testing Seam:**
Configuration parsing and validation tests, manifest schema validation tests, and embedded manifest drift verification tests.

**Demo Path:**
`go test ./internal/autoping/... -run "Test.*Config.*|Test.*Manifest.*"`

**Acceptance Criteria:**
- [x] Plugin manifest declares default `schedule: ["05:00", "10:00", "15:00", "20:00"]` and `timezone: "Local"`.
- [x] Manifest defaults and schemas omit `scan_interval` and `activation_delay`.
- [x] Valid `schedule` values with unique, sorted `HH:MM` times parse correctly into runtime config.
- [x] Invalid `schedule` values (e.g. invalid hours/minutes, malformed time format, empty schedule) produce clear validation errors.
- [x] Valid `timezone` values (e.g. `"Local"`, `"UTC"`, `"Asia/Ho_Chi_Minh"`) load the expected `time.Location`; invalid timezones produce validation errors.
- [x] Embedded manifest drift test passes against canonical `plugin.yaml`.
