# CLIProxyAPI Codex 5h Auto-Ping

A native [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) plugin that sends one minimal, credential-targeted Codex inference request at each configured daily milestone.

> Auto-Ping consumes a small amount of real Codex quota. It does not increase, reset, bypass, or create quota and does not change OpenAI subscription or rate-limit policy.

## Behavior

For every eligible Codex credential, the plugin:

1. waits for the next daily milestone in the configured timezone (`05:00`, `10:00`, `15:00`, `20:00` by default);
2. discovers credentials through `host.auth.list` and reads their material through `host.auth.get`, without pre-flight quota polling;
3. sends a tiny streaming request directly to `https://chatgpt.com/backend-api/codex/responses` with each eligible credential, bounded by `max_concurrency`;
4. waits for any manual ping already using that credential, then rechecks whether the milestone was processed;
5. records each credential's successful milestone atomically so restarts do not normally duplicate the ping.

A successful request is recorded only after a valid Codex response stream completes. If the process crashes after upstream success but before the state write, one rare duplicate is possible; pre-marking the cycle was rejected because it could silently lose the activation.

While the plugin remains running, milestones reached during a slow batch or retry remain pending rather than being skipped. Requests may start later than the milestone when the worker pool is busy. A temporary credential-list failure keeps the milestone pending and retries discovery after `retry_cooldown`.

On startup, enablement, or reconfiguration, catch-up considers only today's most recent elapsed milestone in the configured schedule timezone and dispatches unprocessed eligible credentials immediately. Before today's first milestone, it waits instead. Reconfiguration cancels the old timer and queued work, waits for any in-flight scanner execution to exit, and then activates the replacement schedule without scheduler overlap.

The plugins are independent and may be enabled together. This plugin does not modify `quota-activation` behavior or state.

## Configuration

Auto-Ping has a dedicated opt-out switch. The plugin starts with `auto_ping_disabled: false`, so background Auto-Ping is active as soon as the host-owned `enabled` switch is set without requiring any manual edit. In the Management Center Web UI, the toggle switch remains OFF by default.

```yaml
plugins:
    enabled: true
    dir: "plugins"
    configs:
        cliproxyapi-auto-ping:
            enabled: true
            priority: 1

            # Default: false. Set to true to opt out of background requests.
            auto_ping_disabled: false

            schedule:
                - "05:00"
                - "10:00"
                - "15:00"
                - "20:00"
            timezone: "Local"
            retry_cooldown: "1m"
            max_concurrency: 1
            request_timeout: "60s"

            prompt: "ping"

            # auto tries candidates in order and advances only for a model-specific error.
            model: "auto"
            model_candidates:
                - "gpt-5.5"
                - "gpt-5.6-luna"

            transport: "direct_http"
            scheduler_boost_fallback: true

            # Empty means every otherwise eligible Codex credential.
            exclude_credentials: []

            state_path: "cliproxyapi-auto-ping/state.json"
```

An explicit model disables model fallback:

```yaml
model: "gpt-5.5"
```

An `auto_ping_disabled: true` you wrote yourself is preserved: re-enabling the plugin keeps the scheduler stopped. Removing the key restores the default `false`.

### Defaults

| Field                      | Default                            | Meaning                                                    |
| -------------------------- | ---------------------------------- | ---------------------------------------------------------- |
| `auto_ping_disabled`       | `false`                            | Set to `true` to opt out of background inference requests  |
| `schedule`                 | `["05:00", "10:00", "15:00", "20:00"]` | Daily auto-ping milestone times in 24-hour format      |
| `timezone`                 | `Local`                            | Timezone for daily schedule milestones                     |
| `retry_cooldown`           | `1m`                               | Delay before retrying after an activation failure          |
| `max_concurrency`          | `1`                                | Maximum credentials processed concurrently                 |
| `request_timeout`          | `60s`                              | Inference operation timeout                               |
| `prompt`                   | `ping`                             | Minimal user input                                         |
| `model`                    | `auto`                             | Uses `model_candidates`                                    |
| `transport`                | `direct_http`                      | Guarantees the intended credential is used                 |
| `scheduler_boost_fallback` | `true`                             | Fallback only for host/transport failures                  |
| `state_path`               | `cliproxyapi-auto-ping/state.json` | Persistent state location                                  |

> **Upgrade notice (State Schema v2):** This release performs an intentionally destructive state schema cutover. Existing version 1 state files are rejected on startup and configuration. Operators upgrading from an earlier version must remove the existing `state.json` file or point `state_path` to a new location before enabling this release.
## Eligibility and failure handling

The scheduler skips disabled, revoked, explicitly excluded, and non-Codex credentials. The host's temporary `unavailable` flag alone does not exclude a credential, and prior authentication failure does not suppress a subsequent milestone. A successful milestone is deduplicated per credential, not by the global milestone marker.

- Credential material read failure, network/stream failure, rate limit (HTTP 429), or temporary upstream failure: retry after the later of `retry_cooldown` and upstream `Retry-After` without a fixed attempt cap until success, the next milestone, midnight in the configured schedule timezone, or reconfiguration.
- Invalid authentication, model failure, or business rejection: terminal for the current cycle with no minute retries, but a fresh attempt cycle begins at the next scheduled milestone.
- Unsupported auto-selected model: try the next configured candidate once.
- Explicit model failure: do not silently change models.
- Direct business/authentication errors: never invoke scheduler fallback.

Being scheduled guarantees an attempt under these eligibility rules, not upstream success. An active cycle continues retrying recoverable errors until its cycle boundary (the next milestone, configured-timezone midnight, or reconfiguration), while terminal failures (such as authentication or business rejections) wait for the next scheduled milestone to start fresh.

`scheduler_boost_fallback` temporarily raises the target credential's priority, adds a one-time nonce, and accepts success only if the plugin scheduler confirms that CLIProxyAPI selected the intended credential. The original priority is restored from the latest credential document so a concurrent token refresh is not overwritten.

## Management API

All routes are registered under `/v0/management` and therefore require CLIProxyAPI management authentication. The plugin exposes no unauthenticated resource page.

```text
GET  /v0/management/cliproxyapi-auto-ping/status
GET  /v0/management/cliproxyapi-auto-ping/diagnostics
POST /v0/management/cliproxyapi-auto-ping/ping
```

Manual ping body:

```json
{
    "credential_id": "codex-account-a",
    "model": "gpt-5.5",
    "mark_cycle_processed": false
}
```

Manual pings do not mark a milestone processed unless `mark_cycle_processed` is true. When true, a successful manual ping records the latest elapsed milestone of the current day. An overlapping automatic dispatch rechecks this marker before sending another request.

Status and diagnostics expose credential IDs, milestone history, decisions, counters, selected model, transport, retry timing, and sanitized errors. The global `last_milestone` identifies the last evaluated batch; inspect each credential's `last_processed_milestone` and `last_attempt_status` to confirm success. Access tokens, refresh tokens, authorization headers, cookies, and raw credential JSON are never persisted or returned.

## Build

Go 1.26+ and a C compiler are required because CLIProxyAPI plugins use `-buildmode=c-shared`.

Plugin identity, configuration field descriptors, and runtime defaults come from the canonical `plugin.yaml` at the repository root, embedded into the shared library at build time. The manifest version is authoritative: release tags must match it (`v0.1.0` for version `0.1.0`), and the plugin ID must stay `cliproxyapi-auto-ping` because it names the artifact, host configuration key, and management routes.

```bash
make test
make build
```

Equivalent command on Linux:

```bash
CGO_ENABLED=1 go build -trimpath -buildmode=c-shared -o dist/cliproxyapi-auto-ping.so .
```

Extensions are `.so` on Linux, `.dylib` on macOS, and `.dll` on Windows. Install the library in a CLIProxyAPI discovery path, for example:

```text
plugins/linux/amd64/cliproxyapi-auto-ping.so
plugins/windows/amd64/cliproxyapi-auto-ping.dll
plugins/darwin/arm64/cliproxyapi-auto-ping.dylib
```

Then enable the plugin configuration and restart or reload CLIProxyAPI. Confirm `registered: true` and `effective_enabled: true` through `GET /v0/management/plugins`.

GitHub Actions builds Linux, macOS, and Windows release assets for amd64 and arm64. Release ZIP files are named `cliproxyapi-auto-ping_<version>_<os>_<arch>.zip`, contain `cliproxyapi-auto-ping.<ext>` at the archive root, and are accompanied by `checksums.txt`, matching CLIProxyAPI plugin-store installation format.

## Security

The plugin runs in-process and can read CLIProxyAPI-managed Codex credentials. Install only binaries you trust. Secrets are held only long enough to issue inference requests through host callbacks; they are not written to plugin state or logs.

## References

- [CLIProxyAPI plugin development](https://help.router-for.me/plugin/development.html)
- [Cody292/quota-activation](https://github.com/Cody292/quota-activation)

## License

MIT — see [LICENSE](LICENSE).
