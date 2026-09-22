package autoping

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestManualPingMarkedCycleRejectsStaleUsageObservation(t *testing.T) {
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		return successStream()
	}
	host.httpDoFunc = func(HTTPRequest) (HTTPResponse, error) {
		return HTTPResponse{
			StatusCode: http.StatusOK,
			Body:       []byte(`{"rate_limit":{"limit_window_seconds":18000,"reset_at":1790084449}}`),
		}, nil
	}

	startupDelay := 24 * time.Hour
	runtime := newTestRuntime(t, host, Options{StartupDelay: &startupDelay})
	cfg := testConfig(t, runtime, "retry_cooldown: 1m\n")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(t.Context())

	priorReset := time.Unix(1790084449, 0).UTC()
	if err := runtime.store.Update(t.Context(), file.CredentialID(), func(state *CredentialState) {
		state.Status = "waiting"
		state.ObservedResetAt = priorReset
		state.TargetTriggerAt = priorReset.Add(30 * time.Second)
	}); err != nil {
		t.Fatal(err)
	}

	response, err := runtime.ManualPing(t.Context(), ManualPingRequest{
		CredentialID:       file.CredentialID(),
		MarkCycleProcessed: true,
	})
	if err == nil || response.Success {
		t.Fatalf("stale usage must not report anchored success: response=%#v err=%v", response, err)
	}

	state := runtime.store.Credential(file.CredentialID())
	if state.Status != "stabilizing" || state.FailureKind != string(FailureRetryable) {
		t.Fatalf("stale marked ping state = %#v", state)
	}
	if !state.TargetTriggerAt.IsZero() {
		t.Fatalf("stale usage retained a due trigger: %v", state.TargetTriggerAt)
	}
	if state.NextRetryAt.IsZero() {
		t.Fatal("stale usage did not schedule re-observation")
	}
}
