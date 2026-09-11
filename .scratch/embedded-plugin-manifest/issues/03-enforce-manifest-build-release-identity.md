# 03: Enforce manifest identity in builds and releases

**What to build:** Align local builds, continuous integration, release packaging, and installation guidance with the canonical Plugin Manifest identity and version. Produce `cliproxyapi-auto-ping` artifacts, reject release tags that disagree with manifest version `0.1.0`, and preserve a single-library release package with no runtime companion manifest.

**Blocked by:** 01: Serve metadata and defaults from the embedded Plugin Manifest.

**Status:** ready-for-agent

**Parent specification:** Embedded Plugin Manifest.

**Demo:** Build the platform library, inspect its manifest-backed registration version, and package a matching-tag release artifact; then run the release-version check with a mismatched tag and show that it fails before artifact creation.

- [ ] Local build output uses the `cliproxyapi-auto-ping` library basename on every supported operating system.
- [ ] Continuous-integration artifact labels and uploaded library names use `cliproxyapi-auto-ping`.
- [ ] Release archives use the `cliproxyapi-auto-ping_<version>_<os>_<arch>.zip` naming contract and contain the platform library at the archive root.
- [ ] The host configuration example uses `plugins.configs.cliproxyapi-auto-ping`, matching the discovered artifact ID.
- [ ] Link-time plugin-version injection is removed; registration reports the manifest version in local and release builds.
- [ ] Release automation removes the leading `v` from the tag, compares the result with the manifest version, and stops before building when they differ.
- [ ] A matching `v0.1.0` tag passes the version check and names the release assets with version `0.1.0`.
- [ ] The manifest ID is checked against the stable build and packaging basename so an accidental ID-only edit cannot create conflicting identities.
- [ ] Release packages do not include `plugin.yaml` or require it beside the installed shared library.
- [ ] Build and installation documentation uses the new library, archive, configuration-key, and release naming consistently.
- [ ] A build smoke check proves that the produced plugin initializes from embedded manifest data with no companion file present.
