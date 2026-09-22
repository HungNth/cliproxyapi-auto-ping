package autoping

import (
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestManualPingMarkedCycleRejectsStaleUsageWhenUnanchored(t *testing.T) {
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		return successStream()
	}

	// Usage endpoint returns a reset timestamp from an expired window in the past
	stalePastReset := time.Now().Add(-10 * time.Minute).Unix()
	host.httpDoFunc = func(HTTPRequest) (HTTPResponse, error) {
		return HTTPResponse{
			StatusCode: http.StatusOK,
			Body:       []byte(fmt.Sprintf(`{"rate_limit":{"limit_window_seconds":18000,"reset_at":%d}}`, stalePastReset)),
		}, nil
	}

	runtime := newTestRuntime(t, host, Options{})
	cfg := testConfig(t, runtime, "retry_cooldown: 1m\n")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(t.Context())

	response, err := runtime.ManualPing(t.Context(), ManualPingRequest{
		CredentialID:       file.CredentialID(),
		MarkCycleProcessed: true,
	})
	if err == nil || response.Success {
		t.Fatalf("unanchored marked ping with past reset must report error: response=%#v, err=%v", response, err)
	}

	state := runtime.store.Credential(file.CredentialID())
	if state.Status != "stabilizing" {
		t.Fatalf("expected state.Status = stabilizing, got %q", state.Status)
	}
	if !state.TargetTriggerAt.IsZero() {
		t.Fatalf("unanchored marked ping with past reset must not anchor target: %v", state.TargetTriggerAt)
	}
	if state.NextRetryAt.IsZero() {
		t.Fatal("expected NextRetryAt to be set for retryable stabilization")
	}
}
