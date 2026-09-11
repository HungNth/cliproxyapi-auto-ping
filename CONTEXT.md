# Codex Quota Auto-Ping

This context defines how the plugin observes and activates Codex rolling five-hour quota windows without changing or bypassing upstream quota policy.

## Language

**Codex Credential**:
A CLIProxyAPI-managed authentication record representing one upstream Codex account for quota observation and auto-ping.
_Avoid_: Account, auth file, token when referring to the managed record

**Five-Hour Window**:
A Codex rolling session quota window whose declared duration is 18,000 seconds.
_Avoid_: Long window, weekly quota, monthly quota

**Reset Boundary**:
The upstream `reset_at` timestamp associated with a Five-Hour Window. It is an observation from Codex, not a command that resets quota.
_Avoid_: Forced reset, quota refresh

**Window Observation**:
A quota snapshot for one Codex Credential, including its Reset Boundary and usage state at a specific observation time.
_Avoid_: Ping state, timer tick

**Auto-Ping**:
A minimal real Codex inference request sent with one Codex Credential to start its next Five-Hour Window.
_Avoid_: Quota increase, quota bypass, OAuth refresh

**External Activation**:
A Five-Hour Window started by normal traffic before Auto-Ping acts.
_Avoid_: Auto-Ping success

**Eligible Credential**:
A Codex Credential that is active, not explicitly excluded, and has a trustworthy Five-Hour Window observation requiring activation.
_Avoid_: Every account, scheduler candidate

**Processed Reset Boundary**:
A Reset Boundary already handled by a successful Auto-Ping or confirmed External Activation and therefore not eligible again.
_Avoid_: Attempted reset, failed ping
