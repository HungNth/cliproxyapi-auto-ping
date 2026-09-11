# Yêu cầu triển khai Codex 5h Auto-Ping cho CLIProxyAPI `auto-ping` plugin

## 1. Tổng quan

Cần bổ sung chức năng **Codex 5-hour Auto-Ping** cho plugin:

```text
https://github.com/Cody292/quota-activation
```

Plugin hiện tại đã có cơ chế theo dõi quota, lựa chọn credential, gửi activation request và lưu trạng thái activation.

Tuy nhiên, automatic activation hiện tại **cố tình bỏ qua Codex rolling 5-hour quota window** và chủ yếu phục vụ các quota window dài hạn.

Mục tiêu của yêu cầu này là bổ sung một cơ chế tương tự tính năng **Auto-Ping của 9router**:

```text
https://github.com/decolua/9router
```

Ý tưởng cốt lõi:

> Khi Codex 5-hour quota window vừa reset, plugin tự động gửi một request Codex rất nhỏ bằng chính credential đó để bắt đầu ngay 5-hour window tiếp theo.

Việc này giúp tránh trường hợp quota đã reset nhưng vì account không có traffic nên window tiếp theo chỉ bắt đầu khi user thực sự gửi request nhiều giờ sau đó.

---

# 2. Vấn đề cần giải quyết

Codex sử dụng rolling quota window.

Ví dụ một account có 5-hour window:

```text
Current window:

10:00 ─────────────────────────── 15:00
                    5 hours
```

Window reset lúc:

```text
15:00
```

Nếu sau 15:00 không có request nào cho đến 18:00, có thể xảy ra:

```text
15:00 quota reset

15:00
  │
  │ không có request
  │
  │
18:00 user gửi request đầu tiên
  │
  ▼
new window starts

18:00 ─────────────────────────── 23:00
```

Như vậy lịch quota bị trượt:

```text
Expected:

15:00 → 20:00
20:00 → 01:00


Actual without auto-ping:

18:00 → 23:00
23:00 → 04:00
```

Đối với hệ thống có nhiều Codex accounts, điều này làm các quota window trở nên khó dự đoán và không tận dụng được thời gian reset.

---

# 3. Hành vi mong muốn

Sau khi 5-hour window reset:

```text
15:00
  │
  ▼
quota reset
  │
  ▼
quota-activation detects reset
  │
  ▼
send tiny Codex request
  │
  ▼
new 5h window starts immediately
  │
  ▼
next reset ≈ 20:00
```

Ví dụ:

```text
Account A

Current reset_at:
2026-09-11 15:00

15:00
  │
  ▼
Auto Ping
  │
  ▼
Codex inference:
"quota activation ping"
  │
  ▼
success
  │
  ▼
new reset_at ≈ 20:00
```

---

# 4. Mục tiêu

Implementation phải hỗ trợ:

- phát hiện Codex 5-hour quota window;
- lấy chính xác `reset_at`;
- tự động gửi request sau khi window reset;
- sử dụng đúng Codex credential cần ping;
- mỗi quota cycle chỉ ping đúng một lần;
- không ping liên tục nếu request thất bại;
- retry có cooldown;
- hỗ trợ nhiều Codex accounts;
- hoạt động độc lập với long-window activation hiện tại;
- không phá vỡ behavior hiện có;
- lưu state qua restart;
- có logging và diagnostics rõ ràng;
- không hardcode model một cách không cần thiết;
- hạn chế tối đa quota bị tiêu thụ bởi auto-ping.

---

# 5. Không phải mục tiêu

Feature này **không** nhằm:

- tăng quota Codex;
- bypass quota;
- reset quota trước thời gian quy định;
- tạo quota miễn phí;
- bypass OpenAI rate limit;
- thay đổi subscription;
- refresh OAuth đơn thuần;
- fake quota information;
- thay đổi server-side Codex quota policy.

Auto-ping phải là một **Codex model request thật**.

Request này sẽ tiêu thụ một lượng quota nhỏ.

---

# 6. Kiến trúc hiện tại có thể tái sử dụng

Plugin `quota-activation` hiện đã có nhiều thành phần cần thiết.

Không nên viết lại toàn bộ feature từ đầu.

Có thể tái sử dụng:

```text
quota-activation
│
├── credential enumeration
│
├── Codex provider detection
│
├── quota parsing
│
├── PrimaryWindow / SecondaryWindow
│
├── reset_at information
│
├── activation scheduler
│
├── direct_http transport
│
├── scheduler_boost fallback
│
├── OAuth credential handling
│
├── state/history
│
├── concurrency control
│
├── Codex /responses request
│
└── management API / status
```

Hiện tại detector có behavior tương tự:

```go
if cycleWindow == WindowUnknown || cycleWindow == WindowFiveHour {
    continue
}
```

Đối với feature mới, **không được đơn giản xóa behavior này để trộn 5h window với long-window activation**.

Thay vào đó nên tạo một subsystem riêng:

```text
Long Window Activation
        +
Codex 5h Auto-Ping
```

Hai feature phải có thể bật/tắt độc lập.

---

# 7. Đề xuất cấu hình

Nên bổ sung configuration riêng.

Ví dụ:

```yaml
plugins:
    enabled: true
    dir: "plugins"

    configs:
        quota-activation:
            enabled: true

            # Existing long-window activation
            auto_activate: true
            scan_interval: "30"

            # New feature
            codex_auto_ping:
                enabled: true

                # Scan frequency
                scan_interval: "60s"

                # Request behavior
                prompt: "quota activation ping"

                # Model selection
                model: "auto"

                # Retry behavior
                retry_cooldown: "15m"

                # Number of accounts that may be pinged simultaneously
                max_concurrency: 1

                # Delay after reset before attempting ping
                activation_delay: "5s"

                # Request timeout
                request_timeout: "60s"

                # Transport
                transport: "direct_http"

                # Optional fallback
                scheduler_boost_fallback: true
```

---

# 8. Backward compatibility

Không được làm thay đổi behavior của config hiện tại.

Ví dụ người dùng đang có:

```yaml
quota-activation:
    enabled: true
    auto_activate: true
```

sau khi upgrade plugin:

```text
Codex 5h Auto-Ping MUST remain disabled by default
```

Cho đến khi user bật:

```yaml
codex_auto_ping:
    enabled: true
```

Điều này tránh việc plugin bắt đầu tiêu thụ quota ngoài dự kiến sau khi update.

---

# 9. Phát hiện Codex 5-hour window

Plugin cần đọc quota payload của từng Codex credential.

Cần xác định quota window dựa trên duration.

Ví dụ:

```json
{
    "limit_window_seconds": 18000,
    "reset_at": "2026-09-11T15:00:00Z"
}
```

Vì:

```text
18000 seconds
=
300 minutes
=
5 hours
```

nên đây là:

```text
WindowFiveHour
```

Logic ví dụ:

```go
if window.LimitWindowSeconds == 18000 {
    return WindowFiveHour
}
```

Không nên dựa duy nhất vào vị trí:

```text
primary_window
secondary_window
```

vì upstream có thể thay đổi thứ tự.

Nên detect bằng properties của window.

---

# 10. State machine

Mỗi credential cần được quản lý như một state machine độc lập.

Ví dụ:

```text
                    ┌───────────────┐
                    │ WAITING_RESET │
                    └───────┬───────┘
                            │
                  now >= reset_at
                            │
                            ▼
                    ┌───────────────┐
                    │ READY_TO_PING │
                    └───────┬───────┘
                            │
                         request
                            │
                ┌───────────┴───────────┐
                │                       │
              success                 failure
                │                       │
                ▼                       ▼
        ┌──────────────┐        ┌──────────────┐
        │ PINGED       │        │ COOLDOWN     │
        └──────┬───────┘        └──────┬───────┘
               │                       │
        new reset_at             cooldown expired
               │                       │
               └──────────┬────────────┘
                          ▼
                   WAITING_RESET
```

---

# 11. Quy tắc quan trọng: một `reset_at` chỉ ping một lần

Plugin phải lưu một identifier cho cycle.

Tối thiểu:

```text
credential_id
provider
reset_at
```

Ví dụ:

```json
{
    "credential_id": "codex-account-a",
    "provider": "codex",
    "window": "5h",
    "last_pinged_reset_at": "2026-09-11T15:00:00Z",
    "last_ping_status": "success"
}
```

Khi scanner chạy lại:

```text
current reset_at = 2026-09-11T15:00:00Z
last_pinged_reset_at = 2026-09-11T15:00:00Z

=> DO NOT PING
```

---

# 12. Không được đánh dấu success trước khi inference thành công

Không được:

```text
reset detected
   ↓
mark pinged
   ↓
send request
   ↓
request failed
```

Vì lúc đó account sẽ không được retry.

Đúng phải là:

```text
reset detected
   ↓
send request
   ↓
success?
   │
   ├── YES
   │      ↓
   │  mark cycle as pinged
   │
   └── NO
          ↓
       record failure
          ↓
       cooldown
```

---

# 13. Retry và cooldown

Nếu request thất bại, plugin không được retry mỗi scan interval.

Ví dụ:

```yaml
retry_cooldown: "15m"
```

Timeline:

```text
15:00 reset

15:00:05
ping
↓
FAILED

15:01 scanner
→ skip

15:05 scanner
→ skip

15:10 scanner
→ skip

15:15
cooldown expired
↓
retry
```

State có thể lưu:

```json
{
    "last_attempt_at": "2026-09-11T15:00:05Z",
    "last_attempt_status": "failed",
    "next_retry_at": "2026-09-11T15:15:05Z"
}
```

---

# 14. Activation delay

Không nên nhất thiết ping chính xác tại millisecond `reset_at`.

Nên hỗ trợ:

```yaml
activation_delay: "5s"
```

Ví dụ:

```text
reset_at = 15:00:00

earliest ping:
15:00:05
```

Lý do:

- clock skew;
- upstream reset propagation delay;
- quota endpoint chưa cập nhật ngay;
- tránh request đúng ranh giới reset.

---

# 15. Transport

## Preferred transport

Mặc định:

```yaml
transport: "direct_http"
```

Plugin cần sử dụng credential được chọn để gọi trực tiếp Codex upstream.

Ví dụ:

```text
POST https://chatgpt.com/backend-api/codex/responses
```

Lợi ích:

```text
Account A cần ping

        ↓

request chắc chắn dùng Account A
```

thay vì:

```text
Account A cần ping
        ↓
CPA scheduler
        ↓
có thể chọn Account B
```

---

# 16. Scheduler boost fallback

Có thể giữ behavior hiện tại:

```yaml
scheduler_boost_fallback: true
```

Luồng:

```text
direct_http
   │
   ├── success
   │      ↓
   │    done
   │
   └── transport-level failure
          ↓
   scheduler_boost
          ↓
  temporarily prioritize credential
          ↓
       send request
          ↓
     restore priority
```

Fallback không nên được kích hoạt đối với tất cả lỗi.

Ví dụ lỗi upstream rõ ràng như:

```text
quota exhausted
account disabled
invalid authentication
unsupported model
```

không nhất thiết nên fallback scheduler.

---

# 17. Model selection

Không nên hardcode vĩnh viễn:

```yaml
model: "gpt-5.5"
```

Mặc dù 9router có thể dùng một model cụ thể, model Codex có thể thay đổi hoặc bị retire.

Nên hỗ trợ:

```yaml
model: "auto"
```

## `auto` behavior

Logic đề xuất:

```text
get available Codex models
        ↓
filter supported models
        ↓
prefer lightweight model
        ↓
prefer cheapest / lowest quota impact
        ↓
send minimum-cost request
```

Nếu plugin không có model discovery API đáng tin cậy, có thể sử dụng fallback list.

Ví dụ:

```yaml
model_candidates:
    - "gpt-5.5-mini"
    - "gpt-5.5"
```

Hoặc:

```text
auto
 ↓
current configured Codex model
 ↓
fallback model
```

---

# 18. Explicit model configuration

User vẫn phải được phép override:

```yaml
codex_auto_ping:
    model: "gpt-5.5"
```

Khi explicit model được cấu hình:

```text
do not auto-select another model
```

trừ khi có option riêng:

```yaml
fallback_model_on_failure: true
```

---

# 19. Ping request phải cực nhỏ

Ví dụ prompt:

```text
quota activation ping
```

Hoặc:

```text
ping
```

Request nên:

- không yêu cầu reasoning phức tạp;
- không yêu cầu tool use;
- không gửi context lớn;
- không store conversation nếu API hỗ trợ;
- không tạo response dài;
- hạn chế output tối đa;
- sử dụng minimum reasoning nếu protocol hỗ trợ.

Pseudo request:

```json
{
    "model": "gpt-5.5",
    "input": "ping",
    "store": false
}
```

Mục tiêu:

```text
minimum possible quota consumption
```

---

# 20. Không ping khi account không hợp lệ

Auto-ping phải skip nếu credential:

- disabled;
- revoked;
- expired và không refresh được;
- không phải Codex;
- không có quota information;
- không có 5h window;
- bị user disable khỏi auto-ping;
- đang bị cooldown;
- đã ping cycle hiện tại.

Ví dụ log:

```text
[auto-ping] account=codex-a skipped reason=disabled
```

---

# 21. Multiple accounts

Feature phải hỗ trợ nhiều credential độc lập.

Ví dụ:

```text
CLIProxyAPI
│
├── Codex A
│     reset: 10:00
│
├── Codex B
│     reset: 10:42
│
├── Codex C
│     reset: 12:17
│
└── Codex D
      reset: 14:05
```

Kết quả:

```text
10:00 A → auto-ping
10:42 B → auto-ping
12:17 C → auto-ping
14:05 D → auto-ping
```

Window mới:

```text
A → ~15:00
B → ~15:42
C → ~17:17
D → ~19:05
```

Mỗi account có state riêng.

---

# 22. Concurrency control

Nếu nhiều account reset cùng lúc:

```text
10:00:00 Account A
10:00:00 Account B
10:00:00 Account C
```

plugin không nên nhất thiết gửi tất cả request cùng một lúc.

Cấu hình:

```yaml
max_concurrency: 1
```

hoặc:

```yaml
max_concurrency: 2
```

Implementation có thể sử dụng worker/semaphore.

---

# 23. Scan interval

Scanner nên hỗ trợ duration chuẩn:

```yaml
scan_interval: "60s"
```

Thay vì chỉ:

```yaml
scan_interval: "1"
```

Nếu backward compatibility yêu cầu integer, có thể hỗ trợ cả hai.

Ví dụ:

```text
"60s"
"1m"
"5m"
```

Không cần scan quá thường xuyên.

Một phút là đủ cho use case này.

---

# 24. Persistence

State phải được lưu persistent.

Sau restart:

```text
15:00 auto-ping success
15:05 plugin restart
15:06 scanner starts
```

Plugin phải biết:

```text
15:00 cycle already pinged
```

và không ping lại.

State ví dụ:

```json
{
    "version": 1,
    "credentials": {
        "codex-account-a": {
            "five_hour": {
                "last_observed_reset_at": "2026-09-11T20:00:00Z",
                "last_pinged_reset_at": "2026-09-11T15:00:00Z",
                "last_ping_at": "2026-09-11T15:00:07Z",
                "last_ping_status": "success"
            }
        }
    }
}
```

---

# 25. Detection sau khi ping thành công

Sau khi request thành công, quota endpoint có thể trả về:

```text
old reset_at:
15:00
```

hoặc ngay lập tức:

```text
new reset_at:
20:00
```

Plugin không nên dựa vào việc quota endpoint phải update ngay lập tức.

Success criteria chính:

```text
activation inference request succeeded
```

Sau đó:

```text
mark old reset_at as processed
```

Scanner tiếp theo sẽ nhận new reset_at khi upstream cập nhật.

---

# 26. Clock skew

Cần xử lý clock skew nhỏ.

Ví dụ:

```text
local clock: 15:00:03
upstream reset: 15:00:05
```

Có thể sử dụng:

```yaml
activation_delay: "5s"
```

và không coi vài giây lệch là lỗi.

---

# 27. Quota payload tạm thời không khả dụng

Nếu quota API fail:

```text
scanner
  ↓
quota fetch failed
```

Không được suy đoán rằng reset đã xảy ra và ping bừa.

Behavior:

```text
log warning
skip
retry next scan
```

---

# 28. Long-window activation phải giữ nguyên

Existing feature:

```yaml
auto_activate: true
```

vẫn phải xử lý long quota window như hiện tại.

New feature:

```yaml
codex_auto_ping:
    enabled: true
```

xử lý riêng 5h window.

Architecture:

```text
Quota Scanner
│
├── Long Window Detector
│      │
│      └── existing activation logic
│
└── Codex 5h Detector
       │
       └── auto-ping logic
```

---

# 29. Cả hai feature có thể chạy đồng thời

Ví dụ:

```yaml
auto_activate: true

codex_auto_ping:
    enabled: true
```

Kết quả:

```text
5h window
→ Codex Auto-Ping

7d / long window
→ Existing quota activation
```

Không được để cùng một event kích hoạt cả hai request không cần thiết.

---

# 30. Per-account configuration

Nên cân nhắc cho phép exclude account.

Ví dụ:

```yaml
codex_auto_ping:
    enabled: true

    exclude_credentials:
        - codex-test-account
```

Hoặc allow list:

```yaml
include_credentials:
    - codex-account-a
    - codex-account-b
```

Nếu cả hai không cấu hình:

```text
all eligible Codex credentials
```

---

# 31. Manual auto-ping trigger

Nên bổ sung management API cho testing/debugging.

Ví dụ:

```http
POST /v0/management/quota-activation/codex-auto-ping
```

Payload:

```json
{
    "credential_id": "codex-account-a"
}
```

Response:

```json
{
    "success": true,
    "credential_id": "codex-account-a",
    "model": "gpt-5.5",
    "transport": "direct_http"
}
```

Manual trigger không nhất thiết phải thay đổi `last_pinged_reset_at` trừ khi:

```json
{
    "mark_cycle_processed": true
}
```

Điều này tránh manual testing phá scheduler state.

---

# 32. Status API

Existing status API nên được mở rộng.

Ví dụ:

```http
GET /v0/management/quota-activation/status
```

Response:

```json
{
    "codex_auto_ping": {
        "enabled": true,
        "scan_interval": "1m",
        "accounts": [
            {
                "credential_id": "codex-account-a",
                "window": "5h",
                "current_reset_at": "2026-09-11T20:00:00Z",
                "last_pinged_reset_at": "2026-09-11T15:00:00Z",
                "last_ping_at": "2026-09-11T15:00:07Z",
                "status": "waiting"
            }
        ]
    }
}
```

---

# 33. Diagnostics

Diagnostics nên giúp trả lời:

```text
Why was this account not pinged?
```

Ví dụ:

```json
{
    "credential_id": "codex-account-a",
    "provider": "codex",
    "auto_ping_enabled": true,
    "five_hour_window_detected": true,
    "reset_at": "2026-09-11T20:00:00Z",
    "now": "2026-09-11T18:21:00Z",
    "eligible": false,
    "reason": "reset_not_reached"
}
```

Hoặc:

```json
{
    "eligible": false,
    "reason": "cycle_already_pinged"
}
```

---

# 34. Logging

Log nên đủ rõ nhưng không spam.

Ví dụ waiting:

```text
[auto-ping] credential=codex-a reset_at=20:00 status=waiting
```

Có thể để debug level.

Khi reset detected:

```text
[auto-ping] credential=codex-a reset_at=15:00 status=ready
```

Khi request:

```text
[auto-ping] credential=codex-a model=gpt-5.5 transport=direct_http action=ping
```

Success:

```text
[auto-ping] credential=codex-a reset_at=15:00 status=success
```

Failure:

```text
[auto-ping] credential=codex-a status=failed retry_at=15:15 error="..."
```

Skip:

```text
[auto-ping] credential=codex-a status=skip reason=cycle_already_pinged
```

---

# 35. Không log sensitive information

Không được log:

```text
OAuth access token
refresh token
Authorization header
cookie
full credential secret
```

Credential nên được reference bằng safe ID/name.

---

# 36. Security

Auto-ping chỉ được sử dụng credentials mà CLIProxyAPI/plugin đã được phép truy cập.

Management endpoints phải tiếp tục được bảo vệ bằng CPA management authentication.

Không tạo unauthenticated endpoint cho phép trigger arbitrary Codex request.

---

# 37. Failure classifications

Nên phân loại lỗi.

## Retryable

Ví dụ:

```text
network timeout
temporary 5xx
connection reset
temporary upstream unavailable
```

Behavior:

```text
cooldown
retry later
```

## Possibly retryable with model fallback

```text
model not found
model retired
unsupported model
```

Nếu:

```yaml
model: "auto"
```

plugin có thể thử model candidate tiếp theo.

## Non-retryable hoặc long cooldown

```text
credential revoked
account disabled
authentication invalid
```

Không nên request mỗi 15 phút vô hạn.

---

# 38. Model failure handling

Ví dụ:

```text
auto-selected model A
        ↓
404 model not found
        ↓
invalidate model cache
        ↓
discover/select model B
        ↓
retry once
```

Không nên tạo loop model retry vô hạn.

---

# 39. Success definition

Auto-ping được coi thành công khi:

```text
Codex inference request accepted and successfully starts/returns a valid response
```

Không cần nội dung response có ý nghĩa.

Ví dụ:

```text
HTTP 200 + valid Codex response
```

hoặc streaming protocol hoàn thành hợp lệ.

---

# 40. Avoid duplicate requests trong cùng process

Có thể xảy ra:

```text
scanner tick A
scanner tick B
```

cùng phát hiện một account eligible trước khi state được ghi.

Cần lock/in-flight protection.

Ví dụ state:

```text
credential A
reset_at X
status = in_flight
```

Pseudo:

```go
if !acquirePingLock(credentialID, resetAt) {
    return
}

defer releasePingLock(...)
```

---

# 41. Avoid duplicate requests giữa restart

Persistent state giải quyết phần lớn vấn đề.

Nếu process crash:

```text
request succeeded
   ↓
process crashes
   ↓
state not saved
```

có khả năng ping lại.

Có thể chấp nhận rare duplicate hoặc cải thiện bằng atomic state write.

State write nên:

```text
temp file
   ↓
fsync
   ↓
atomic rename
```

nếu plugin hiện có persistence mechanism tương tự thì reuse.

---

# 42. Scheduler pseudo-code

```go
func ScanCodexAutoPing(ctx context.Context) {
    if !config.CodexAutoPing.Enabled {
        return
    }

    credentials := host.Auth.List()

    for _, credential := range credentials {
        if credential.Provider != "codex" {
            continue
        }

        if !credential.Enabled {
            continue
        }

        quota, err := getQuota(credential)
        if err != nil {
            log.Warn(...)
            continue
        }

        window := findFiveHourWindow(quota)

        if window == nil {
            continue
        }

        if time.Now().Before(window.ResetAt.Add(config.ActivationDelay)) {
            continue
        }

        if state.AlreadyPinged(
            credential.ID,
            window.ResetAt,
        ) {
            continue
        }

        if state.InCooldown(credential.ID) {
            continue
        }

        enqueueAutoPing(
            credential,
            window.ResetAt,
        )
    }
}
```

Worker:

```go
func ExecuteAutoPing(
    credential Credential,
    resetAt time.Time,
) {
    if !acquireLock(credential.ID, resetAt) {
        return
    }

    defer releaseLock(...)

    model := selectModel(credential)

    result, err := activator.Activate(
        credential,
        model,
        config.Prompt,
    )

    if err != nil {
        state.RecordFailure(
            credential.ID,
            resetAt,
            time.Now().Add(config.RetryCooldown),
        )

        return
    }

    state.RecordSuccess(
        credential.ID,
        resetAt,
        time.Now(),
    )
}
```

---

# 43. Quan trọng: `reset_at` sau reset có thể thay đổi

Có một vấn đề cần xử lý cẩn thận.

Trước reset:

```text
reset_at = 15:00
```

Sau khi upstream nhận biết reset nhưng trước khi ping, quota payload có thể đã trả:

```text
reset_at = 20:00
```

Nếu implementation chỉ kiểm tra:

```go
now >= currentResetAt
```

thì sẽ bỏ lỡ auto-ping.

Do đó detector không nên chỉ dựa vào snapshot hiện tại.

Cần lưu previous observed window state.

Ví dụ:

```text
previous scan:

reset_at = 15:00


current scan:

reset_at = 20:00
```

Plugin có thể suy ra:

```text
a new 5h cycle has appeared
```

và cần quyết định xem cycle mới đã được started bởi external traffic hay cần auto-ping.

Tùy semantic thực tế của Codex quota API, implementation cần xác minh chính xác behavior.

Feature phải hỗ trợ cả hai khả năng:

### Case A

Quota giữ old `reset_at` cho tới request đầu tiên:

```text
now >= old reset_at
→ ping
```

### Case B

Quota endpoint tự chuyển `reset_at` sang cycle mới:

```text
old reset_at != new reset_at
→ detect cycle transition
```

Implementation phải dựa trên observed state thay vì assumption cứng.

---

# 44. External traffic

Nếu user đã tự gửi request ngay sau reset:

```text
15:00 reset
15:00:02 user request
15:00:10 auto-ping scanner
```

plugin lý tưởng không nên gửi thêm request.

Nếu quota payload cho thấy window mới đã bắt đầu:

```text
reset_at ≈ 20:00
```

thì detector có thể xem:

```text
cycle already activated externally
```

và không ping.

Nên lưu:

```json
{
    "activation_source": "external"
}
```

nếu xác định được.

Điều này giúp tránh quota waste.

---

# 45. Detection tốt hơn dựa trên reset transition

Ví dụ:

```text
Previous:
reset_at = 15:00
used = 97%

Current:
reset_at = 20:00
used = 1%
```

Có bằng chứng mạnh:

```text
new window already started
```

Không cần auto-ping.

Trong trường hợp:

```text
Previous:
reset_at = 15:00

Current time:
15:01

quota still says:
reset_at = 15:00
```

thì:

```text
auto-ping required
```

---

# 46. Grace period

Có thể bổ sung:

```yaml
external_activity_grace_period: "10s"
```

Sau reset:

```text
wait 5–10 seconds
```

để xem external traffic có tự bắt đầu window không.

Sau đó mới auto-ping.

Không bắt buộc nhưng hữu ích.

---

# 47. Metrics

Nếu plugin có metrics support, nên expose:

```text
codex_auto_ping_attempts_total
codex_auto_ping_success_total
codex_auto_ping_failures_total
codex_auto_ping_skipped_total
```

Optional labels:

```text
reason
transport
```

Không nên dùng credential ID làm high-cardinality metric label nếu có nhiều account.

---

# 48. UI / Status page

Nếu plugin status page hỗ trợ HTML UI, nên có section:

```text
Codex 5h Auto-Ping

Enabled: Yes

Credential     Reset At     Last Ping    Status
------------------------------------------------
Codex A        20:00        15:00:07     Waiting
Codex B        20:42        15:42:03     Waiting
Codex C        17:17        -            Pending
```

Có thể thêm:

```text
Ping Now
```

cho manual testing nếu management UI đã authenticated.

---

# 49. Ví dụ hoàn chỉnh

Giả sử:

```text
Account A
5h reset_at = 10:00
```

### Trước reset

```text
09:59:00

scanner
↓
now < reset_at
↓
skip
```

### Sau reset

```text
10:00:05

scanner
↓
now >= reset_at + activation_delay
↓
cycle not processed
↓
send request
```

Request:

```text
Credential: Account A
Model: auto → gpt-5.5
Prompt: ping
Transport: direct_http
```

Response:

```text
success
```

State:

```json
{
    "credential": "A",
    "last_pinged_reset_at": "10:00",
    "last_ping_at": "10:00:06",
    "status": "success"
}
```

Sau đó Codex:

```text
new reset_at ≈ 15:00
```

Scanner lúc:

```text
10:01
11:00
12:00
13:00
14:00
```

đều:

```text
skip: reset_not_reached
```

15:00:

```text
new cycle
↓
auto-ping again
```

---

# 50. Ví dụ failure

```text
10:00 reset
```

10:00:05:

```text
auto-ping
↓
network timeout
```

State:

```json
{
    "last_attempt_at": "10:00:05",
    "status": "failed",
    "retry_at": "10:15:05"
}
```

10:01:

```text
skip
reason=cooldown
```

10:15:05:

```text
retry
↓
success
```

State:

```json
{
    "last_pinged_reset_at": "10:00",
    "last_ping_at": "10:15:06",
    "status": "success"
}
```

New window sẽ bắt đầu khoảng:

```text
10:15 → 15:15
```

Điều này là expected vì activation không thành công tại 10:00.

---

# 51. Ví dụ với nhiều accounts

```text
Codex A → reset 10:00
Codex B → reset 10:42
Codex C → reset 12:17
Codex D → reset 14:05
```

Auto-ping:

```text
10:00 → A
10:42 → B
12:17 → C
14:05 → D
```

Sau đó:

```text
A next reset ≈ 15:00
B next reset ≈ 15:42
C next reset ≈ 17:17
D next reset ≈ 19:05
```

Plugin giữ được cadence quota của từng account mà không cần manual traffic.

---

# 52. Suggested code organization

Không bắt buộc giữ đúng tên sau, nhưng nên tách rõ responsibilities.

Ví dụ:

```text
internal/
├── autopinger/
│   ├── codex.go
│   ├── scheduler.go
│   ├── state.go
│   └── worker.go
│
├── detector/
│   ├── codex.go
│   └── window.go
│
├── activator/
│   ├── activator.go
│   └── protocol_codex.go
│
└── runtime/
    └── auto_scanner.go
```

Hoặc integrate vào architecture hiện tại nhưng phải giữ:

```text
5h auto-ping logic
```

tách biệt khỏi:

```text
long-window activation logic
```

---

# 53. Testing requirements

Phải bổ sung unit tests.

## Window detection

Input:

```text
limit_window_seconds = 18000
```

Expected:

```text
WindowFiveHour
```

---

## Before reset

```text
now = 09:59
reset_at = 10:00
```

Expected:

```text
no ping
```

---

## After reset

```text
now = 10:00:10
reset_at = 10:00
```

Expected:

```text
ping once
```

---

## Duplicate scan

```text
same reset_at
scanner runs 10 times
```

Expected:

```text
one successful ping only
```

---

## Failure cooldown

```text
first request fails
```

Expected:

```text
no retry before cooldown
```

---

## Retry success

```text
first fails
cooldown expires
second succeeds
```

Expected:

```text
cycle marked processed
```

---

## Restart persistence

```text
ping success
plugin restart
same reset_at
```

Expected:

```text
no duplicate ping
```

---

## Multiple accounts

```text
A eligible
B not eligible
C eligible
```

Expected:

```text
ping A and C only
```

---

## Disabled account

Expected:

```text
skip
```

---

## No 5h window

Expected:

```text
skip
```

---

## Long-window regression

Existing tests for:

```text
7d / long-window activation
```

must continue passing.

---

# 54. Integration tests

Nếu có thể mock Codex endpoint:

```text
Mock Codex
│
├── quota endpoint
└── responses endpoint
```

Test flow:

```text
quota.reset_at = now - 1s
        ↓
scanner
        ↓
POST /responses exactly once
        ↓
mock success
        ↓
state persisted
```

Sau scanner lần hai:

```text
POST count remains 1
```

---

# 55. Acceptance Criteria

Feature chỉ được coi là hoàn thành nếu thỏa tất cả các điều kiện sau.

### AC1

Khi:

```text
codex_auto_ping.enabled = false
```

không có Codex 5h auto-ping request.

### AC2

Plugin detect đúng 5-hour window.

### AC3

Trước `reset_at` không gửi request.

### AC4

Sau `reset_at`, plugin gửi một Codex inference request nhỏ.

### AC5

Request sử dụng đúng credential.

### AC6

Mỗi `reset_at` chỉ được auto-ping thành công một lần.

### AC7

State survive plugin/process restart.

### AC8

Failure có retry cooldown.

### AC9

Không retry liên tục mỗi scan.

### AC10

Nhiều accounts được quản lý độc lập.

### AC11

Existing long-window quota activation không bị thay đổi.

### AC12

`direct_http` hoạt động.

### AC13

`scheduler_boost_fallback` vẫn có thể sử dụng khi phù hợp.

### AC14

Không expose token trong logs/API.

### AC15

Model có thể cấu hình explicit.

### AC16

Có chế độ:

```yaml
model: auto
```

hoặc một mechanism tương đương để tránh phụ thuộc cứng vào model dễ bị retire.

### AC17

Có status/diagnostic information đủ để xác định tại sao account được hoặc không được ping.

---

# 56. Recommended defaults

Đề xuất:

```yaml
codex_auto_ping:
    enabled: false
    scan_interval: "1m"
    activation_delay: "5s"
    retry_cooldown: "15m"
    max_concurrency: 1
    request_timeout: "60s"
    prompt: "ping"
    model: "auto"
    transport: "direct_http"
    scheduler_boost_fallback: true
```

Quan trọng:

```text
enabled = false by default
```

để tránh user upgrade plugin rồi vô tình phát sinh inference request.

---

# 57. README update

README phải giải thích rõ:

> Codex Auto-Ping automatically sends a minimal Codex inference request after a 5-hour rolling quota window resets, causing the next 5-hour window to begin immediately. This consumes a small amount of quota.

Phải có cảnh báo:

> Auto-Ping does not increase, reset, bypass, or create quota. It only starts the next rolling quota window by sending a real request.

---

# 58. README example

```yaml
plugins:
    configs:
        quota-activation:
            enabled: true

            codex_auto_ping:
                enabled: true
                scan_interval: "1m"
                activation_delay: "5s"
                retry_cooldown: "15m"
                model: "auto"
                prompt: "ping"
                max_concurrency: 1
                transport: "direct_http"
```

Explanation:

```text
Account reset:
15:00

Auto-Ping:
15:00:05

Next 5h window:
~15:00 → ~20:00
```

---

# 59. Relationship với existing quota activation

README phải phân biệt rõ hai feature.

| Feature                | Purpose                                                            |
| ---------------------- | ------------------------------------------------------------------ |
| Long-window activation | Activate/manage longer Codex/Antigravity quota cycles              |
| Codex 5h Auto-Ping     | Immediately start the next Codex rolling 5-hour window after reset |

Hai feature:

```text
independent
```

nhưng có thể:

```text
enabled simultaneously
```

---

# 60. Tóm tắt implementation

Feature mong muốn có thể tóm tắt bằng flow sau:

```text
             CLIProxyAPI
                  │
                  ▼
         list Codex credentials
                  │
                  ▼
           fetch quota state
                  │
                  ▼
        detect 5-hour window
                  │
                  ▼
           inspect reset_at
                  │
           ┌──────┴───────┐
           │              │
       not reset        reset
           │              │
          skip            ▼
                   already processed?
                       │
                 ┌─────┴─────┐
                 │           │
                yes          no
                 │           │
                skip         ▼
                           cooldown?
                             │
                       ┌─────┴─────┐
                       │           │
                      yes          no
                       │           │
                      skip         ▼
                              select model
                                   │
                                   ▼
                          send tiny request
                                   │
                         ┌─────────┴─────────┐
                         │                   │
                      success              failure
                         │                   │
                         ▼                   ▼
                  persist success     persist failure
                         │                   │
                         ▼                   ▼
                  wait next cycle        cooldown
```

---

# 61. Kết quả cuối cùng mong muốn

Sau khi implementation hoàn thành, `quota-activation` phải có khả năng cung cấp behavior tương đương ý tưởng của **9router Codex Auto-Ping**:

> Auto-start the next Codex 5-hour window immediately after reset by sending a very small Codex request. The request consumes a small amount of quota.

Nhưng implementation cho CLIProxyAPI cần tốt hơn ở các điểm:

- multi-account aware;
- persistent state;
- configurable model;
- `model: auto`;
- retry cooldown;
- direct credential targeting;
- concurrency control;
- diagnostics;
- manual trigger;
- compatibility với existing long-window activation;
- không hardcode model dễ bị retire;
- không duplicate ping cùng quota cycle.

Mục tiêu cuối cùng là biến `quota-activation` thành một plugin vừa hỗ trợ **long-window activation hiện tại**, vừa hỗ trợ **Codex 5-hour Auto-Ping** phù hợp với các CLIProxyAPI deployment có nhiều Codex OAuth accounts.
