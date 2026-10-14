package autoping

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func antigravityCredential(id string) (AuthFile, AuthDocument) {
	authIndex := "index-" + id
	file := AuthFile{ID: id, AuthIndex: authIndex, Name: id + ".json", Provider: "antigravity", Status: "active", Priority: 1}
	data, _ := json.Marshal(map[string]any{"type": "antigravity", "access_token": "token-" + id, "project_id": "proj-" + id})
	return file, AuthDocument{AuthIndex: authIndex, Name: file.Name, JSON: data}
}

func TestManualPingAntigravityRequiresValidTargetModel(t *testing.T) {
	host := newFakeHost()
	file, document := antigravityCredential("ag-test-1")
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document

	runtime := newTestRuntime(t, host, Options{})
	cfg := testConfig(t, runtime, "")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	_, err := runtime.ManualPing(t.Context(), ManualPingRequest{
		CredentialID: "ag-test-1",
		Model:        "gpt-5.5",
	})
	if err == nil {
		t.Fatal("expected error for unsupported model on Antigravity credential, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported Antigravity model") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestManualPingAntigravityExactPinnedExecution(t *testing.T) {
	host := newFakeHost()
	file, document := antigravityCredential("ag-exact-1")
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document

	var capturedReq ModelExecuteRequest
	host.modelExecuteFunc = func(req ModelExecuteRequest) (ModelExecuteResponse, error) {
		capturedReq = req
		return ModelExecuteResponse{
			StatusCode: http.StatusOK,
			Body:       []byte(`{"type":"response.completed","response":{"id":"resp_ag","status":"completed"}}`),
		}, nil
	}

	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	runtime := newTestRuntime(t, host, Options{Now: func() time.Time { return now }})
	cfg := testConfig(t, runtime, "")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	resp, err := runtime.ManualPing(t.Context(), ManualPingRequest{
		CredentialID: "ag-exact-1",
		Model:        AntigravityModelGemini,
	})
	if err != nil {
		t.Fatalf("unexpected manual ping error: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got %#v", resp)
	}

	// Verify host model execution parameters
	if capturedReq.ForcedProvider != "antigravity" {
		t.Fatalf("captured ForcedProvider = %q, want antigravity", capturedReq.ForcedProvider)
	}
	if capturedReq.AuthID != "ag-exact-1" {
		t.Fatalf("captured AuthID = %q, want ag-exact-1", capturedReq.AuthID)
	}
	if capturedReq.Model != AntigravityModelGemini {
		t.Fatalf("captured Model = %q, want %q", capturedReq.Model, AntigravityModelGemini)
	}
	if capturedReq.Stream {
		t.Fatal("captured Stream = true, want false")
	}
	if strings.Contains(string(capturedReq.Body), "max_output_tokens") || strings.Contains(string(capturedReq.Body), "max_tokens") {
		t.Fatalf("captured Body must not contain output token limit fields, got %s", string(capturedReq.Body))
	}

	// Verify state store persistence
	state := runtime.store.Credential("ag-exact-1")
	if state.Provider != "antigravity" {
		t.Fatalf("state.Provider = %q, want antigravity", state.Provider)
	}
	geminiTarget, ok := state.Targets[TargetKeyGemini5h]
	if !ok {
		t.Fatalf("missing target %q in state.Targets: %#v", TargetKeyGemini5h, state.Targets)
	}
	if geminiTarget.LastAttemptStatus != "success" {
		t.Fatalf("geminiTarget.LastAttemptStatus = %q, want success", geminiTarget.LastAttemptStatus)
	}
	if geminiTarget.Successes != 1 || geminiTarget.Failures != 0 {
		t.Fatalf("geminiTarget counts: successes = %d, failures = %d, want 1/0", geminiTarget.Successes, geminiTarget.Failures)
	}
	if geminiTarget.SelectedModel != AntigravityModelGemini {
		t.Fatalf("geminiTarget.SelectedModel = %q, want %q", geminiTarget.SelectedModel, AntigravityModelGemini)
	}
	if geminiTarget.Transport != TransportHostModel {
		t.Fatalf("geminiTarget.Transport = %q, want %q", geminiTarget.Transport, TransportHostModel)
	}
}

func TestManualPingAntigravityFailureRecordsTargetFailedStatus(t *testing.T) {
	host := newFakeHost()
	file, document := antigravityCredential("ag-fail-1")
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document

	host.modelExecuteFunc = func(req ModelExecuteRequest) (ModelExecuteResponse, error) {
		return ModelExecuteResponse{
			StatusCode: http.StatusBadGateway,
			Body:       []byte(`{"error":{"message":"upstream gateway error"}}`),
		}, nil
	}

	runtime := newTestRuntime(t, host, Options{})
	cfg := testConfig(t, runtime, "")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	resp, err := runtime.ManualPing(t.Context(), ManualPingRequest{
		CredentialID: "ag-fail-1",
		Model:        AntigravityModelClaude,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if resp.Success {
		t.Fatal("expected resp.Success = false")
	}

	state := runtime.store.Credential("ag-fail-1")
	claudeTarget, ok := state.Targets[TargetKeyClaude5h]
	if !ok {
		t.Fatalf("missing target %q in state.Targets", TargetKeyClaude5h)
	}
	if claudeTarget.LastAttemptStatus != "failed" {
		t.Fatalf("claudeTarget.LastAttemptStatus = %q, want failed", claudeTarget.LastAttemptStatus)
	}
	if claudeTarget.Failures != 1 || claudeTarget.Successes != 0 {
		t.Fatalf("claudeTarget counts: failures = %d, successes = %d, want 1/0", claudeTarget.Failures, claudeTarget.Successes)
	}
}

func TestManualPingAntigravityIneligible(t *testing.T) {
	host := newFakeHost()
	file, document := antigravityCredential("ag-disabled-1")
	file.Disabled = true
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document

	runtime := newTestRuntime(t, host, Options{})
	cfg := testConfig(t, runtime, "")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	_, err := runtime.ManualPing(t.Context(), ManualPingRequest{
		CredentialID: "ag-disabled-1",
		Model:        AntigravityModelGemini,
	})
	if err == nil {
		t.Fatal("expected ineligible error, got nil")
	}
	if !strings.Contains(err.Error(), "credential is not eligible: disabled") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestManualPingAntigravityTimeoutClassification(t *testing.T) {
	host := newFakeHost()
	file, document := antigravityCredential("ag-timeout-1")
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document

	host.modelExecuteFunc = func(req ModelExecuteRequest) (ModelExecuteResponse, error) {
		return ModelExecuteResponse{}, context.DeadlineExceeded
	}

	runtime := newTestRuntime(t, host, Options{})
	cfg := testConfig(t, runtime, "")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	cfg.RequestTimeout = 1 * time.Millisecond
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	resp, err := runtime.ManualPing(t.Context(), ManualPingRequest{
		CredentialID: "ag-timeout-1",
		Model:        AntigravityModelGemini,
	})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if resp.Success {
		t.Fatal("expected resp.Success = false")
	}

	state := runtime.store.Credential("ag-timeout-1")
	geminiTarget, ok := state.Targets[TargetKeyGemini5h]
	if !ok {
		t.Fatalf("missing target %q in state.Targets", TargetKeyGemini5h)
	}
	if geminiTarget.FailureKind != string(FailureTimeout) {
		t.Fatalf("geminiTarget.FailureKind = %q, want %q", geminiTarget.FailureKind, FailureTimeout)
	}
}

func TestManualPingAntigravityPersistenceFailureReturnsError(t *testing.T) {
	host := newFakeHost()
	file, document := antigravityCredential("ag-persist-err-1")
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document

	host.modelExecuteFunc = func(req ModelExecuteRequest) (ModelExecuteResponse, error) {
		return ModelExecuteResponse{
			StatusCode: http.StatusOK,
			Body:       []byte(`{"type":"response.completed"}`),
		}, nil
	}

	runtime := newTestRuntime(t, host, Options{})
	cfg := testConfig(t, runtime, "")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	canceledCtx, cancel := context.WithCancel(t.Context())
	cancel() // Canceled context causes store.Update saveLocked to fail

	resp, err := runtime.ManualPing(canceledCtx, ManualPingRequest{
		CredentialID: "ag-persist-err-1",
		Model:        AntigravityModelGemini,
	})
	if err == nil {
		t.Fatal("expected persistence error on canceled context, got nil")
	}
	if resp.Success {
		t.Fatal("expected resp.Success = false on persistence failure")
	}
	if !strings.Contains(err.Error(), "persist manual ping state") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestManualPingAntigravityMarkedCycleAnchorsAtFutureResetPlusMargin(t *testing.T) {
	host := newFakeHost()
	file, document := antigravityCredential("ag-marked-1")
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document

	host.modelExecuteFunc = func(req ModelExecuteRequest) (ModelExecuteResponse, error) {
		return ModelExecuteResponse{
			StatusCode: http.StatusOK,
			Body:       []byte(`{"type":"response.completed","response":{"id":"resp_ag","status":"completed"}}`),
		}, nil
	}

	var capturedQuotaReq HTTPRequest
	host.httpDoFunc = func(req HTTPRequest) (HTTPResponse, error) {
		capturedQuotaReq = req
		return HTTPResponse{
			StatusCode: http.StatusOK,
			Body: []byte(`{
				"groups": [
					{
						"displayName": "Gemini Models",
						"buckets": [
							{"bucketId": "gemini-5h", "window": "5h", "resetTime": "2026-10-14T17:00:00Z", "remainingFraction": 0.95},
							{"bucketId": "gemini-weekly", "window": "weekly", "resetTime": "2026-10-21T12:00:00Z"}
						]
					}
				]
			}`),
		}, nil
	}

	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	runtime := newTestRuntime(t, host, Options{Now: func() time.Time { return now }})
	cfg := testConfig(t, runtime, "antigravity_reset_margin: 45s")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	resp, err := runtime.ManualPing(t.Context(), ManualPingRequest{
		CredentialID:       "ag-marked-1",
		Model:              AntigravityModelGemini,
		MarkCycleProcessed: true,
	})
	if err != nil {
		t.Fatalf("unexpected manual ping error: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got %#v", resp)
	}

	// Verify quota request called retrieveUserQuotaSummary
	if capturedQuotaReq.URL != antigravityQuotaURL {
		t.Fatalf("quota request URL = %q, want %q", capturedQuotaReq.URL, antigravityQuotaURL)
	}

	state := runtime.store.Credential("ag-marked-1")
	target := state.Targets[TargetKeyGemini5h]
	expectedReset := time.Date(2026, 10, 14, 17, 0, 0, 0, time.UTC)
	if !target.ObservedResetAt.Equal(expectedReset) {
		t.Fatalf("target.ObservedResetAt = %v, want %v", target.ObservedResetAt, expectedReset)
	}
	expectedTrigger := expectedReset.Add(45 * time.Second)
	if !target.TargetTriggerAt.Equal(expectedTrigger) {
		t.Fatalf("target.TargetTriggerAt = %v, want %v", target.TargetTriggerAt, expectedTrigger)
	}
	if target.Status != "waiting" {
		t.Fatalf("target.Status = %q, want waiting", target.Status)
	}
	if target.Reason != "trigger_scheduled" {
		t.Fatalf("target.Reason = %q, want trigger_scheduled", target.Reason)
	}
}

func TestManualPingAntigravityMarkedCycleStabilizingWhenQuotaNonFuture(t *testing.T) {
	host := newFakeHost()
	file, document := antigravityCredential("ag-stale-1")
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document

	host.modelExecuteFunc = func(req ModelExecuteRequest) (ModelExecuteResponse, error) {
		return ModelExecuteResponse{
			StatusCode: http.StatusOK,
			Body:       []byte(`{"type":"response.completed"}`),
		}, nil
	}

	// Returns reset in the past (non-future)
	host.httpDoFunc = func(req HTTPRequest) (HTTPResponse, error) {
		return HTTPResponse{
			StatusCode: http.StatusOK,
			Body: []byte(`{
				"groups": [
					{
						"displayName": "Gemini Models",
						"buckets": [
							{"bucketId": "gemini-5h", "window": "5h", "resetTime": "2026-10-14T10:00:00Z"}
						]
					}
				]
			}`),
		}, nil
	}

	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	runtime := newTestRuntime(t, host, Options{Now: func() time.Time { return now }})
	cfg := testConfig(t, runtime, "")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	resp, err := runtime.ManualPing(t.Context(), ManualPingRequest{
		CredentialID:       "ag-stale-1",
		Model:              AntigravityModelGemini,
		MarkCycleProcessed: true,
	})
	if err == nil {
		t.Fatal("expected error when quota reset is non-future, got nil")
	}
	if resp.Success {
		t.Fatal("expected resp.Success = false")
	}

	state := runtime.store.Credential("ag-stale-1")
	target := state.Targets[TargetKeyGemini5h]
	if target.Status != "stabilizing" {
		t.Fatalf("target.Status = %q, want stabilizing", target.Status)
	}
	if target.Reason != "usage_stabilization_retry" {
		t.Fatalf("target.Reason = %q, want usage_stabilization_retry", target.Reason)
	}
	if !target.TargetTriggerAt.IsZero() {
		t.Fatalf("target.TargetTriggerAt must be zero during stabilizing, got %v", target.TargetTriggerAt)
	}
}

func TestManualPingAntigravityResolvesMissingProjectViaLoadCodeAssist(t *testing.T) {
	host := newFakeHost()
	// Document without project_id
	authIndex := "index-ag-noproject"
	file := AuthFile{ID: "ag-noproject", AuthIndex: authIndex, Name: "ag-noproject.json", Provider: "antigravity", Status: "active", Priority: 1}
	data, _ := json.Marshal(map[string]any{"type": "antigravity", "access_token": "token-xyz"})
	document := AuthDocument{AuthIndex: authIndex, Name: file.Name, JSON: data}
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document

	host.modelExecuteFunc = func(req ModelExecuteRequest) (ModelExecuteResponse, error) {
		return ModelExecuteResponse{
			StatusCode: http.StatusOK,
			Body:       []byte(`{"type":"response.completed"}`),
		}, nil
	}

	var capturedProjectInQuota string
	host.httpDoFunc = func(req HTTPRequest) (HTTPResponse, error) {
		switch req.URL {
		case antigravityCodeAssistURL:
			return HTTPResponse{
				StatusCode: http.StatusOK,
				Body:       []byte(`{"cloudaicompanionProject":{"id":"discovered-proj-99"}}`),
			}, nil
		case antigravityQuotaURL:
			var parsed map[string]string
			_ = json.Unmarshal(req.Body, &parsed)
			capturedProjectInQuota = parsed["project"]
			return HTTPResponse{
				StatusCode: http.StatusOK,
				Body: []byte(`{
					"groups": [
						{
							"displayName": "Claude and GPT models",
							"buckets": [
								{"bucketId": "3p-5h", "window": "5h", "resetTime": "2026-10-14T19:00:00Z"}
							]
						}
					]
				}`),
			}, nil
		default:
			return HTTPResponse{StatusCode: http.StatusNotFound}, nil
		}
	}

	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	runtime := newTestRuntime(t, host, Options{Now: func() time.Time { return now }})
	cfg := testConfig(t, runtime, "")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	resp, err := runtime.ManualPing(t.Context(), ManualPingRequest{
		CredentialID:       "ag-noproject",
		Model:              AntigravityModelClaude,
		MarkCycleProcessed: true,
	})
	if err != nil {
		t.Fatalf("unexpected manual ping error: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got %#v", resp)
	}
	if capturedProjectInQuota != "discovered-proj-99" {
		t.Fatalf("quota request project = %q, want discovered-proj-99", capturedProjectInQuota)
	}
}

func TestAntigravityResetMarginConfigValidationAndDefault(t *testing.T) {
	host := newFakeHost()
	runtime := newTestRuntime(t, host, Options{})

	// Default configuration
	cfg := testConfig(t, runtime, "")
	if cfg.AntigravityResetMargin != 30*time.Second {
		t.Fatalf("cfg.AntigravityResetMargin default = %v, want 30s", cfg.AntigravityResetMargin)
	}

	// Explicit margin
	cfgExplicit := testConfig(t, runtime, "antigravity_reset_margin: 1m")
	if cfgExplicit.AntigravityResetMargin != 1*time.Minute {
		t.Fatalf("cfgExplicit.AntigravityResetMargin = %v, want 1m", cfgExplicit.AntigravityResetMargin)
	}

	// Negative margin rejected
	_, err := runtime.parseConfig([]byte("antigravity_reset_margin: -10s"))
	if err == nil {
		t.Fatal("expected error for negative antigravity_reset_margin, got nil")
	}
}

func TestManualPingAntigravityMarkedPersistenceFailure(t *testing.T) {
	host := newFakeHost()
	file, document := antigravityCredential("ag-marked-fail-1")
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document

	cancelCtx, cancel := context.WithCancel(t.Context())
	host.modelExecuteFunc = func(req ModelExecuteRequest) (ModelExecuteResponse, error) {
		return ModelExecuteResponse{
			StatusCode: http.StatusOK,
			Body:       []byte(`{"type":"response.completed"}`),
		}, nil
	}
	host.httpDoFunc = func(req HTTPRequest) (HTTPResponse, error) {
		cancel() // Cancel before quota persistence executes
		return HTTPResponse{
			StatusCode: http.StatusOK,
			Body: []byte(`{
				"groups": [
					{
						"displayName": "Gemini Models",
						"buckets": [
							{"bucketId": "gemini-5h", "window": "5h", "resetTime": "2026-10-14T20:00:00Z"}
						]
					}
				]
			}`),
		}, nil
	}

	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	runtime := newTestRuntime(t, host, Options{Now: func() time.Time { return now }})
	cfg := testConfig(t, runtime, "")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	resp, err := runtime.ManualPing(cancelCtx, ManualPingRequest{
		CredentialID:       "ag-marked-fail-1",
		Model:              AntigravityModelGemini,
		MarkCycleProcessed: true,
	})
	if err == nil {
		t.Fatal("expected persistence error on marked manual ping, got nil")
	}
	if resp.Success {
		t.Fatal("expected resp.Success = false")
	}
	if !strings.Contains(err.Error(), "persist marked manual ping state") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestManualPingAntigravityQuota401RetriesWithFreshAuth(t *testing.T) {
	host := newFakeHost()
	file, document := antigravityCredential("ag-401-retry-1")
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document

	host.modelExecuteFunc = func(req ModelExecuteRequest) (ModelExecuteResponse, error) {
		return ModelExecuteResponse{
			StatusCode: http.StatusOK,
			Body:       []byte(`{"type":"response.completed"}`),
		}, nil
	}

	quotaCallCount := 0
	var capturedBearerToken string
	host.httpDoFunc = func(req HTTPRequest) (HTTPResponse, error) {
		quotaCallCount++
		capturedBearerToken = req.Headers.Get("Authorization")
		if quotaCallCount == 1 {
			// First attempt fails with 401, update host auth document with fresh token
			data, _ := json.Marshal(map[string]any{
				"type":         "antigravity",
				"access_token": "token-fresh-after-refresh",
				"project_id":   "proj-ag-401-retry-1",
			})
			host.mu.Lock()
			host.docs[file.AuthIndex] = AuthDocument{AuthIndex: file.AuthIndex, Name: file.Name, JSON: data}
			host.mu.Unlock()
			return HTTPResponse{StatusCode: http.StatusUnauthorized}, nil
		}
		// Second attempt succeeds
		return HTTPResponse{
			StatusCode: http.StatusOK,
			Body: []byte(`{
				"groups": [
					{
						"displayName": "Gemini Models",
						"buckets": [
							{"bucketId": "gemini-5h", "window": "5h", "resetTime": "2026-10-14T21:00:00Z"}
						]
					}
				]
			}`),
		}, nil
	}

	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	runtime := newTestRuntime(t, host, Options{Now: func() time.Time { return now }})
	cfg := testConfig(t, runtime, "")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	resp, err := runtime.ManualPing(t.Context(), ManualPingRequest{
		CredentialID:       "ag-401-retry-1",
		Model:              AntigravityModelGemini,
		MarkCycleProcessed: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success on retried fresh auth, got %#v", resp)
	}
	if quotaCallCount < 2 {
		t.Fatalf("expected quotaCallCount >= 2, got %d", quotaCallCount)
	}
	if capturedBearerToken != "Bearer token-fresh-after-refresh" {
		t.Fatalf("captured bearer token = %q, want Bearer token-fresh-after-refresh", capturedBearerToken)
	}

	state := runtime.store.Credential("ag-401-retry-1")
	target := state.Targets[TargetKeyGemini5h]
	if target.Status != "waiting" {
		t.Fatalf("target.Status = %q, want waiting", target.Status)
	}
}
