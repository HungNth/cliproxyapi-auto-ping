package autoping

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestManualPingUnmarkedDoesNotAnchorDynamicCycle(t *testing.T) {
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) { return successStream() }

	runtime := newTestRuntime(t, host, Options{})
	cfg := testConfig(t, runtime, "")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	resp, err := runtime.ManualPing(t.Context(), ManualPingRequest{CredentialID: "codex-a", MarkCycleProcessed: false})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got %#v", resp)
	}
	state := runtime.store.Credential("codex-a")
	if state.LastAttemptStatus != "success" {
		t.Fatalf("last attempt status = %q, want success", state.LastAttemptStatus)
	}
	if state.Successes != 1 || state.Failures != 0 {
		t.Fatalf("successes = %d, failures = %d, want 1/0", state.Successes, state.Failures)
	}
	if !state.TargetTriggerAt.IsZero() {
		t.Fatalf("unmarked manual ping should not set TargetTriggerAt, got %v", state.TargetTriggerAt)
	}
}

func TestManualPingMarkedCycleAnchorsDynamicCycle(t *testing.T) {
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) { return successStream() }
	host.httpDoFunc = func(HTTPRequest) (HTTPResponse, error) {
		return HTTPResponse{
			StatusCode: http.StatusOK,
			Body:       []byte(`{"rate_limit": {"limit_window_seconds": 18000, "reset_at": 1790084449}}`),
		}, nil
	}

	runtime := newTestRuntime(t, host, Options{})
	cfg := testConfig(t, runtime, "")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	resp, err := runtime.ManualPing(t.Context(), ManualPingRequest{CredentialID: "codex-a", MarkCycleProcessed: true})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got %#v", resp)
	}
	state := runtime.store.Credential("codex-a")
	if state.LastAttemptStatus != "success" {
		t.Fatalf("last attempt status = %q, want success", state.LastAttemptStatus)
	}
	if state.Successes != 1 || state.Failures != 0 {
		t.Fatalf("successes = %d, failures = %d, want 1/0", state.Successes, state.Failures)
	}
	if state.ObservedResetAt.Unix() != 1790084449 {
		t.Fatalf("observed_reset_at = %d, want 1790084449", state.ObservedResetAt.Unix())
	}
	if state.TargetTriggerAt.Unix() != 1790084449+30 {
		t.Fatalf("target_trigger_at = %d, want %d", state.TargetTriggerAt.Unix(), 1790084449+30)
	}
	if !state.LastProcessedResetAt.IsZero() {
		t.Fatalf("fresh unanchored manual ping should have zero LastProcessedResetAt, got %v", state.LastProcessedResetAt)
	}
}

func TestManualPingFailureRecordsFailedStatus(t *testing.T) {
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		return HTTPStreamResponse{StatusCode: http.StatusInternalServerError}, []HTTPStreamChunk{{Done: true}}, nil
	}

	runtime := newTestRuntime(t, host, Options{})
	cfg := testConfig(t, runtime, "")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	resp, err := runtime.ManualPing(t.Context(), ManualPingRequest{CredentialID: "codex-a"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if resp.Success {
		t.Fatal("expected resp.Success = false")
	}
	state := runtime.store.Credential("codex-a")
	if state.LastAttemptStatus != "failed" {
		t.Fatalf("last attempt status = %q, want failed", state.LastAttemptStatus)
	}
	if state.Failures != 1 || state.Successes != 0 {
		t.Fatalf("failures = %d, successes = %d, want 1/0", state.Failures, state.Successes)
	}
}

func TestManagementHandleStatusExposesDynamicTriggers(t *testing.T) {
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document

	runtime := newTestRuntime(t, host, Options{})
	cfg := testConfig(t, runtime, "")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	resetAt := time.Date(2026, 9, 22, 8, 0, 3, 0, time.UTC)
	targetAt := resetAt.Add(30 * time.Second)
	_ = runtime.store.Update(t.Context(), "codex-a", func(current *CredentialState) {
		current.Status = "waiting"
		current.Reason = "trigger_scheduled"
		current.ObservedResetAt = resetAt
		current.TargetTriggerAt = targetAt
	})

	request, _ := json.Marshal(managementRequest{Method: http.MethodGet, Path: "/v0/management/cliproxyapi-auto-ping/status"})
	raw := runtime.Handle(t.Context(), "management.handle", request)
	response := decodeManagementResponse(t, raw)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}

	var payload struct {
		Accounts []CredentialState `json:"accounts"`
	}
	if err := json.Unmarshal(response.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Accounts) != 1 {
		t.Fatalf("accounts len = %d, want 1", len(payload.Accounts))
	}
	account := payload.Accounts[0]
	if !account.ObservedResetAt.Equal(resetAt) {
		t.Fatalf("observed_reset_at = %v, want %v", account.ObservedResetAt, resetAt)
	}
	if !account.TargetTriggerAt.Equal(targetAt) {
		t.Fatalf("target_trigger_at = %v, want %v", account.TargetTriggerAt, targetAt)
	}
}

func TestManagementHandlePingMarkedCycleAnchorsNextCycle(t *testing.T) {
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) { return successStream() }
	host.httpDoFunc = func(HTTPRequest) (HTTPResponse, error) {
		return HTTPResponse{
			StatusCode: http.StatusOK,
			Body:       []byte(`{"rate_limit":{"limit_window_seconds":18000,"reset_at":1790084449}}`),
		}, nil
	}

	runtime := newTestRuntime(t, host, Options{})
	cfg := testConfig(t, runtime, "")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	pingReqBody, _ := json.Marshal(ManualPingRequest{CredentialID: "codex-a", MarkCycleProcessed: true})
	req, _ := json.Marshal(managementRequest{
		Method: http.MethodPost,
		Path:   "/v0/management/cliproxyapi-auto-ping/ping",
		Body:   pingReqBody,
	})
	raw := runtime.Handle(t.Context(), "management.handle", req)
	response := decodeManagementResponse(t, raw)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.StatusCode, string(response.Body))
	}

	state := runtime.store.Credential("codex-a")
	if state.ObservedResetAt.Unix() != 1790084449 {
		t.Fatalf("observed_reset_at = %d, want 1790084449", state.ObservedResetAt.Unix())
	}
	if state.TargetTriggerAt.Unix() != 1790084449+30 {
		t.Fatalf("target_trigger_at = %d, want %d", state.TargetTriggerAt.Unix(), 1790084449+30)
	}
}

func TestDynamicReconfigurationCancelsRunningScheduleCleanly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newFakeHost()
		file, document := credential("codex-a", "token-a", 1)
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document

		clock := &fakeClock{now: time.Date(2026, 9, 22, 4, 0, 0, 0, time.UTC)}
		var usageCalls atomic.Int32
		host.httpDoFunc = func(HTTPRequest) (HTTPResponse, error) {
			usageCalls.Add(1)
			return HTTPResponse{
				StatusCode: http.StatusOK,
				Body:       []byte(fmt.Sprintf(`{"rate_limit":{"limit_window_seconds":18000,"reset_at":%d}}`, clock.Now().Add(5*time.Hour).Unix())),
			}, nil
		}

		runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: new(time.Duration)})
		cfg1 := testConfig(t, runtime, "schedule: [\"05:00\"]\ntimezone: UTC\n")
		cfg1.StatePath = filepath.Join(t.TempDir(), "old-state.json")
		if err := runtime.Configure(t.Context(), cfg1); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if usageCalls.Load() != 0 {
			t.Fatalf("old schedule queried before 05:00: %d", usageCalls.Load())
		}

		cfg2State := filepath.Join(t.TempDir(), "new-state.json")
		cfg2YAML := "schedule: [\"04:00\"]\ntimezone: UTC\nstate_path: " + cfg2State + "\n"
		req, _ := json.Marshal(map[string]string{"config_yaml": cfg2YAML})
		raw := runtime.Handle(t.Context(), "plugin.reconfigure", req)
		var envelope struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil || !envelope.OK {
			t.Fatalf("reconfigure failed: %s", string(raw))
		}
		synctest.Wait()
		if usageCalls.Load() != 1 {
			t.Fatalf("replacement schedule usage calls = %d, want 1", usageCalls.Load())
		}

		// If the old 05:00 loop survived cancellation, it would query against old-state.json here.
		clock.Advance(time.Hour)
		time.Sleep(time.Hour)
		synctest.Wait()
		if usageCalls.Load() != 1 {
			t.Fatalf("old schedule survived reconfiguration: usage calls = %d, want 1", usageCalls.Load())
		}

		shutdownRaw := runtime.Handle(t.Context(), "plugin.shutdown", nil)
		var shutdownEnvelope struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(shutdownRaw, &shutdownEnvelope); err != nil || !shutdownEnvelope.OK {
			t.Fatalf("shutdown failed: %s", string(shutdownRaw))
		}
	})
}
