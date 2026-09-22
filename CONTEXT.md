# Codex Quota Auto-Ping

This context defines scheduled Auto-Ping for Eligible Credentials at daily Schedule Milestones, without quota polling or changes to upstream quota policy.

## Language

**Codex Credential**:
A CLIProxyAPI-managed authentication record representing one upstream Codex account for scheduled Auto-Ping.
_Avoid_: Account, auth file, token when referring to the managed record

**Five-Hour Window**:
A Codex rolling session quota window whose declared duration is 18,000 seconds.
_Avoid_: Long window, weekly quota, monthly quota

**Schedule Milestone**:
A configured daily wall-clock time (such as `05:00`) at which Auto-Ping triggers for all Eligible Credentials.
_Avoid_: Cron tick, timer trigger, reset boundary

**Processed Milestone**:
A Schedule Milestone on a given calendar day whose Auto-Ping completed successfully and was recorded for a Codex Credential.
_Avoid_: Dispatched milestone, processed reset, handled boundary

**Milestone Attempt Cycle**:
All Auto-Ping attempts for one Codex Credential and one Schedule Milestone, ending at whichever occurs first: success, the next Schedule Milestone, midnight in the configured timezone, or a schedule/timezone configuration change.
_Avoid_: Retry queue, pending reset

**Milestone Catch-Up**:
An Auto-Ping dispatched upon plugin startup for any Eligible Credential that has not yet processed the most recent elapsed Schedule Milestone of the current calendar day in the configured schedule timezone.
_Avoid_: Missed ping, replay, backfill

**Auto-Ping**:
A minimal real Codex inference request sent with one Codex Credential to start its next Five-Hour Window.
_Avoid_: Quota increase, quota bypass, OAuth refresh

**Milestone Retry**:
A repeated Auto-Ping within the current Milestone Attempt Cycle after a Recoverable Auto-Ping Failure.
_Avoid_: New milestone, catch-up ping

**Recoverable Auto-Ping Failure**:
An Auto-Ping failure that may succeed without credential or configuration changes, such as temporary credential access, network, timeout, rate-limit, stream, or upstream server failure.
_Avoid_: Authentication failure, model/configuration error, business rejection

**Eligible Credential**:
A Codex Credential that is not explicitly excluded and is neither disabled nor revoked. Temporary availability, retry, and prior authentication failure states do not remove eligibility at a later Schedule Milestone.
_Avoid_: Every account, scheduler candidate

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
A Plugin Instance Configuration that sets Auto-Ping to disabled. Later activation does not replace this choice.
_Avoid_: Missing setting, temporary scanner stop
