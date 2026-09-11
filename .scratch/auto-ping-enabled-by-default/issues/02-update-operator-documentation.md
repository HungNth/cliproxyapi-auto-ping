# 02: Update the operator contract and record the decision

**What to build:** Correct the operator-facing documentation so the documented default matches the shipped binary, and record the reversal of the explicit opt-in decision.

**Blocked by:** none

**Status:** ready-for-agent

**Parent specification:** Auto-Ping Enabled By Default.

**Demo:** Read the README configuration section and the defaults table and confirm they state `true`, then read ADR 0007 and confirm it supersedes ADR 0003.

- [ ] The README configuration example comment no longer states `Default: false.` and no longer calls the field an explicit opt-in.
- [ ] The README defaults table lists `auto_ping_enabled` as `true` with a meaning that describes what disabling it prevents.
- [ ] The README states that a `false` written by the operator is preserved across plugin re-enablement.
- [ ] The README configuration section states that install and enable require no manual edit and that the host-owned `enabled` switch is the only required action.
- [ ] ADR 0007 records the default-enabled decision, marks ADR 0003 superseded, and names the rejected alternatives.
- [ ] The domain glossary keeps the **Initial Plugin Configuration** and **Explicit Auto-Ping Opt-Out** terms, free of implementation detail.
