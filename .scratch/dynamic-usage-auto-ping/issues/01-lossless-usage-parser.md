# Issue 01: Lossless Upstream Usage Parser

Status: ready-for-agent
Parent: `.scratch/dynamic-usage-auto-ping/spec.md`

## Required Behavior

Implement wire payload decoding for `GET https://chatgpt.com/backend-api/wham/usage`. Locate the 5-hour quota window (`limit_window_seconds == 18000`) only at `rate_limit`, `rate_limits`, `rate_limits_by_limit_id.codex`, the root mapping, or their direct child window objects. Parse `reset_at` losslessly using `json.Number` supporting integer epoch seconds (e.g. `1790084449`), milliseconds (`>= 1_000_000_000_000`), and RFC3339 strings. Float values, trailing JSON, unrelated recursive matches, and ambiguous multiple matches must be rejected.

## Acceptance Criteria

- [ ] `"reset_at": 1790084449` decodes cleanly to `time.Unix(1790084449, 0).UTC()`.
- [ ] Millisecond timestamps (`>= 1_000_000_000_000`) decode via `time.UnixMilli`.
- [ ] RFC3339 string timestamps continue to decode accurately.
- [ ] Float values (e.g. `1790084449.123`) return `ErrInvalidUsage`.
- [ ] Missing `limit_window_seconds: 18000` returns `ErrNoFiveHourWindow`.
- [ ] Trailing JSON and multiple matching windows in one candidate return `ErrInvalidUsage`.

## Testing Seam

Unit tests in `internal/autoping/usage_test.go` verifying lossless integer parsing, float rejection, and candidate structure searches.
