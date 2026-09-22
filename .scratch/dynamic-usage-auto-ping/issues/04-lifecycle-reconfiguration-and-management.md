# Issue 04: Lifecycle Reconfiguration and Management Routes

Status: ready-for-agent
Parent: `.scratch/dynamic-usage-auto-ping/spec.md`

## Required Behavior

Update plugin lifecycle handling and management endpoints (`/status`, `/diagnostics`, `/ping`) to reflect dynamic usage-anchored scheduling. 
Retain existing manual-ping semantics: an unmarked `/ping` executes targeted inference but does NOT advance or anchor the dynamic cycle. Only when `mark_cycle_processed: true` is explicitly provided, a successful ping initiates quota observation to anchor the subsequent dynamic trigger cycle, preserving strict per-credential concurrency locks.
Dynamic reconfiguration cancels active waiting timers and recalculates targets from current state. Clean shutdown terminates dynamic waiting timers without orphaned goroutines.

## Acceptance Criteria

- [ ] `/status` payload exposes per-credential `observed_reset_at` and `target_trigger_at`.
- [ ] `/diagnostics` reports `/wham/usage` integration and dynamic trigger status.
- [ ] An unmarked manual `/ping` does NOT update `observed_reset_at` or `target_trigger_at`.
- [ ] A manual `/ping` with `mark_cycle_processed: true` updates `target_trigger_at` only after verified success and persisted state.
- [ ] Dynamic reconfiguration halts running timers and resumes with new configuration without race conditions.
- [ ] Clean shutdown terminates dynamic waiting timers cleanly.

## Testing Seam

Management and lifecycle integration tests in `internal/autoping/management_test.go` and `internal/autoping/runtime_test.go`.
