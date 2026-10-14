# Quota Auto-Ping

This context defines reset-aligned Auto-Ping for eligible Codex and Google Antigravity credentials. Each provider retains its own quota semantics, and Auto-Ping does not change upstream quota policy.

## Language

**Supported Credential**:
A CLIProxyAPI-managed authentication record for a provider handled by this plugin: Codex or Antigravity.
_Avoid_: Account, auth file, token

**Codex Credential**:
A Supported Credential representing one upstream Codex identity.
_Avoid_: Codex account, Codex auth file

**Antigravity Credential**:
A Supported Credential representing one upstream Google Antigravity identity.
_Avoid_: Google account, Antigravity auth file

**Eligible Credential**:
A Supported Credential that is not explicitly excluded and is neither disabled nor revoked. Temporary unavailability or a prior failure does not permanently remove eligibility.
_Avoid_: Every account, scheduler candidate

**Quota Window**:
A provider-defined, time-bounded usage allowance with its own reset time.
_Avoid_: Limit bucket, shared reset

**Codex Five-Hour Window**:
The Codex rolling Quota Window whose declared duration is 18,000 seconds.
_Avoid_: Long window, weekly quota, monthly quota

**Antigravity Model Family**:
An Antigravity quota category shared by models with the same capacity semantics. Gemini and Claude/GPT are separate Antigravity Model Families.
_Avoid_: Model type, provider

**Antigravity Five-Hour Window**:
The independently resetting five-hour Quota Window for one Antigravity Model Family. Antigravity weekly windows are not Auto-Ping targets.
_Avoid_: Shared Antigravity window, weekly target

**Target Model**:
The specific inference model used for one Auto-Ping Target.
_Avoid_: Model family, fallback candidate

**Auto-Ping Target**:
The independently scheduled combination of one Eligible Credential, one provider Quota Window, and one Target Model.
_Avoid_: Credential schedule, shared provider timer

**Schedule Milestone**:
A configured daily wall-clock time that bootstraps an unanchored Auto-Ping Target or begins a fresh Attempt Cycle after a terminal failure. An anchored Antigravity target does not ping merely because a Schedule Milestone elapsed.
_Avoid_: Cron tick, timer trigger, quota reset

**Attempt Cycle**:
The Auto-Ping attempts for one target until success or a terminal failure. A terminal target becomes eligible for a fresh Attempt Cycle at the next Schedule Milestone.
_Avoid_: Retry queue, permanent block

**Schedule Catch-Up**:
Bootstrap or terminal recovery performed after startup when the applicable Schedule Milestone has already elapsed.
_Avoid_: Replay, backfill

**Auto-Ping**:
A minimal real inference request sent after an Auto-Ping Target's selected Quota Window resets. It establishes reset-aligned activity but does not increase, bypass, or extend quota.
_Avoid_: Quota increase, quota bypass, OAuth refresh

**Recoverable Auto-Ping Failure**:
A target-local failure that may succeed without credential or configuration changes, such as network, timeout, rate-limit, stream, or upstream server failure.
_Avoid_: Authentication failure, model/configuration error, business rejection

**Credential Authentication Failure**:
An authentication failure showing that a credential's shared authorization cannot serve its targets. It ends the current Attempt Cycle for every target of that credential.
_Avoid_: Model failure, target-local failure

**Plugin Manifest**:
The canonical declaration of the plugin's identity, release metadata, and default runtime configuration.
_Avoid_: Plugin config, release tag, hardcoded metadata

**Plugin Instance Configuration**:
Host-managed settings for one installed plugin instance. Explicit values override defaults declared by the Plugin Manifest.
_Avoid_: Plugin Manifest, build metadata

**Initial Plugin Configuration**:
The Plugin Instance Configuration established when an installed plugin is first enabled. An omitted Auto-Ping setting inherits the Plugin Manifest default.
_Avoid_: Re-activation, configuration migration

**Explicit Auto-Ping Opt-Out**:
A Plugin Instance Configuration that sets Auto-Ping to disabled for every supported provider. Later activation does not replace this choice.
_Avoid_: Missing setting, temporary scanner stop
