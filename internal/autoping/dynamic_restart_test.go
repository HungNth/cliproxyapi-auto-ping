package autoping

import (
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestDynamicScheduleRestoresPersistedTargetBeforeInitialAnchor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clock := &fakeClock{now: time.Date(2026, 9, 22, 3, 0, 0, 0, time.UTC)}
		statePath := filepath.Join(t.TempDir(), "state.json")
		store, err := LoadStateStore(t.Context(), statePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Update(t.Context(), "codex-a", func(state *CredentialState) {
			state.Status = "waiting"
			state.ObservedResetAt = clock.Now().Add(-time.Hour)
			state.TargetTriggerAt = clock.Now().Add(time.Hour)
		}); err != nil {
			t.Fatal(err)
		}

		host := newFakeHost()
		file, document := credential("codex-a", "token-a", 1)
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document
		var pings atomic.Int32
		host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
			pings.Add(1)
			return successStream()
		}
		host.httpDoFunc = func(HTTPRequest) (HTTPResponse, error) {
			return HTTPResponse{
				StatusCode: http.StatusOK,
				Body:       []byte(`{"rate_limit":{"limit_window_seconds":18000,"reset_at":1790084449}}`),
			}, nil
		}

		runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: new(time.Duration)})
		cfg := testConfig(t, runtime, "schedule: [\"05:00\"]\ntimezone: UTC\nstate_path: "+statePath+"\n")
		if err := runtime.Configure(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		defer runtime.Shutdown(t.Context())
		synctest.Wait()
		if pings.Load() != 0 {
			t.Fatalf("pings before persisted target = %d", pings.Load())
		}

		clock.Advance(time.Hour)
		time.Sleep(time.Hour)
		synctest.Wait()
		if pings.Load() != 1 {
			t.Fatalf("pings at persisted 04:00 target = %d, want 1", pings.Load())
		}
	})
}
