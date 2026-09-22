# 05: Start a Fresh Attempt at Every Schedule Milestone

**Parent:** `../spec.md`

**What to build:** Start an independent Milestone Attempt Cycle for every Eligible Credential at every Schedule Milestone, so temporary state and prior failures cannot suppress the next intended Auto-Ping while explicit safety controls remain authoritative.

**Blocked by:** 03: Cut Over to Milestone Attempt Cycle State; 04: Retry Recoverable Failures Through Cycle Boundaries

**Status:** resolved

**Testing seam:** Advance synthetic time across consecutive milestones through the plugin lifecycle boundary and inspect targeted Host requests, persisted state, and management status for each credential.

**Demo path:** Produce success, cooldown, temporary unavailable, and authentication failure at one milestone, advance to the next milestone, and show a fresh attempt for every otherwise Eligible Credential.

- [x] A successful earlier milestone does not suppress a later milestone.
- [x] Temporary unavailable state does not suppress a scheduled attempt.
- [x] Cooldown or exhausted historical retry state does not suppress a later milestone.
- [x] Prior 401/403 state does not suppress a later milestone even when the credential version is unchanged.
- [x] Global Auto-Ping opt-out, explicit credential exclusion, disabled credentials, and revoked credentials remain ineligible.
- [x] A newer milestone clears pending retry work from the older milestone.
- [x] If an older automatic request is still running for the credential, cancellation propagates and the newer request waits for it to exit rather than overlapping.
- [x] Cross-credential work remains bounded by `max_concurrency`.
- [x] Automatic attempts, retries, and manual pings never overlap for the same credential.
- [x] A successful manual ping satisfies the current milestone only when `mark_cycle_processed` is true.
- [x] An unmarked manual ping does not alter automatic milestone processing.
- [x] Attempted and failed work remains unprocessed; only persisted success marks the Processed Milestone.
