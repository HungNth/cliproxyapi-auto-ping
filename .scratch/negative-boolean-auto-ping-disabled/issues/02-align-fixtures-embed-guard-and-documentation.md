# 02: Align fixtures, embed guard, and documentation

**What to build:** Align all internal test fixtures, regression test suites, embedded manifest drift guards, and operator documentation with `auto_ping_disabled: false`.

**Blocked by:** 01

**Status:** resolved

**Parent specification:** Negative Boolean auto_ping_disabled Configuration.

**Demo:** Run `make test` proving all unit and embed guard tests pass, then review `README.md` demonstrating the operator documentation reflects `auto_ping_disabled: false`.

- [x] All test fixture manifests in `internal/autoping/` declare `auto_ping_disabled: false`.
- [x] Disabled-scanner integration tests explicitly provide `auto_ping_disabled: true` and verify no Codex inference request is dispatched.
- [x] `manifest_embed_test.go` asserts the embedded `plugin.yaml` declares default `auto_ping_disabled: false`.
- [x] `README.md` configuration examples and default settings table document `auto_ping_disabled: false` and describe the opt-out mechanism.
- [x] `make test` runs and passes cleanly with no failures.
