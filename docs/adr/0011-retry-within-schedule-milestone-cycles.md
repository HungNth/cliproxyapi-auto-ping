# Retry within Schedule Milestone cycles

Status: accepted

Every Schedule Milestone starts a new Milestone Attempt Cycle for each Eligible Credential, regardless of temporary availability, cooldown, or prior authentication failure; explicit plugin opt-out, credential exclusion, disabled, and revoked states still prevent Auto-Ping.

Recoverable failures have no attempt-count cap and retry after the later of `retry_cooldown` (default `1m`) and a valid upstream `Retry-After`, ending at whichever occurs first: success, the next milestone, a configured-timezone date change, or schedule/timezone reconfiguration. Authentication, model, and business failures end the current cycle without retry, and the next Schedule Milestone starts fresh with no carry-over.

Startup catches up only the latest elapsed milestone of the current calendar day in the configured schedule timezone, missed milestones coalesce to the latest one, and a successful manual ping explicitly marked processed satisfies the milestone. Reconfiguration cancels old automatic work and waits for its scanner to exit before starting the replacement schedule; same-credential requests never overlap.
