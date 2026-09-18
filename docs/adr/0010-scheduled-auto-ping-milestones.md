# Scheduled auto-ping milestones

Auto-ping triggers at configured daily wall-clock milestones (`05:00`, `10:00`, `15:00`, `20:00` by default) instead of continuously polling dynamic upstream `reset_at` observations. A fixed schedule eliminates background usage API traffic and payload parsing fragility, while startup catch-up ensures quota is ready unless the next milestone is less than 1 hour away.
