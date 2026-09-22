package autoping

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDisabledAutoPingSendsNoRequests(t *testing.T) {
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document
	host.httpDoFunc = func(HTTPRequest) (HTTPResponse, error) {
		t.Fatal("disabled auto-ping must not observe usage")
		return HTTPResponse{}, nil
	}
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		t.Fatal("disabled auto-ping must not send inference requests")
		return HTTPStreamResponse{}, nil, nil
	}

	startupDelay := new(time.Duration)
	runtime := newTestRuntime(t, host, Options{StartupDelay: startupDelay})
	cfg := testConfig(t, runtime, "auto_ping_disabled: true\n")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(t.Context())

	time.Sleep(10 * time.Millisecond)
	if len(host.httpRequests) != 0 || host.streamRequestCount() != 0 {
		t.Fatalf("disabled auto-ping made requests: usage=%d inference=%d", len(host.httpRequests), host.streamRequestCount())
	}
}
