# 01: Serve metadata and defaults from the embedded Plugin Manifest

**What to build:** Replace hardcoded plugin metadata, configuration field descriptors, and runtime defaults with one strict Plugin Manifest embedded into the shared library. Runtime construction must consume the manifest bytes through one constructor seam, return an error for invalid input, and expose validated values through lifecycle registration and Plugin Instance Configuration behavior.

**Blocked by:** None (can start immediately).

**Status:** ready-for-agent

**Parent specification:** Embedded Plugin Manifest.

**Demo:** Change the manifest display name and first model candidate, rebuild, then show that lifecycle registration and an instance with omitted settings expose the changed values without any Go-source edit. Restore the approved values before completion.

- [ ] The schema-version-1 manifest declares ID `cliproxyapi-auto-ping`, the approved display metadata, descriptors for exactly all fourteen supported configuration fields, and all approved runtime defaults.
- [ ] The manifest is embedded into the shared library; installation and runtime startup do not read a companion manifest file.
- [ ] Runtime construction accepts manifest bytes and returns either a usable runtime or a descriptive error; production and tests use this same seam.
- [ ] YAML decoding rejects unknown keys, unsupported schema versions, missing required metadata, duplicate fields, missing supported fields, unsupported field names or types, and malformed values.
- [ ] Manifest defaults pass the same duration, concurrency, model, transport, prompt, token, and state-location invariants as Plugin Instance Configuration.
- [ ] Invalid manifests fail plugin initialization without panic and without hardcoded metadata or default fallback.
- [ ] Lifecycle registration returns display name, version, author, repository, description, ordered configuration fields, enum values, and default hints derived from the manifest.
- [ ] Omitted Plugin Instance Configuration values inherit manifest defaults, while explicit instance values continue to override them, including `model` and `model_candidates`.
- [ ] Configuration default hints are generated from validated defaults rather than repeating default literals in field descriptions.
- [ ] Capabilities and management route behavior remain defined by the implementation rather than becoming data-driven manifest fields.
- [ ] Existing configuration and lifecycle tests are updated through the constructor seam, and focused invalid-manifest cases prove strict rejection at observable boundaries.
