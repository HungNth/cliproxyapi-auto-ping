# 02: Make Scheduler Reconfiguration an Atomic Handoff

**Parent:** `../spec.md`

**What to build:** Make schedule and timezone reconfiguration cancel old automatic work, wait for the old scanner and dispatch to exit, and only then activate the replacement schedule, so an old loop cannot overlap the new loop or cause a due milestone to be skipped.

**Blocked by:** None (can start immediately)

**Status:** resolved

**Testing seam:** Reconfigure through the plugin lifecycle boundary with a fake Host and synthetic time while the old timer, queued work, or an automatic request is active.

**Demo path:** Hold an automatic request open, reconfigure to a schedule whose milestone becomes due during the handoff, release cancellation, and show exactly one dispatch under the new configuration.

- [x] Reconfiguration immediately cancels the old automatic scheduler context and its automatic in-flight request.
- [x] The replacement scanner does not start until the canceled scanner and automatic dispatch have fully exited.
- [x] Scheduler handoff does not depend on a dispatch guard returning a transient busy error.
- [x] Queued credentials from the canceled configuration never dispatch after the handoff.
- [x] A milestone that becomes due during the drain is evaluated under the new schedule and timezone after the handoff.
- [x] The caller context bounds the drain operation.
- [x] If the caller context ends before drain completes, reconfiguration returns a failure and does not start overlapping schedulers.
- [x] Manual requests remain independently serialized per credential and are not mistaken for old automatic schedule work.
- [x] Shutdown remains bounded and idempotent after any successful or failed handoff.
