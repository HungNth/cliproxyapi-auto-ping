# 01: Run one exact-pinned Antigravity target manually

**What to build:** Let an operator manually Auto-Ping one explicit Antigravity Target on one Eligible Credential. The request must run through CLIProxyAPI's provider-aware model execution path, pinned to the exact credential and forced to the Antigravity provider, while recording target-local attempt state without changing Codex manual behavior.

**Blocked by:** None (can start immediately).

**Status:** resolved

**Parent specification:** Google Antigravity Reset-Aligned Auto-Ping Specification.

**Testing seam:** Exercise the Runtime lifecycle and management handler through the existing fake Host and synthetic clock. Capture the host model-execution request and inspect externally visible response and persisted state.

**Demo:** Submit a manual ping for an Antigravity Credential and one fixed Target Model; show exact `auth_id` and Antigravity provider pinning, a successful per-target result, nested version-3 target state, and an unchanged Codex manual ping.

- [x] Antigravity Credentials are recognized by normalized provider/type identity and use the existing exclusion, disabled, revoked, and temporary-unavailability eligibility rules.
- [x] Manual ping accepts either `gemini-3.8-flash-high` or `claude-sonnet-5-5-high` for an Antigravity Credential and rejects any other explicit model with a clear client error.
- [x] The host model-execution contract carries both the exact credential ID and `forced_provider: antigravity`, uses non-streaming OpenAI Responses translation, applies the shared prompt, disables storage, and omits output-token limit fields.
- [x] Antigravity activation never uses direct Google HTTP, priority mutation, scheduler boost, scheduler nonce confirmation, or fallback to another credential.
- [x] Host execution errors fail closed and are classified and sanitized without exposing credential material.
- [x] State schema remains version 3; existing Codex records load unchanged, while an Antigravity credential may persist optional nested target attempt state.
- [x] Target state records the selected model, transport, attempt timestamp, outcome, counters, and sanitized error independently from credential-level Codex schedule fields.
- [x] Automatic and manual work for the same credential retains the existing no-overlap conflict behavior.
- [x] Existing Codex manual requests, responses, payloads, transports, counters, and state remain unchanged, including omission of unsupported output-token parameters.

## Answer

Delivered exact-pinned Antigravity manual Auto-Ping:
1. Added `ForcedProvider` and `AuthID` to `ModelExecuteRequest` in the host callback interface.
2. Integrated `isAntigravity` provider classification.
3. Manual ping for Antigravity credentials validates explicit target models (`gemini-3.8-flash-high` or `claude-sonnet-5-5-high`) and rejects unsupported models.
4. Executed non-streaming via `host.model.execute` pinned to `forced_provider: antigravity` and the exact credential ID.
5. Persisted target-local attempt outcomes to state schema v3 under `targets`.
6. Verified with unit tests covering exact pinning, model validation, error classification, and Codex non-regression.
