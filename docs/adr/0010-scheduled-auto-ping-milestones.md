# Scheduled auto-ping milestones

Status: accepted; catch-up and retry policy superseded by ADR-0011

Auto-ping triggers at configured daily wall-clock milestones (`05:00`, `10:00`, `15:00`, `20:00` by default) instead of continuously polling dynamic upstream `reset_at` observations. A fixed schedule eliminates background usage API traffic and payload parsing fragility.
