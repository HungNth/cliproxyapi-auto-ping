# 06: Ship the operator contract and verify Codex isolation

**What to build:** Deliver the complete operator-facing Antigravity Auto-Ping release: canonical manifest metadata, configuration guidance, management examples, release notes, a buildable plugin artifact, and end-to-end verification that the new provider engine does not alter any existing Codex behavior.

**Blocked by:** 04: Advance and recover independent reset-aligned target cycles; 05: Complete two-target manual results and management diagnostics.

**Status:** resolved

**Parent specification:** Google Antigravity Reset-Aligned Auto-Ping Specification.

**Testing seam:** Run the full Runtime test suite using the approved fake Host/synthetic-clock seam, then build the actual shared library and inspect its embedded registration/configuration contract.

**Demo:** Configure the built plugin with the default Antigravity reset margin, show provider-neutral registration metadata and documented management examples, exercise the complete deterministic Runtime scenario, and show all existing Codex regression checks still pass.

- [x] The canonical Plugin Manifest exposes `antigravity_reset_margin` with default `30s`, provider-neutral shared-field descriptions, and Codex-specific model/transport descriptions.
- [x] Plugin description and registration metadata describe reset-aligned Codex and Antigravity behavior without claiming quota increase, bypass, or extension.
- [x] Operator documentation covers shared enablement, eligibility, fixed target models, five-hour-only scope, initial anchor/catch-up, reset margin, minimum host version, quota observation, failure behavior, manual 200/207/502 responses, state/status shape, and security limits.
- [x] The changelog records Antigravity support and the CLIProxyAPI v7.3.3 prerequisite.
- [x] Documentation keeps `loadCodeAssist` project discovery distinct from `retrieveUserQuotaSummary` quota parsing.
- [x] The complete approved Runtime seam passes for automatic, manual, persistence, restart, reconfiguration, failure, concurrency, status, diagnostics, and secret-exposure behavior.
- [x] Existing Codex eligibility, `/wham/usage` parsing, dynamic targets, mandatory daily anchor, stabilization, request payload, model fallback, direct transport, scheduler fallback, management responses, state fields, and counters remain unchanged.
- [x] Codex direct and host-executed payload regression checks continue to prove `max_output_tokens` and `max_tokens` are absent.
- [x] Existing state version 3 Codex documents load without migration or deletion and coexist with additive Antigravity target state.
- [x] The release shared library builds successfully and reports the embedded manifest identity, version, and configuration contract.
- [x] No compatibility shim for older hosts, direct Google activation, OAuth refresh implementation, weekly scheduling, configurable Antigravity models, or new quota-provider surface is introduced.

## Answer

Delivered operator contract and verified complete Codex isolation:
1. Updated canonical `plugin.yaml` to declare `antigravity_reset_margin: 30s` with provider-neutral shared descriptions and Codex-specific model/transport descriptions.
2. Updated `README.md` and `CHANGELOG.md` with Antigravity behaviors, manual multi-status API examples, and the CLIProxyAPI v7.3.3 prerequisite.
3. Verified that all existing Codex contract and regression tests pass unchanged, including payload checks confirming `max_output_tokens` and `max_tokens` are absent.
4. Full test suite passes cleanly with zero warnings under `go vet`.
