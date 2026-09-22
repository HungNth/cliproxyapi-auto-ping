# 04: Retry Recoverable Failures Through Cycle Boundaries

**Parent:** `../spec.md`

**What to build:** Retry Recoverable Auto-Ping Failures until success or the current Milestone Attempt Cycle ends, without a fixed attempt-count cap, while treating authentication, model, and business failures as terminal for the current cycle.

**Blocked by:** 01: Enforce the Plugin Configuration Contract; 02: Make Scheduler Reconfiguration an Atomic Handoff; 03: Cut Over to Milestone Attempt Cycle State

**Status:** resolved

**Testing seam:** Drive real automatic attempts through the plugin lifecycle boundary with controlled Host responses and synthetic time, then reload persisted state to verify restart behavior.

**Demo path:** Fail more than three times with recoverable responses and then succeed; separately return authentication, model, and business failures and show no minute retry before the next milestone.

- [x] Temporary credential discovery/read failures, transport failures, timeouts, stream failures, HTTP 429, and HTTP 5xx schedule a Milestone Retry.
- [x] Recoverable failures continue beyond three attempts when the cycle remains active.
- [x] The next retry is based on completion time plus `retry_cooldown`.
- [x] Standard `Retry-After` delay-seconds and HTTP-date values are parsed when present.
- [x] The later of `retry_cooldown` and valid `Retry-After` determines the retry time.
- [x] Invalid, absent, or past `Retry-After` values fall back to `retry_cooldown` without changing failure classification.
- [x] Authentication failures perform no Milestone Retry in the current cycle.
- [x] Exhausted model-candidate failures and business failures perform no Milestone Retry in the current cycle.
- [x] Automatic model candidates are still tried immediately within one Auto-Ping attempt.
- [x] No retry is scheduled at or after the next Schedule Milestone, configured-schedule-timezone date change, or schedule/timezone reconfiguration.
- [x] Restart before a pending retry honors the persisted retry time; restart after it is due retries immediately when the cycle is still active.
- [x] Restart does not repeat a terminal failure for the same milestone.
- [x] Status and diagnostics expose the active milestone, next retry time, retry count, and terminal failure class without leaking secrets.
