# Confirm window inactivity before auto-ping

The scheduler uses consecutive Codex usage observations to distinguish an inactive sliding `reset_at` from a window already started by external traffic. It accepts up to one scan interval of delay to avoid duplicate inference requests and quota waste; literal `now >= reset_at` and first-slide triggers were rejected because Codex may advance `reset_at` before the plugin acts.
