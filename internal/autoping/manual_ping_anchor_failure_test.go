package autoping

import (
	"net/http"
	"path/filepath"
	"testing"
)

func TestManualPingMarkedCycleReportsAnchorFailureAndPersistsStabilization(t *testing.T) {
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		return successStream()
	}
	host.httpDoFunc = func(HTTPRequest) (HTTPResponse, error) {
		return HTTPResponse{StatusCode: http.StatusServiceUnavailable}, nil
	}

	runtime := newTestRuntime(t, host, Options{})
	cfg := testConfig(t, runtime, "retry_cooldown: 1m\n")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	response, err := runtime.ManualPing(t.Context(), ManualPingRequest{
		CredentialID:       "codex-a",
		MarkCycleProcessed: true,
	})
	if err == nil {
		t.Fatal("expected quota anchor error")
	}
	if response.Success {
		t.Fatalf("response must not report anchored success: %#v", response)
	}

	state := runtime.store.Credential("codex-a")
	if state.LastAttemptStatus != "success" || state.Successes != 1 {
		t.Fatalf("inference outcome was not preserved: %#v", state)
	}
	if state.Status != "stabilizing" || state.Reason != "usage_stabilization_retry" {
		t.Fatalf("anchor failure did not enter stabilization: %#v", state)
	}
	if state.FailureKind != string(FailureRetryable) || state.NextRetryAt.IsZero() {
		t.Fatalf("anchor retry state = %#v", state)
	}
	if !state.TargetTriggerAt.IsZero() {
		t.Fatalf("failed anchor must not set target: %v", state.TargetTriggerAt)
	}
}
