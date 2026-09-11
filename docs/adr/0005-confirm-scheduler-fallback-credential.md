# Confirm the credential selected by scheduler fallback

`scheduler_boost_fallback` runs only after a direct transport or host failure and is successful only when the plugin scheduler confirms that CLIProxyAPI selected the intended Codex Credential. This adds a small session/claim mechanism but prevents a fallback request routed through another credential from being reported as activation of the target.
