# Pin Antigravity execution through the host

Status: accepted

Antigravity Auto-Ping requires CLIProxyAPI v7.3.3 or newer and executes through `host.model.execute` with both `forced_provider: antigravity` and the exact `auth_id`. This keeps OAuth refresh, project resolution, translation, signatures, and cooldown behavior in the host and fails closed instead of routing through another credential; direct Google activation, priority boosting, and scheduler-nonce certification are not used.
