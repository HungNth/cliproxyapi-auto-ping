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
A Schedule Milestone on a given calendar day that has already been dispatched for a Codex Credential.
_Avoid_: Processed reset, handled boundary

**Milestone Catch-Up**:
An Auto-Ping dispatched upon plugin startup for any Eligible Credential that has not yet processed the most recent elapsed Schedule Milestone of the day, provided startup is at least one hour before the next milestone.
_Avoid_: Missed ping, replay, backfill

**Auto-Ping**:
A minimal real Codex inference request sent with one Codex Credential to start its next Five-Hour Window.
_Avoid_: Quota increase, quota bypass, OAuth refresh

**Eligible Credential**:
A Codex Credential that is active, not explicitly excluded, and not blocked due to authentication failure.
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
