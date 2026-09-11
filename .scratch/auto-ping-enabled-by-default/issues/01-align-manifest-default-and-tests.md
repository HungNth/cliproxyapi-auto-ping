# 01: Align the shipped default with the tests

**What to build:** Make the test suite exercise the shipped `auto_ping_enabled: true` default instead of a fixture that pins `false`, and add the missing drift guard that fails when the embedded manifest default changes.

**Blocked by:** none

**Status:** ready-for-agent

**Parent specification:** Auto-Ping Enabled By Default.

**Demo:** Run the test suite with the fixture aligned to `true`, then edit `plugin.yaml` to `auto_ping_enabled: false` and show the embed test failing.

- [ ] The internal test fixture manifest declares `auto_ping_enabled: true`, matching `plugin.yaml`.
- [ ] The default-configuration test asserts Auto-Ping is enabled when the key is omitted, and is renamed to describe that behavior.
- [ ] The disabled-scanner test sets `auto_ping_enabled: false` explicitly and still proves no host request is made.
- [ ] The omitted-settings precedence test asserts the omitted key inherits `true`, and adding an explicit `false` assertion proves the override still wins.
- [ ] The registration-metadata test asserts the `auto_ping_enabled` default value and generated default hint reflect `true`.
- [ ] The embed test asserts the embedded manifest default is `true`, so changing `plugin.yaml` alone fails the suite.
- [ ] The manifest field description in `plugin.yaml` and in the fixture describe the opt-out rather than an opt-in.
