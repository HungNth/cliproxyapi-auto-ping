# 01: Install the latest Linux plugin safely

**What to build:** Deliver the complete Linux installation path defined by the parent specification: an operator can run the public Bash installer on a supported CLIProxyAPI host, have the correct latest release selected and verified, and receive an atomically installed plugin plus a clear restart or reload instruction. Include the runnable black-box verification and operator-facing usage documentation needed to keep this behavior maintainable.

**Blocked by:** None (can start immediately).

**Status:** resolved

- [x] Running on glibc Linux maps `x86_64` or `amd64` to the amd64 release and `aarch64` or `arm64` to the arm64 release.
- [x] Unsupported architectures, musl systems, and OpenWrt fail clearly before downloading a release artifact.
- [x] Installation fails clearly when the CLIProxyAPI root is absent, while an existing root receives any missing plugin subdirectories automatically.
- [x] The installer accepts either `curl` or `wget`, requires `unzip` and `sha256sum`, and reports missing dependencies before modifying the installation.
- [x] The latest stable release tag determines the exact Linux ZIP basename matching the release workflow.
- [x] The release ZIP is installed only after its exact entry in `checksums.txt` is found and SHA-256 verification succeeds.
- [x] Download, checksum, or extraction failure leaves an existing plugin library unchanged.
- [x] The verified archive member is written to a destination-local temporary file and atomically replaces the architecture-specific `cliproxyapi-auto-ping.so`.
- [x] Successful output reports the exact installed path and tells the operator to restart or reload CLIProxyAPI without attempting process or service management.
- [x] A framework-free black-box shell check executes the real installer with a temporary home and controlled commands, covering amd64 success, arm64 mapping, checksum failure preservation, unsupported libc rejection, absent host root, and missing dependencies.
- [x] Operator documentation shows how to run the installer from a checkout and from the raw GitHub URL, and release notes record the new installation path.
