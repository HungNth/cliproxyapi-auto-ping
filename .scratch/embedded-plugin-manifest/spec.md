# Embedded Plugin Manifest

Status: ready-for-agent

## Problem Statement

Plugin identity, release metadata, configuration field descriptions, runtime defaults, build naming, and release versioning are currently spread across Go source, build commands, workflows, and documentation. Values such as the display name, version, author, and model candidates can drift because changing one copy does not update the others. The existing `auto-ping` identifier must also be replaced consistently with `cliproxyapi-auto-ping` while the project is still in development.

## Solution

Introduce one strict repository-root `plugin.yaml` as the canonical Plugin Manifest. Embed it into every shared-library build so releases remain single-file artifacts. The runtime constructor will accept the embedded manifest bytes, parse and validate them, and either return a fully initialized runtime or an error. Registration metadata, configuration field descriptors, default runtime configuration, generated default hints, status identity, and management route prefixes will use the validated manifest. Explicit Plugin Instance Configuration continues to override manifest defaults.

Perform a clean identifier cutover from `auto-ping` to `cliproxyapi-auto-ping` across the artifact name, release archive name, host configuration key, management routes, and status output. Change the default state location to `cliproxyapi-auto-ping/state.json` without aliases or migration because the plugin has not reached a tagged release.

## User Stories

1. As a plugin maintainer, I want to edit plugin identity in one manifest, so that registration metadata cannot drift across source files.
2. As a plugin maintainer, I want to edit default runtime configuration in one manifest, so that runtime behavior and displayed defaults remain aligned.
3. As a plugin maintainer, I want configuration field descriptions in the manifest, so that the management registration surface is reviewable without reading Go source.
4. As a plugin maintainer, I want every supported configuration field represented exactly once, so that the management UI and runtime configuration contract cannot diverge silently.
5. As a plugin maintainer, I want strict manifest decoding, so that misspelled keys fail visibly instead of being ignored.
6. As a plugin maintainer, I want invalid default values rejected during plugin initialization, so that a broken release cannot run with partial or implicit settings.
7. As a plugin maintainer, I want no hardcoded metadata or default fallback, so that the manifest remains the single source of truth.
8. As a plugin developer, I want the manifest embedded at build time, so that local and release builds use the same canonical information.
9. As a plugin developer, I want one runtime-construction seam for manifest parsing and initialization, so that production and tests exercise the same path.
10. As a plugin developer, I want a malformed manifest to return a constructor error, so that plugin initialization can fail cleanly without a panic.
11. As a plugin operator, I want releases to remain a single shared-library file, so that installation does not require locating or synchronizing a companion manifest.
12. As a plugin operator, I want explicit Plugin Instance Configuration to override manifest defaults, so that each installation can select its own model candidates and operational settings.
13. As a plugin operator, I want omitted instance settings to inherit the manifest defaults, so that the plugin has predictable behavior with minimal configuration.
14. As a Management Center user, I want registration descriptions to show defaults derived from the manifest, so that displayed hints never become stale when defaults change.
15. As a Management Center user, I want the display name `Codex 5h Auto-Ping`, so that the plugin remains recognizable after its identifier changes.
16. As a Management Center user, I want status output to report `cliproxyapi-auto-ping`, so that status identity matches the installed artifact and configuration key.
17. As an API consumer, I want management routes under `/cliproxyapi-auto-ping`, so that route identity matches the new plugin identifier.
18. As a CLIProxyAPI administrator, I want the plugin configuration key to be `cliproxyapi-auto-ping`, so that host discovery and configuration use one identifier.
19. As a release consumer, I want shared libraries and release archives named with `cliproxyapi-auto-ping`, so that downloaded assets match the plugin identifier.
20. As a release manager, I want version `0.1.0` declared in the manifest, so that the first release has one canonical version source.
21. As a release manager, I want a release tag to be checked against the manifest version, so that a mismatched tag cannot publish a falsely versioned binary.
22. As a release manager, I want link-time version overrides removed, so that the built plugin cannot report a version different from its manifest.
23. As a plugin operator, I want the default state location to be `cliproxyapi-auto-ping/state.json`, so that new installations use the new identifier consistently.
24. As a plugin operator during development, I want no migration from `auto-ping/state.json`, so that the implementation stays clean before the first tagged release.
25. As a maintainer, I want capabilities and route behavior defined beside their Go implementations, so that the manifest cannot advertise behavior the binary does not implement.
26. As a maintainer, I want route prefixes derived from the validated Plugin Manifest ID, so that registration and request handling cannot disagree about endpoint names.
27. As a maintainer, I want the manifest ID checked against the build and packaging contract, so that an accidental YAML edit cannot create an artifact with conflicting identities.
28. As a tester, I want valid and invalid manifests exercised through the runtime constructor, so that validation behavior is observable at the highest practical seam.
29. As a tester, I want plugin registration exercised through the existing plugin lifecycle interface, so that tests assert consumer-visible metadata rather than internal field copies.
30. As a future maintainer, I want the manifest decision recorded in the domain documentation, so that nobody reintroduces hardcoded metadata or a runtime companion file without revisiting the trade-off.

## Implementation Decisions

- The canonical term is **Plugin Manifest**: the declaration of plugin identity, release metadata, configuration field descriptors, and default runtime configuration.
- The canonical term is **Plugin Instance Configuration**: host-managed settings for one installed plugin instance. Explicit instance values override Plugin Manifest defaults.
- The Plugin Manifest is a strict YAML document named `plugin.yaml` at the repository root and is embedded into the binary during compilation.
- The manifest schema version is `1`.
- The canonical plugin ID is `cliproxyapi-auto-ping`.
- The display metadata remains: name `Codex 5h Auto-Ping`, version `0.1.0`, author `HungNth`, repository `https://github.com/HungNth/cliproxyapi-auto-ping`, and description `Starts inactive Codex rolling five-hour windows with one minimal targeted request.`
- The manifest contains descriptors for exactly the fourteen supported configuration fields: name, type, enum values where applicable, and description. Missing, duplicate, and unsupported descriptors are invalid.
- The manifest contains all default runtime configuration: automatic ping disabled, one-minute scan interval, five-second activation delay, fifteen-minute retry cooldown, concurrency one, sixty-second request timeout, prompt `ping`, one output token, automatic model selection, model candidates ordered as `gpt-5.5` then `gpt-5.6-luna`, direct HTTP transport, scheduler fallback enabled, no excluded credentials, and state location `cliproxyapi-auto-ping/state.json`.
- Configuration field default hints are generated from the validated manifest defaults instead of repeating default literals in descriptions.
- YAML decoding rejects unknown keys. Required identity and metadata values must be non-empty. Default configuration must pass the same validation rules as Plugin Instance Configuration.
- Runtime construction is the single seam for loading the manifest: it accepts host access, manifest bytes, and runtime options, and returns either a runtime or an error.
- Plugin initialization returns failure when runtime construction rejects the embedded manifest. It does not panic and does not substitute fallback metadata or defaults.
- The runtime retains the validated Plugin Manifest and uses it for lifecycle registration, default configuration, status identity, and management route prefixes.
- Explicit Plugin Instance Configuration retains its current precedence over defaults, including `model` and `model_candidates`.
- Capabilities and management route behavior remain in Go because they describe implemented behavior. Only the route prefix is derived from the manifest ID.
- The ID cutover is complete and intentionally breaking: shared-library names, release archive names, artifact labels, host configuration keys, management routes, status output, examples, and documentation use `cliproxyapi-auto-ping`.
- No aliases for `auto-ping` are retained.
- The default state path changes to `cliproxyapi-auto-ping/state.json`; no migration or fallback lookup is implemented.
- The manifest version is authoritative. Link-time version injection is removed. Release automation strips the leading `v` from the tag, verifies equality with the manifest version, and stops before building on mismatch.
- The manifest ID is declarative but checked against the stable build and packaging contract. Changing it requires an intentional contract-wide cutover rather than silently renaming only the runtime.
- Release packages continue to contain only the platform shared library at the archive root; `plugin.yaml` is not shipped as a runtime companion file.

## Testing Decisions

- Tests assert consumer-visible behavior, invariants, precedence, and real error cases; they do not assert implementation wiring, source text, or private field copies.
- The primary test seam is runtime construction from manifest bytes. This is the same seam used by the embedded production manifest.
- A valid manifest is followed through lifecycle registration to verify the visible ID, display metadata, configuration field order and descriptions, generated default hints, and version.
- Invalid-manifest cases cover unknown YAML keys, missing required metadata, unsupported schema version, duplicate configuration fields, missing supported fields, unsupported field names or types, empty automatic model candidates, invalid durations, invalid concurrency, invalid transport, and an empty state path.
- Plugin Instance Configuration tests verify that explicit values override manifest defaults while omitted values inherit them.
- Management registration and request handling tests verify the `/cliproxyapi-auto-ping` route prefix and reject the obsolete `/auto-ping` endpoints.
- Status tests verify that the visible plugin ID and version come from the manifest without exposing credential secrets.
- Release automation verifies that a tag/version mismatch fails before artifact creation and that produced artifacts use the `cliproxyapi-auto-ping` basename.
- A build smoke check creates the platform shared library and confirms that no companion manifest is required at runtime.
- Existing configuration parsing tests, management lifecycle tests, and status-security tests provide the prior patterns for these checks and should be updated rather than duplicated at lower seams.

## Out of Scope

- Editing or reloading the Plugin Manifest after the shared library has been built.
- Shipping `plugin.yaml` beside the shared library.
- Backward-compatible aliases for the `auto-ping` artifact, host configuration key, management routes, or status identity.
- Migrating or reading the previous `auto-ping/state.json` location.
- Moving capabilities or management route behavior into YAML.
- Changing the display name, author, repository, description, or initial version beyond the values already agreed.
- Changing CLIProxyAPI host behavior or adding host support for automatically persisting plugin defaults into its configuration file.
- Adding a general-purpose manifest framework for other plugins.

## Further Notes

- The repository currently has no release tags, so the clean identifier and state-path cutover occurs before the first published release.
- Existing development configurations must rename the host key from `auto-ping` to `cliproxyapi-auto-ping`.
- Existing development clients must update management endpoints from `/auto-ping/*` to `/cliproxyapi-auto-ping/*`.
- The first intended release remains `v0.1.0`, matching manifest version `0.1.0`.
