package autoping

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDynamicPingStopsBeforeInferenceWhenStateCannotPersist(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		t.Fatal("inference must not run when in-flight state cannot persist")
		return HTTPStreamResponse{}, nil, nil
	}

	startupDelay := 24 * time.Hour
	runtime := newTestRuntime(t, host, Options{StartupDelay: &startupDelay})
	cfg := testConfig(t, runtime, "")
	cfg.StatePath = statePath
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(t.Context())

	if err := runtime.store.Update(t.Context(), file.CredentialID(), func(state *CredentialState) {
		state.Status = "waiting"
		state.ObservedResetAt = time.Now().Add(-5 * time.Hour)
		state.TargetTriggerAt = time.Now().Add(-time.Second)
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(statePath, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := runtime.runDynamicJob(t.Context(), cfg, runtime.store, file); err == nil {
		t.Fatal("expected persistence error")
	}
	if host.streamRequestCount() != 0 {
		t.Fatalf("stream requests = %d, want 0", host.streamRequestCount())
	}
}
