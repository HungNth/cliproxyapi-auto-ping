# Embed one canonical plugin manifest in every build

The plugin declares its identity, release metadata, configuration field descriptors, and default runtime configuration in a strict repository-root manifest embedded into the shared library. Plugin Instance Configuration remains authoritative for explicit per-install overrides, while malformed manifests fail initialization and release tags must equal the manifest version; runtime companion files and hardcoded fallbacks were rejected because they create deployment coupling and multiple sources of truth.
