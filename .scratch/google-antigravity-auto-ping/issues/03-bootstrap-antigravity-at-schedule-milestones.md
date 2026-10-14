# 03: Bootstrap Antigravity targets at Schedule Milestones

**What to build:** Automatically discover every Eligible Antigravity Credential and bootstrap its Gemini and Claude/GPT Auto-Ping Targets at the configured daily anchor or Schedule Catch-Up. Each successful target must acquire its own future five-hour reset, while target-specific failures stay isolated and credential authentication failure stops sibling work.

**Blocked by:** 02: Anchor a marked manual target to its five-hour reset.

**Status:** resolved

**Parent specification:** Google Antigravity Reset-Aligned Auto-Ping Specification.

**Testing seam:** Run the configured Runtime under synthetic time with mixed Codex and Antigravity credential inventories, captured exact-pinned host executions, and quota-summary responses for both Model Families.

**Demo:** Start before the daily anchor and show no early Antigravity request; advance to the anchor and show two exact-pinned target requests followed by independently scheduled Gemini and Claude/GPT resets. Repeat startup after the anchor to show Schedule Catch-Up.

- [x] The global Auto-Ping setting and existing exclusion list govern both providers; no Antigravity-specific enablement field is introduced.
- [x] Unanchored Antigravity targets wait before the first daily Schedule Milestone and bootstrap at that milestone.
- [x] Startup after the applicable milestone performs Schedule Catch-Up for unanchored Antigravity targets.
- [x] Bootstrap executes exactly the two fixed five-hour targets for each Eligible Antigravity Credential under the credential's no-overlap lock.
- [x] Target execution remains serialized within one credential and globally bounded by the shared `max_concurrency` limit across Codex and Antigravity credential jobs.
- [x] Model or business failure on one target is recorded as target-local and does not prevent the sibling target from executing.
- [x] Credential authentication failure marks both targets terminal for the current Attempt Cycle and skips any queued sibling request.
- [x] One quota summary may anchor every successfully pinged bootstrap target, but a failed target is never advanced by its sibling's observation.
- [x] Gemini and Claude/GPT targets persist independent reset, trigger, retry, model, outcome, and counter state.
- [x] Weekly quota buckets never produce bootstrap targets or trigger times.
- [x] Mixed provider inventories cannot route Antigravity credentials through Codex usage or activation paths, or Codex credentials through Antigravity paths.
- [x] Existing Codex pre-anchor, Schedule Catch-Up, mandatory daily anchor, concurrency, request, and stabilization behavior remains unchanged.

## Answer

Delivered Antigravity milestone bootstrap and Schedule Catch-Up:
1. Integrated Antigravity credential discovery into `runDynamicSchedule`, preserving shared global enablement and exclusion lists.
2. Unanchored Antigravity targets sleep until the initial daily anchor (`05:00`), or perform catch-up immediately if startup occurs after it.
3. Serialized execution of both fixed targets under the credential in-flight lock, bounded by global `max_concurrency`.
4. Sibling failure isolation: model failure on one target does not prevent the second target from executing and anchoring.
5. Auth failure blocks both targets and the credential for the cycle, skipping queued sibling work.
6. All Ticket 03 behavioral tests pass cleanly with full Codex regression protection.
