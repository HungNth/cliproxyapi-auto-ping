# 02: Anchor a marked manual target to its five-hour reset

**What to build:** Let a successful marked manual Antigravity ping establish its next automatic trigger from the selected five-hour Quota Window. Quota observation must use refreshed host-owned credential material, keep project discovery separate from quota summary parsing, and remain observe-only whenever the reset cannot yet be accepted.

**Blocked by:** 01: Run one exact-pinned Antigravity target manually.

**Status:** resolved

**Parent specification:** Google Antigravity Reset-Aligned Auto-Ping Specification.

**Testing seam:** Drive marked and unmarked manual pings through the Runtime with fake credential documents, model responses, project-discovery responses, and quota-summary responses.

**Demo:** Mark a successful Gemini manual ping as cycle-processed, return a future Gemini five-hour reset, and show its next trigger at reset plus the configured margin; then return stale or missing quota and show observation retry without another inference.

- [x] The Plugin Manifest and runtime configuration expose `antigravity_reset_margin` as a non-negative duration with default `30s`.
- [x] Shared schedule, timezone, retry, timeout, prompt, concurrency, exclusion, and state settings retain their existing meaning; Codex model and transport settings remain Codex-only.
- [x] After successful marked inference, target state is durably set to `stabilizing` before quota observation begins.
- [x] Quota observation re-reads the credential document and never implements or persists Google OAuth refresh itself.
- [x] A missing project identifier is resolved through `loadCodeAssist` only for project discovery; its response is never parsed as quota summary data.
- [x] Quota summary uses `retrieveUserQuotaSummary` and accepts only the explicit `groups[].buckets[]` contract.
- [x] Gemini anchoring selects one unambiguous `gemini-5h` bucket and Claude/GPT anchoring selects one unambiguous `3p-5h` bucket with a valid RFC3339 future reset.
- [x] Weekly buckets, alternate model-quota shapes, unrelated nested resets, malformed timestamps, and duplicate matching buckets never become scheduling anchors.
- [x] A future selected reset transitions the successful target to waiting at reset plus `antigravity_reset_margin`.
- [x] Stale, missing, malformed, ambiguous, or non-future selected quota data keeps the target stabilizing and schedules observation-only retry without resending inference.
- [x] A quota authentication failure first re-reads fresh credential material and retries observation; repeated failure with fresh material becomes a credential-wide authentication failure.
- [x] An unmarked manual ping updates attempt results only and leaves quota observation and automatic target scheduling unchanged.
- [x] Existing Codex marked and unmarked manual-cycle behavior remains unchanged.

## Answer

Implemented marked manual Antigravity cycle anchoring:
1. Added `antigravity_reset_margin` (default `30s`) to `plugin.yaml`, manifest, and `Config`.
2. Quota observation re-reads the host auth document, resolves `project_id` directly or via `loadCodeAssist`, and queries `retrieveUserQuotaSummary`.
3. Parsed strictly from `groups[].buckets[]` selecting `gemini-5h` or `3p-5h` future RFC3339 reset timestamps and anchors at `resetTime + antigravity_reset_margin`.
4. Stale, non-future, or missing quota persists `stabilizing` without duplicate inference.
5. A quota 401/403 re-reads fresh auth and retries once before marking credential-wide authentication failure.
6. All Ticket 02 behavioral tests pass cleanly.
