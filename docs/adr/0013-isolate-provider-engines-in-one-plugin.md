# Isolate provider engines in one Auto-Ping plugin

Status: accepted (supersedes ADR-0001)

Auto-Ping supports Codex and Google Antigravity through one plugin lifecycle, global enablement setting, exclusion list, and management surface, while each provider keeps separate credential parsing, quota observation, request construction, scheduling targets, and failure state. A generic provider engine would hide incompatible protocols and quota-window cardinalities; a second plugin would duplicate the shared operator surface.
