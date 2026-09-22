package autoping

import (
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestDispatchDueDynamicJobsHonorsMaxConcurrency(t *testing.T) {
	host := newFakeHost()
	for _, id := range []string{"codex-a", "codex-b"} {
		file, document := credential(id, "token-"+id, 1)
		host.auths = append(host.auths, file)
		host.docs[file.AuthIndex] = document
	}

	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var active atomic.Int32
	var maximum atomic.Int32
	host.httpDoFunc = func(HTTPRequest) (HTTPResponse, error) {
		current := active.Add(1)
		for {
			prior := maximum.Load()
			if current <= prior || maximum.CompareAndSwap(prior, current) {
				break
			}
		}
		entered <- struct{}{}
		<-release
		active.Add(-1)
		return HTTPResponse{
			StatusCode: http.StatusOK,
			Body:       []byte(`{"rate_limit":{"limit_window_seconds":18000,"reset_at":1790084449}}`),
		}, nil
	}

	startupDelay := 24 * time.Hour
	runtime := newTestRuntime(t, host, Options{StartupDelay: &startupDelay})
	cfg := testConfig(t, runtime, "max_concurrency: 2\n")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(t.Context())

	done := make(chan struct{})
	go func() {
		runtime.dispatchDueDynamicJobs(t.Context(), cfg, runtime.store, host.auths)
		close(done)
	}()

	<-entered
	<-entered
	close(release)
	<-done

	if maximum.Load() != 2 {
		t.Fatalf("maximum concurrent usage requests = %d, want 2", maximum.Load())
	}
}
