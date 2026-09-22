# Issue 03: Version 3 State Store Cutover and Persistence

Status: ready-for-agent
Parent: `.scratch/dynamic-usage-auto-ping/spec.md`

## Required Behavior

Update `internal/autoping/state.go` to support state schema `version: 3`. Strictly reject existing `version: 1` and `version: 2` state documents with the explicit actionable error `unsupported state schema version: please remove stale state file`. Do not add compatibility migration layers or silently reset operator data. Record per-credential `observed_reset_at`, `target_trigger_at`, `last_processed_reset_at`, and attempt/success counters. Restore persisted schedules upon runtime startup without duplicate initialization.

## Acceptance Criteria

- [ ] Version 1 and version 2 state documents return an explicit error containing `unsupported state schema version: please remove stale state file` on load.
- [ ] Stale state files are not silently overwritten, reset, or migrated.
- [ ] Version 3 documents round-trip `observed_reset_at`, `target_trigger_at`, and `last_processed_reset_at` losslessly.
- [ ] Runtime reloads active version 3 targets without duplicate startup queries.

## Testing Seam

Unit tests in `internal/autoping/state_test.go` verifying explicit rejection of version 1 and version 2 documents and lossless round-trip of version 3 documents.
