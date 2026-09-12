# 03: Rebuild distribution binary and verify clean test suite

**What to build:** Execute full test suite across the repository, build updated plugin distribution artifacts in `dist/`, and verify end-to-end readiness.

**Blocked by:** 02: Surface upstream error details and candidate model fallback on HTTP 400

**Status:** resolved

**Parent specification:** Fix Codex Auto-Ping HTTP 400 Bad Request and Surface Upstream Errors.

**Demo:** `go test -v ./...` passes all tests and `dist/cliproxyapi-auto-ping.dll` is recompiled.

- [x] Run full test suite: `go test -v ./...`
- [x] Build plugin binary in `dist/`: `go build -buildmode=c-shared -o dist/cliproxyapi-auto-ping.dll .`
- [x] Mark issues resolved in `.scratch/fix-codex-auto-ping-400/issues/`.
