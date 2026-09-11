# CLIProxyAPI Codex 5h Auto-Ping

A native [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) plugin that observes each Codex OAuth credential and sends one minimal, credential-targeted inference request when its rolling five-hour window is inactive.

> Auto-Ping consumes a small amount of real Codex quota. It does not increase, reset, bypass, or create quota and does not change OpenAI subscription or rate-limit policy.

## Behavior

For every eligible Codex credential, the plugin:

1. reads the credential through `host.auth.list` and `host.auth.get`;
2. fetches `https://chatgpt.com/backend-api/wham/usage` through `host.http.do`;
3. identifies the five-hour window by `limit_window_seconds: 18000`, not by primary/secondary position;
4. compares consecutive observations to distinguish an inactive sliding window from a window started by normal user traffic;
5. sends a tiny streaming request directly to `https://chatgpt.com/backend-api/codex/responses` with that credential;
6. persists the processed reset boundary atomically so restarts do not normally duplicate the ping.

A successful request is recorded only after a valid Codex response stream completes. If the process crashes after upstream success but before the state write, one rare duplicate is possible; pre-marking the cycle was rejected because it could silently lose the activation.

## Relationship to quota-activation

| Plugin | Purpose |
| --- | --- |
| `quota-activation` | Existing long-window Codex/Antigravity activation |
| `auto-ping` | Codex rolling five-hour window activation only |

The plugins are independent and may be enabled together. This plugin does not modify `quota-activation` behavior or state.

## Configuration

Auto-Ping has a second explicit safety switch. Both the host-owned `enabled` and plugin-owned `auto_ping_enabled` must be true.

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    auto-ping:
      enabled: true
      priority: 1

      # Explicit opt-in. Default: false.
      auto_ping_enabled: true

      scan_interval: "1m"
      activation_delay: "5s"
      retry_cooldown: "15m"
      max_concurrency: 1
      request_timeout: "60s"

      prompt: "ping"
      max_output_tokens: 1

      # auto tries candidates in order and advances only for a model-specific error.
      model: "auto"
      model_candidates:
        - "gpt-5.5"
        - "gpt-5.6-luna"

      transport: "direct_http"
      scheduler_boost_fallback: true

      # Empty means every otherwise eligible Codex credential.
      exclude_credentials: []

      state_path: "auto-ping/state.json"
```

An explicit model disables model fallback:

```yaml
model: "gpt-5.5"
```

### Defaults

| Field | Default | Meaning |
| --- | --- | --- |
| `auto_ping_enabled` | `false` | Prevents unexpected inference requests after installation |
| `scan_interval` | `1m` | Quota observation frequency |
| `activation_delay` | `5s` | Delay after a fixed reset boundary |
| `retry_cooldown` | `15m` | Delay after retryable activation failure |
| `max_concurrency` | `1` | Maximum credentials processed concurrently |
| `request_timeout` | `60s` | Quota/inference operation timeout |
| `prompt` | `ping` | Minimal user input |
| `max_output_tokens` | `1` | Maximum generated tokens |
| `model` | `auto` | Uses `model_candidates` |
| `transport` | `direct_http` | Guarantees the intended credential is used |
| `scheduler_boost_fallback` | `true` | Fallback only for host/transport failures |
| `state_path` | `auto-ping/state.json` | Persistent state location |

## Eligibility and failure handling

The scanner skips credentials that are disabled, unavailable, revoked, excluded, non-Codex, missing a five-hour window, cooling down, or already processed for the detected boundary.

- Quota fetch failure: skip and retry on the next scan; never guess that a reset occurred.
- Network/stream/temporary upstream failure: retry after `retry_cooldown`.
- Invalid authentication: block retries until CLIProxyAPI reports that the credential changed or refreshed.
- Unsupported auto-selected model: try the next configured candidate once.
- Explicit model failure: do not silently change models.
- Direct business/authentication errors: never invoke scheduler fallback.

`scheduler_boost_fallback` temporarily raises the target credential's priority, adds a one-time nonce, and accepts success only if the plugin scheduler confirms that CLIProxyAPI selected the intended credential. The original priority is restored from the latest credential document so a concurrent token refresh is not overwritten.

## Management API

All routes are registered under `/v0/management` and therefore require CLIProxyAPI management authentication. The plugin exposes no unauthenticated resource page.

```text
GET  /v0/management/auto-ping/status
GET  /v0/management/auto-ping/diagnostics
POST /v0/management/auto-ping/ping
```

Manual ping body:

```json
{
  "credential_id": "codex-account-a",
  "model": "gpt-5.5",
  "mark_cycle_processed": false
}
```

Manual pings do not alter the processed reset boundary unless `mark_cycle_processed` is true. When true, the current observation must already be eligible for automatic activation.

Status and diagnostics expose credential IDs, reset timestamps, decisions, counters, selected model, transport, cooldown, and sanitized errors. Access tokens, refresh tokens, authorization headers, cookies, and raw credential JSON are never persisted or returned.

## Build

Go 1.26+ and a C compiler are required because CLIProxyAPI plugins use `-buildmode=c-shared`.

```bash
make test
make build
```

Equivalent command on Linux:

```bash
CGO_ENABLED=1 go build -trimpath -buildmode=c-shared -o dist/auto-ping.so .
```

Extensions are `.so` on Linux, `.dylib` on macOS, and `.dll` on Windows. Install the library in a CLIProxyAPI discovery path, for example:

```text
plugins/linux/amd64/auto-ping.so
plugins/windows/amd64/auto-ping.dll
plugins/darwin/arm64/auto-ping.dylib
```

Then enable the plugin configuration and restart or reload CLIProxyAPI. Confirm `registered: true` and `effective_enabled: true` through `GET /v0/management/plugins`.

GitHub Actions builds Linux, macOS, and Windows release assets for amd64 and arm64. Release ZIP files contain `auto-ping.<ext>` at the archive root and are accompanied by `checksums.txt`, matching CLIProxyAPI plugin-store installation format.

## Security

The plugin runs in-process and can read CLIProxyAPI-managed Codex credentials. Install only binaries you trust. Secrets are held only long enough to issue quota and inference requests through host callbacks; they are not written to plugin state or logs.

## References

- [CLIProxyAPI plugin development](https://help.router-for.me/plugin/development.html)
- [KKKKeybird/cpa-codex-auto-ping](https://github.com/KKKKeybird/cpa-codex-auto-ping) — basic timer/plugin ABI reference
- [Cody292/quota-activation](https://github.com/Cody292/quota-activation) — credential targeting, state, and fallback reference
- [decolua/9router](https://github.com/decolua/9router) — Codex usage and sliding-window behavior reference

## License

MIT — see [LICENSE](LICENSE).
