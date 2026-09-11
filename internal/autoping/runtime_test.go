package autoping

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestScannerHandlesMultipleAccountsOnceAndSurvivesRestart(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)}
	host := newFakeHost()
	for _, id := range []string{"codex-a", "codex-b", "codex-c"} {
		file, document := credential(id, "token-"+id, 1)
		host.auths = append(host.auths, file)
		host.docs[file.AuthIndex] = document
	}

	var quotaMu sync.Mutex
	resets := map[string]time.Time{
		"token-codex-a": clock.Now().Add(5 * time.Hour),
		"token-codex-b": clock.Now().Add(5 * time.Hour),
		"token-codex-c": clock.Now().Add(5 * time.Hour),
	}
	host.httpDoFunc = func(request HTTPRequest) (HTTPResponse, error) {
		token := request.Headers.Get("Authorization")
		token = token[len("Bearer "):]
		quotaMu.Lock()
		resetAt := resets[token]
		quotaMu.Unlock()
		return HTTPResponse{StatusCode: http.StatusOK, Body: usagePayload(resetAt, 0)}, nil
	}
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) { return successStream() }

	startupDelay := 24 * time.Hour
	runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: &startupDelay})
	cfg := testConfig(t, runtime, "max_concurrency: 2\nscheduler_boost_fallback: false\n")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Shutdown(t.Context()) })

	if err := runtime.ScanOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(host.streamRequests) != 0 {
		t.Fatalf("initial observation sent %d pings", len(host.streamRequests))
	}

	clock.Advance(time.Minute)
	quotaMu.Lock()
	resets["token-codex-a"] = resets["token-codex-a"].Add(time.Minute)
	resets["token-codex-c"] = resets["token-codex-c"].Add(time.Minute)
	quotaMu.Unlock()
	if err := runtime.ScanOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(host.streamRequests) != 2 {
		t.Fatalf("pings = %d, want A and C only; accounts=%#v", len(host.streamRequests), runtime.store.Accounts())
	}

	if err := runtime.ScanOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(host.streamRequests) != 2 {
		t.Fatalf("duplicate scan sent another ping: %d", len(host.streamRequests))
	}

	if err := runtime.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	restarted := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: &startupDelay})
	if err := restarted.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	defer restarted.Shutdown(t.Context())
	if err := restarted.ScanOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(host.streamRequests) != 2 {
		t.Fatalf("restart duplicated a processed cycle: %d", len(host.streamRequests))
	}
}

func TestScannerFailureCooldownThenRetrySuccess(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)}
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document

	resetAt := clock.Now().Add(5 * time.Hour)
	host.httpDoFunc = func(HTTPRequest) (HTTPResponse, error) {
		return HTTPResponse{StatusCode: http.StatusOK, Body: usagePayload(resetAt, 0)}, nil
	}
	attempts := 0
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		attempts++
		if attempts == 1 {
			return HTTPStreamResponse{}, nil, errors.New("temporary stream failure")
		}
		return successStream()
	}

	startupDelay := 24 * time.Hour
	runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: &startupDelay})
	cfg := testConfig(t, runtime, "scheduler_boost_fallback: false\n")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(t.Context())

	if err := runtime.ScanOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	resetAt = resetAt.Add(time.Minute)
	if err := runtime.ScanOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want first failure; accounts=%#v", attempts, runtime.store.Accounts())
	}

	clock.Advance(time.Minute)
	resetAt = resetAt.Add(time.Minute)
	if err := runtime.ScanOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatalf("cooldown retried early: %d", attempts)
	}

	clock.Advance(cfg.RetryCooldown)
	resetAt = resetAt.Add(cfg.RetryCooldown)
	if err := runtime.ScanOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want retry after cooldown", attempts)
	}
	state := runtime.store.Credential("codex-a")
	if state.LastAttemptStatus != "success" || state.LastProcessedResetAt.IsZero() {
		t.Fatalf("state after retry = %#v", state)
	}
}

func TestScannerSkipsDisabledAndMissingFiveHourWindow(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)}
	host := newFakeHost()
	disabled, disabledDocument := credential("codex-disabled", "token-disabled", 1)
	disabled.Disabled = true
	longOnly, longDocument := credential("codex-long", "token-long", 1)
	host.auths = []AuthFile{disabled, longOnly}
	host.docs[disabled.AuthIndex] = disabledDocument
	host.docs[longOnly.AuthIndex] = longDocument
	host.httpDoFunc = func(HTTPRequest) (HTTPResponse, error) {
		body := []byte(`{"rate_limit":{"primary_window":{"limit_window_seconds":604800,"reset_at":"2026-09-18T10:00:00Z"}}}`)
		return HTTPResponse{StatusCode: http.StatusOK, Body: body}, nil
	}
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		t.Fatal("ineligible credential must not be pinged")
		return HTTPStreamResponse{}, nil, nil
	}

	startupDelay := 24 * time.Hour
	runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: &startupDelay})
	cfg := testConfig(t, runtime, "")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(t.Context())
	if err := runtime.ScanOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if state := runtime.store.Credential("codex-disabled"); state.Reason != "disabled" {
		t.Fatalf("disabled state = %#v", state)
	}
	if state := runtime.store.Credential("codex-long"); state.Reason != "five_hour_window_not_found" {
		t.Fatalf("long-window state = %#v", state)
	}
}

func TestManualPingDoesNotProcessBoundaryByDefault(t *testing.T) {
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) { return successStream() }

	runtime := newTestRuntime(t, host, Options{})
	cfg := testConfig(t, runtime, "auto_ping_disabled: true\n")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	response, err := runtime.ManualPing(t.Context(), ManualPingRequest{CredentialID: "codex-a"})
	if err != nil || !response.Success {
		t.Fatalf("response = %#v, error = %v", response, err)
	}
	if state := runtime.store.Credential("codex-a"); !state.LastProcessedResetAt.IsZero() {
		t.Fatalf("manual ping processed boundary: %#v", state)
	}
}

func TestDisabledScannerSendsNoRequests(t *testing.T) {
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document
	host.httpDoFunc = func(HTTPRequest) (HTTPResponse, error) {
		t.Fatal("disabled scanner fetched quota")
		return HTTPResponse{}, nil
	}
	runtime := newTestRuntime(t, host, Options{})
	cfg := testConfig(t, runtime, "auto_ping_disabled: true\n")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ScanOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func usagePayload(resetAt time.Time, usedPercent float64) []byte {
	payload, _ := json.Marshal(map[string]any{
		"rate_limit": map[string]any{
			"primary_window": map[string]any{
				"limit_window_seconds": fiveHourSeconds,
				"reset_at":             resetAt.UTC().Format(time.RFC3339),
				"used_percent":         usedPercent,
			},
		},
	})
	return payload
}
