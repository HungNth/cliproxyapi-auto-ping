# 02: Cut over the runtime and management identity

**What to build:** Use the validated Plugin Manifest ID as the runtime identity and management route prefix. Complete the intentionally breaking management cutover to `cliproxyapi-auto-ping`, including status output and the new default state location, without retaining obsolete endpoints, aliases, or state migration.

**Blocked by:** 01: Serve metadata and defaults from the embedded Plugin Manifest.

**Status:** ready-for-agent

**Parent specification:** Embedded Plugin Manifest.

**Demo:** Register management routes, request the new status and diagnostics endpoints, and perform a manual ping through the new route prefix; show that the obsolete route prefix is neither registered nor handled.

- [ ] Lifecycle and management status report plugin ID `cliproxyapi-auto-ping` and the manifest version.
- [ ] Management registration exposes status, diagnostics, and manual-ping routes under `/cliproxyapi-auto-ping`.
- [ ] Management request handling derives the same route prefix from the validated manifest ID and successfully serves all three existing behaviors.
- [ ] Obsolete `/auto-ping/*` routes are not registered, accepted as aliases, or documented.
- [ ] The default persistent state location is `cliproxyapi-auto-ping/state.json`.
- [ ] The runtime neither reads nor migrates `auto-ping/state.json` when the new default location is used.
- [ ] Management capabilities and route behavior remain in Go; only identity and route prefix come from the manifest.
- [ ] Status and diagnostics continue to avoid exposing credential tokens or other authentication material.
- [ ] Consumer-facing configuration and management examples use the new ID and endpoint prefix.
- [ ] Tests verify the new registration and request paths through the existing lifecycle and management interfaces, including rejection of the obsolete prefix.
