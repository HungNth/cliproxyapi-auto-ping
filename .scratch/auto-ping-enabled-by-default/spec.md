# Auto-Ping Enabled By Default

Status: ready-for-agent

## Problem Statement

The shipped `plugin.yaml` declares `defaults.auto_ping_enabled: true`, but the README, ADR 0003, half the internal test suite, and the operator contract still describe an explicit opt-in with default `false`. Every internal test builds its runtime from a local fixture manifest pinned to `false`, so the drift between the shipped default and the tested default is invisible to `make test`; only the embed test reads the real manifest and it pins the ID and version only.

An operator installing and enabling the plugin therefore cannot tell which behavior to expect, and a future edit can flip the shipped default in either direction without any test failing.

## Solution

Treat `auto_ping_enabled: true` as the intended default and make every artifact agree with it: the manifest field description, the operator documentation, the test fixture, the default assertions, and a drift guard in the embed test that pins the shipped default. Record the reversal of ADR 0003 in a new ADR.

No runtime code changes: the host writes only `enabled` and `store` on install and enable, so an absent `auto_ping_enabled` already inherits the manifest default, and an explicit `false` already overrides it.

## User Stories

1. As a plugin operator, I want a newly installed and enabled plugin to start with Auto-Ping active, so that I do not have to edit the configuration file before it works.
2. As a plugin operator, I want a `false` I wrote myself to survive plugin re-enablement, so that a deliberate opt-out is not silently reversed.
3. As a plugin operator, I want the documented default to match the shipped binary, so that I can predict behavior from the README.
4. As a Management Center user, I want the `auto_ping_enabled` field description and default hint to describe the real behavior, so that "opt-in" is not displayed for a field that is on by default.
5. As a plugin maintainer, I want a test that fails when the shipped manifest default drifts, so that documentation and default cannot diverge again.
6. As a plugin maintainer, I want the domain glossary to name the first-enable configuration and the persistent opt-out, so that "activation" and "user disabled it" are distinguishable in later discussions.
7. As a future maintainer, I want the reversal of the explicit opt-in decision recorded, so that nobody reinstates it from ADR 0003 without revisiting the trade-off.

## Implementation Decisions

- The intended default for `auto_ping_enabled` is `true`. The shipped `plugin.yaml` already declares it; the default is the manifest default and stays there.
- The manual-edit requirement is removed. Host-owned `enabled: true` remains the only enablement action an operator must perform.
- The plugin-owned switch is retained as a persistent opt-out, not as an opt-in.
- An absent key inherits the manifest default `true`. An explicit `false` overrides it and is preserved by ordinary re-enablement.
- Replacing the whole Plugin Instance Configuration without the key restores the default; that is the operator deleting their own opt-out.
- Existing installations are not migrated. A stored or absent `false` is never rewritten to `true`.
- No new host contract is required and no host-side seeding of plugin-owned defaults is added.
- The manifest field description changes from "Explicit opt-in for background Codex inference requests." to wording describing the opt-out.
- The internal test fixture manifest is aligned with the shipped manifest so tests exercise the shipped default.
- The embed test asserts the shipped default, closing the drift seam.
- The domain glossary gains the terms **Initial Plugin Configuration** and **Explicit Auto-Ping Opt-Out**.
- ADR 0007 supersedes ADR 0003. ADR 0003 and the completed `.scratch/embedded-plugin-manifest` spec stay unchanged as historical record.

## Testing Decisions

- Tests assert consumer-visible behavior: the default reported through registration, the configuration an omitted key resolves to, and whether the scanner sends requests.
- The default is asserted against the runtime built from the test fixture, and separately against the embedded `plugin.yaml` through the existing embed seam.
- The disabled path keeps its existing test, which must now set `auto_ping_enabled: false` explicitly because omission no longer means disabled.
- Explicit-override precedence keeps its existing test, with the omitted case asserting `true` and the explicit `false` case asserting `false`.
- No test asserts source text or the fixture literals beyond what is needed to observe the default.

## Out of Scope

- Migrating or rewriting existing installations.
- Adding host support for persisting plugin defaults into the host configuration file.
- Any change to the scanner, transports, state store, or management API.
- Changing other manifest defaults.
- Shipping `plugin.yaml` as a runtime companion file.

## Further Notes

- The host writes plugin instance configuration by cloning its preserved YAML subtree and setting only `enabled` and `store`, so install and enable never touch `auto_ping_enabled`.
- The plugin persists no configuration; the default is re-resolved on every `plugin.register` / `plugin.reconfigure`.
