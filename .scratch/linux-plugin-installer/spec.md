# Linux Plugin Installer

Status: ready-for-agent

## Problem Statement

Linux operators currently have to inspect release asset names, determine the machine architecture, download and verify the correct archive, extract the shared library, and place it in CLIProxyAPI's architecture-specific plugin directory by hand. This is error-prone, especially because the release architecture name, checksum manifest, archive layout, and CLIProxyAPI discovery path must all agree.

## Solution

Provide a single Bash installer at `scripts/install/linux.sh` that installs or upgrades the latest released Linux build of the plugin. It detects supported CPU architecture, rejects Linux environments where CLIProxyAPI dynamic plugins are unavailable or the published binary ABI is unsupported, downloads the exact release ZIP and checksum manifest, verifies the archive before extraction, and atomically replaces the architecture-specific shared library under the existing CLIProxyAPI installation.

The installer does not manage the CLIProxyAPI process. After a successful installation it reports the installed path and tells the operator to restart or reload CLIProxyAPI.

## User Stories

1. As a Linux CLIProxyAPI operator, I want one command to install the plugin, so that I do not have to reproduce release naming and directory rules manually.
2. As an amd64 operator, I want the installer to select the amd64 release asset, so that CLIProxyAPI receives a compatible shared library.
3. As an arm64 operator, I want the installer to select the arm64 release asset, so that CLIProxyAPI receives a compatible shared library.
4. As an operator on an unsupported CPU architecture, I want installation to stop with a clear error, so that an incompatible artifact is never installed.
5. As an operator on Alpine, another musl system, or OpenWrt, I want installation to stop with a clear explanation, so that I do not install a glibc-built plugin into a host that cannot load it.
6. As an operator, I want the installer to require an existing CLIProxyAPI installation root, so that a successful message cannot refer to an orphaned directory on a machine without the host.
7. As an operator with an existing CLIProxyAPI installation, I want missing plugin subdirectories created automatically, so that first-time plugin installation needs no manual directory setup.
8. As an operator, I want the latest stable GitHub release selected automatically, so that rerunning the installer upgrades the plugin without requiring a version argument.
9. As an operator, I want the installer to match the exact release asset basename, so that a macOS, Windows, or wrong-architecture artifact cannot be selected accidentally.
10. As an operator, I want the downloaded ZIP checked against the release checksum manifest, so that corrupted or substituted archives are rejected.
11. As an operator, I want a missing checksum entry or unavailable checksum manifest to stop installation, so that verification cannot silently degrade.
12. As an operator with a currently installed plugin, I want a failed download, checksum, or extraction to leave that plugin unchanged, so that a failed upgrade does not break the host.
13. As an operator upgrading the plugin, I want the verified shared library replaced atomically, so that CLIProxyAPI never observes a partially written file.
14. As an operator, I want the final library installed at `$HOME/cliproxyapi/plugins/linux/<arch>/cliproxyapi-auto-ping.so`, so that it matches CLIProxyAPI's Linux plugin discovery layout.
15. As an operator, I want the installer to report the exact installed path, so that I can inspect the result directly.
16. As an operator, I want the installer not to restart or kill CLIProxyAPI, so that it cannot disrupt a deployment method it does not understand.
17. As an operator, I want a clear restart or reload reminder after installation, so that the running host begins using the new library.
18. As a maintainer, I want the installer to use the release workflow's existing artifact and checksum contracts, so that installation cannot drift from packaging.
19. As a maintainer, I want the installer to use standard Linux command-line tools instead of a new framework or dependency, so that the implementation remains auditable.
20. As a maintainer, I want the actual installer exercised as a black box with controlled platform and download commands, so that tests cover operator-visible behavior rather than internal shell functions.
21. As a maintainer, I want checksum failure tested against an existing installed plugin, so that the no-destructive-upgrade invariant remains protected.
22. As a maintainer, I want both architecture mappings tested, so that release asset selection and destination layout cannot diverge.
23. As a maintainer, I want unsupported libc environments tested, so that future simplification cannot accidentally remove the safety gate.

## Implementation Decisions

- The installer supports Linux only and requires Bash.
- Supported CPU mappings are `x86_64` or `amd64` to release architecture `amd64`, and `aarch64` or `arm64` to release architecture `arm64`.
- OpenWrt and musl-based systems are rejected before download. Published Linux plugin artifacts are built on Ubuntu/glibc, while the reference CLIProxyAPI installer selects a dynamic-plugin-disabled host build for those environments.
- The CLIProxyAPI installation root is `$HOME/cliproxyapi`. It must already exist. The installer creates only the missing plugin subdirectories beneath that root.
- The architecture-specific destination is `$HOME/cliproxyapi/plugins/linux/<arch>/cliproxyapi-auto-ping.so`.
- The installer selects only GitHub's latest stable release. Explicit versions, prereleases, and rollback commands are not supported.
- The release tag supplies the version after removing its leading `v`.
- The expected release archive name is `cliproxyapi-auto-ping_<version>_linux_<arch>.zip`.
- The archive must contain `cliproxyapi-auto-ping.so` at its root, matching the existing release workflow.
- Downloading supports either `curl` or `wget`. ZIP extraction requires `unzip`. SHA-256 verification requires `sha256sum`. Missing required tools produce a clear error before installation begins.
- The installer downloads the release archive and `checksums.txt`, finds the exact archive entry in the checksum manifest, and fails closed when the manifest is unavailable, malformed for that asset, or does not match the downloaded bytes.
- Extraction writes the single shared-library member to a temporary file in the destination directory. Only after download, verification, and extraction succeed does an atomic rename replace the destination.
- No backup, version directory, symlink, local version metadata, compatibility path, or rollback state is retained.
- Rerunning the installer performs the same verified replacement and therefore serves as the upgrade path.
- The installer does not start, stop, restart, reload, or discover CLIProxyAPI processes or services.
- Success output includes the exact installed library path and a restart or reload instruction.
- The script is self-contained and can run from a repository checkout or from its raw GitHub URL without relying on adjacent files.

## Testing Decisions

- Tests assert command exit status, selected release asset, destination path, preservation of an existing plugin on failure, and final installed bytes. They do not assert private shell function structure or source text.
- The primary seam is the real installer process executed with a temporary `HOME` and a controlled `PATH` containing fake platform, downloader, checksum, and ZIP commands.
- The success path verifies amd64 selection, exact checksum validation, extraction of the library, creation of missing plugin subdirectories, replacement of an existing plugin, and the reported destination.
- A second architecture scenario verifies that `aarch64` selects the `arm64` release asset and destination directory.
- A checksum mismatch scenario starts with an existing destination library and verifies a non-zero exit while preserving its bytes.
- Unsupported musl or OpenWrt detection verifies a non-zero exit before any release download.
- Missing CLIProxyAPI root and missing required command scenarios verify clear non-zero failures without creating an orphan installation.
- The runnable check uses shell and standard utilities already required by the installer; no test framework dependency is added.
- Existing repository tests do not cover shell installers, so this feature introduces one highest-level black-box check rather than lower-level tests for individual shell helpers.

## Out of Scope

- macOS or Windows installation.
- Linux architectures other than amd64 and arm64.
- musl, Alpine, or OpenWrt plugin installation.
- Building the plugin locally.
- Selecting an explicit release version, prerelease, or development artifact.
- Status, uninstall, downgrade, or rollback commands.
- Backups, multiple installed versions, version metadata, or symlink management.
- Editing CLIProxyAPI configuration or enabling the plugin automatically.
- Starting, stopping, restarting, reloading, or discovering CLIProxyAPI services and processes.
- Installing CLIProxyAPI itself.
- Supporting a configurable installation root or system-wide installation.
- Introducing `jq`, a test framework, or a general-purpose installer framework.

## Further Notes

- The release workflow is the source of truth for Linux architectures, archive names, archive contents, and checksum publication.
- The reference CLIProxyAPI installer informs architecture detection, latest-release lookup, downloader fallback, and fail-fast dependency checks, but its host lifecycle, configuration, systemd, and version-directory behavior does not apply to this single-file plugin.
- No glossary or ADR update is required. These decisions are operational, easy to reverse, and do not introduce a new domain concept or a surprising architectural commitment.
