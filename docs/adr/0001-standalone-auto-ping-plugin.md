# Keep Codex auto-ping in a standalone plugin

This repository ships the `auto-ping` plugin instead of forking `quota-activation`. The boundary isolates Codex five-hour scheduling from long-window and Antigravity logic, lets both plugins be enabled independently, and keeps existing long-window behavior untouched; the trade-off is a separate configuration and management surface.
