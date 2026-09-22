# CLIProxyAPI Codex 5h Auto-Ping

A native [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) plugin that observes each Codex credential's five-hour quota boundary and sends one minimal targeted request after that boundary.

> Auto-Ping consumes a small amount of real Codex quota. It does not increase, reset, bypass, or create quota and does not change OpenAI subscription or rate-limit policy.

## Behavior

For every eligible Codex credential, the plugin:

1. waits until the first configured daily milestone (`05:00` by default) before initial discovery; startup after that milestone catches up immediately;
2. reads `GET https://chatgpt.com/backend-api/wham/usage` and selects the `limit_window_seconds: 18000` window;
3. schedules the credential independently at the observed `reset_at + 30s`; the 30-second margin mitigates boundary timing risk but is not an upstream guarantee;
4. sends a tiny streaming request to `https://chatgpt.com/backend-api/codex/responses`, bounded by `max_concurrency` and serialized with manual pings for the same credential;
5. re-observes usage after success and schedules the next cycle only after `reset_at` advances.

Recoverable usage or inference failures retry after `retry_cooldown`. Authentication, model, and business failures end the current attempt cycle; later configured schedule entries start a fresh cycle. Reconfiguration preserves persisted targets and retry/terminal state while replacing the running scheduler without overlap.

The private usage endpoint can fail, return stale data, or change shape. A stale post-ping observation keeps the credential in stabilization and retries observation without sending another inference request.

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

            # The first entry anchors initial discovery. Later entries bound terminal cycles.
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
| `schedule`                 | `["05:00", "10:00", "15:00", "20:00"]` | First entry anchors discovery; later entries start fresh terminal cycles |
| `timezone`                 | `Local`                            | Timezone for schedule anchors                              |
| `retry_cooldown`           | `1m`                               | Delay before retrying after an activation failure          |
| `max_concurrency`          | `1`                                | Maximum credentials processed concurrently                 |
| `request_timeout`          | `60s`                              | Inference operation timeout                               |
| `prompt`                   | `ping`                             | Minimal user input                                         |
| `model`                    | `auto`                             | Uses `model_candidates`                                    |
| `transport`                | `direct_http`                      | Guarantees the intended credential is used                 |
| `scheduler_boost_fallback` | `true`                             | Fallback only for host/transport failures                  |
| `state_path`               | `cliproxyapi-auto-ping/state.json` | Persistent state location                                  |

> **Upgrade notice (State Schema v3):** Existing version 1 and version 2 state files are rejected with an actionable startup/configuration error. Remove the old `state.json` file or select a new `state_path` before enabling this release.

## Eligibility and failure handling

The scheduler skips disabled, revoked, explicitly excluded, and non-Codex credentials. The host's temporary `unavailable` flag alone does not exclude a credential.

- Credential material, transport, timeout, HTTP 429, or upstream 5xx failure: retry usage observation or inference after `retry_cooldown`.
- Invalid authentication, model failure, business rejection, missing five-hour window, or invalid usage payload: terminal for the current cycle; retry fresh at the next configured schedule anchor.
- Unsupported auto-selected model: try the next configured candidate once.
- Explicit model failure: do not silently change models.
- Direct business/authentication errors: never invoke scheduler fallback.

Scheduling guarantees an attempt under these eligibility rules, not that upstream will activate or reset quota. `/wham/usage` is an observed private API contract, not an authoritative real-time guarantee.

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

Manual pings do not advance the dynamic cycle unless `mark_cycle_processed` is true. When true, a successful manual ping re-observes `/wham/usage` and anchors the next dynamic cycle at `reset_at + 30s` only after upstream reset strictly advances. If usage observation fails or returns a stale reset, the account transitions to `stabilizing` and retries observation without duplicating inference.

Status and diagnostics expose credential IDs, `observed_reset_at`, `target_trigger_at`, `last_processed_reset_at`, decisions, counters, selected model, transport, retry timing, and sanitized errors. Access tokens, refresh tokens, authorization headers, cookies, and raw credential JSON are never persisted or returned.

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

## Installation on Linux

A public installer script is provided for Linux glibc systems (`amd64` and `arm64`). It automatically resolves the latest release asset, verifies its SHA-256 checksum from `checksums.txt`, and atomically installs `cliproxyapi-auto-ping.so` into your existing CLIProxyAPI installation (`~/cliproxyapi/plugins/linux/<arch>/cliproxyapi-auto-ping.so`).

### Prerequisites

- Linux with glibc (Alpine/musl and OpenWrt are unsupported).
- Existing CLIProxyAPI installation at `~/cliproxyapi`.
- Standard tools installed: `bash`, `curl` (or `wget`), `unzip`, and `sha256sum`.

### One-line install or upgrade

```bash
curl -fsSL https://raw.githubusercontent.com/HungNth/cliproxyapi-auto-ping/main/scripts/install/linux.sh | bash
```

Or run from a cloned repository:

```bash
bash scripts/install/linux.sh
```

After installation, restart or reload your CLIProxyAPI instance to load the updated plugin library.

## Security

The plugin runs in-process and can read CLIProxyAPI-managed Codex credentials. Install only binaries you trust. Secrets are held only long enough to issue inference requests through host callbacks; they are not written to plugin state or logs.

## References

- [CLIProxyAPI plugin development](https://help.router-for.me/plugin/development.html)
- [Cody292/quota-activation](https://github.com/Cody292/quota-activation)

## License

MIT — see [LICENSE](LICENSE).
