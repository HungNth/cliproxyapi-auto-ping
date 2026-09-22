package autoping

import (
	"fmt"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestDynamicScheduleInitialAnchorSleepsBefore0500(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newFakeHost()
		file, document := credential("codex-a", "token-a", 1)
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document

		var usageCalls atomic.Int32
		host.httpDoFunc = func(req HTTPRequest) (HTTPResponse, error) {
			usageCalls.Add(1)
			return HTTPResponse{
				StatusCode: http.StatusOK,
				Body: []byte(`{
					"rate_limit": {
						"limit_window_seconds": 18000,
						"reset_at": 1790084449
					}
				}`),
			}, nil
		}
		host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
			return successStream()
		}

		clock := &fakeClock{now: time.Date(2026, 9, 22, 4, 0, 0, 0, time.UTC)} // Exactly 04:00 UTC
		runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: new(time.Duration)})
		cfg := testConfig(t, runtime, "schedule: [\"05:00\", \"10:00\"]\ntimezone: UTC\n")
		cfg.StatePath = filepath.Join(t.TempDir(), "state.json")

		if err := runtime.Configure(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		defer runtime.Shutdown(t.Context())

		// At startup before 05:00, no usage queries should be made immediately
		synctest.Wait()
		if usageCalls.Load() != 0 {
			t.Fatalf("expected 0 usage calls before 05:00, got %d", usageCalls.Load())
		}
	})
}

func TestDynamicScheduleStartupCatchUpAfter0500QueriesUsageImmediately(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newFakeHost()
		file, document := credential("codex-a", "token-a", 1)
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document

		var usageCalls atomic.Int32
		host.httpDoFunc = func(req HTTPRequest) (HTTPResponse, error) {
			usageCalls.Add(1)
			return HTTPResponse{
				StatusCode: http.StatusOK,
				Body: []byte(`{
					"rate_limit": {
						"limit_window_seconds": 18000,
						"reset_at": 1790084449
					}
				}`),
			}, nil
		}
		host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
			return successStream()
		}

		clock := &fakeClock{now: time.Date(2026, 9, 22, 6, 0, 0, 0, time.UTC)} // Exactly 06:00 UTC (past 05:00)
		runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: new(time.Duration)})
		cfg := testConfig(t, runtime, "schedule: [\"05:00\", \"10:00\"]\ntimezone: UTC\n")
		cfg.StatePath = filepath.Join(t.TempDir(), "state.json")

		if err := runtime.Configure(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		defer runtime.Shutdown(t.Context())

		time.Sleep(5 * time.Second)
		synctest.Wait()

		if usageCalls.Load() == 0 {
			t.Fatalf("expected immediate catch-up usage query when starting after 05:00, got 0 calls")
		}

		state := runtime.store.Credential("codex-a")
		if state.ObservedResetAt.Unix() != 1790084449 {
			t.Fatalf("observed_reset_at = %d, want 1790084449", state.ObservedResetAt.Unix())
		}
	})
}

func TestDynamicScheduleTransientUsageFailureEntersCooldownWithoutBlindPing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newFakeHost()
		file, document := credential("codex-a", "token-a", 1)
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document

		host.httpDoFunc = func(req HTTPRequest) (HTTPResponse, error) {
			return HTTPResponse{StatusCode: http.StatusInternalServerError}, nil
		}
		var streamCalls atomic.Int32
		host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
			streamCalls.Add(1)
			return successStream()
		}

		clock := &fakeClock{now: time.Date(2026, 9, 22, 5, 0, 0, 0, time.UTC)}
		runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: new(time.Duration)})
		cfg := testConfig(t, runtime, "schedule: [\"05:00\"]\ntimezone: UTC\nretry_cooldown: 1m\n")
		cfg.StatePath = filepath.Join(t.TempDir(), "state.json")

		if err := runtime.Configure(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		defer runtime.Shutdown(t.Context())

		synctest.Wait()
		if streamCalls.Load() != 0 {
			t.Fatalf("expected 0 inference stream pings on usage failure, got %d", streamCalls.Load())
		}

		state := runtime.store.Credential("codex-a")
		if state.Status != "failed" && state.Status != "cooldown" {
			t.Fatalf("state status = %q, want failed or cooldown", state.Status)
		}
		if state.NextRetryAt.IsZero() {
			t.Fatalf("expected NextRetryAt to be set for retryable usage error")
		}
	})
}

func TestDynamicScheduleTerminalAuthFailureStopsRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newFakeHost()
		file, document := credential("codex-a", "token-a", 1)
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document

		host.httpDoFunc = func(req HTTPRequest) (HTTPResponse, error) {
			return HTTPResponse{StatusCode: http.StatusUnauthorized}, nil
		}
		var streamCalls atomic.Int32
		host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
			streamCalls.Add(1)
			return successStream()
		}

		clock := &fakeClock{now: time.Date(2026, 9, 22, 5, 0, 0, 0, time.UTC)}
		runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: new(time.Duration)})
		cfg := testConfig(t, runtime, "schedule: [\"05:00\"]\ntimezone: UTC\nretry_cooldown: 1m\n")
		cfg.StatePath = filepath.Join(t.TempDir(), "state.json")

		if err := runtime.Configure(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		defer runtime.Shutdown(t.Context())

		synctest.Wait()
		if streamCalls.Load() != 0 {
			t.Fatalf("expected 0 inference stream pings on auth failure, got %d", streamCalls.Load())
		}

		state := runtime.store.Credential("codex-a")
		if state.Status != "blocked" {
			t.Fatalf("state status = %q, want blocked", state.Status)
		}
		if state.FailureKind != "auth" {
			t.Fatalf("failure kind = %q, want auth", state.FailureKind)
		}
		if !state.NextRetryAt.IsZero() {
			t.Fatalf("expected NextRetryAt to be zero for terminal auth failure, got %v", state.NextRetryAt)
		}
	})
}

func TestDynamicScheduleTerminalAuthFailureResetsAtNextAnchor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newFakeHost()
		file, document := credential("codex-a", "token-a", 1)
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document

		var usageCallCount atomic.Int32
		host.httpDoFunc = func(req HTTPRequest) (HTTPResponse, error) {
			count := usageCallCount.Add(1)
			if count == 1 {
				return HTTPResponse{StatusCode: http.StatusUnauthorized}, nil
			}
			return HTTPResponse{
				StatusCode: http.StatusOK,
				Body: []byte(`{
					"rate_limit": {
						"limit_window_seconds": 18000,
						"reset_at": 1790084449
					}
				}`),
			}, nil
		}
		host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
			return successStream()
		}

		clock := &fakeClock{now: time.Date(2026, 9, 22, 5, 0, 0, 0, time.UTC)}
		runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: new(time.Duration)})
		cfg := testConfig(t, runtime, "schedule: [\"05:00\", \"10:00\"]\ntimezone: UTC\n")
		cfg.StatePath = filepath.Join(t.TempDir(), "state.json")

		if err := runtime.Configure(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		defer runtime.Shutdown(t.Context())

		time.Sleep(5 * time.Second)
		synctest.Wait()

		state := runtime.store.Credential("codex-a")
		if state.FailureKind != "auth" {
			t.Fatalf("expected FailureKind = auth, got %q", state.FailureKind)
		}
		if state.Status != "blocked" {
			t.Fatalf("expected status = blocked, got %q", state.Status)
		}

		// Advance clock past next scheduled milestone anchor (10:00 UTC)
		clock.Advance(5 * time.Hour)
		time.Sleep(5 * time.Hour)
		synctest.Wait()

		state = runtime.store.Credential("codex-a")
		if state.FailureKind != "" {
			t.Fatalf("expected FailureKind to be cleared at next anchor, got %q", state.FailureKind)
		}
		if state.ObservedResetAt.Unix() != 1790084449 {
			t.Fatalf("observed_reset_at = %d, want 1790084449", state.ObservedResetAt.Unix())
		}
	})
}

func TestDynamicScheduleDispatchesAtTargetAndStabilizes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newFakeHost()
		file, document := credential("codex-a", "token-a", 1)
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document

		baseTime := time.Date(2026, 9, 22, 5, 0, 0, 0, time.UTC)
		clock := &fakeClock{now: baseTime}

		firstReset := baseTime.Unix()
		secondReset := baseTime.Unix() + 18000

		var usageCallCount atomic.Int32
		host.httpDoFunc = func(req HTTPRequest) (HTTPResponse, error) {
			count := usageCallCount.Add(1)
			reset := firstReset
			if count > 1 {
				reset = secondReset
			}
			return HTTPResponse{
				StatusCode: http.StatusOK,
				Body: []byte(fmt.Sprintf(`{
					"rate_limit": {
						"limit_window_seconds": 18000,
						"reset_at": %d
					}
				}`, reset)),
			}, nil
		}

		var streamCalls atomic.Int32
		host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
			streamCalls.Add(1)
			return successStream()
		}

		runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: new(time.Duration)})
		cfg := testConfig(t, runtime, "schedule: [\"05:00\"]\ntimezone: UTC\n")
		cfg.StatePath = filepath.Join(t.TempDir(), "state.json")

		if err := runtime.Configure(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		defer runtime.Shutdown(t.Context())
		time.Sleep(5 * time.Second)
		synctest.Wait()
		clock.Advance(35 * time.Second)
		time.Sleep(35 * time.Second)
		synctest.Wait()
		if streamCalls.Load() != 1 {
			t.Fatalf("expected 1 stream ping, got %d", streamCalls.Load())
		}
		state := runtime.store.Credential("codex-a")
		if state.Successes != 1 {
			t.Fatalf("successes = %d, want 1", state.Successes)
		}
		if state.LastAttemptStatus != "success" {
			t.Fatalf("last_attempt_status = %q, want success", state.LastAttemptStatus)
		}
		if state.ObservedResetAt.Unix() != secondReset {
			t.Fatalf("observed_reset_at = %d, want %d", state.ObservedResetAt.Unix(), secondReset)
		}
	})
}

func TestDynamicScheduleStaleStabilizationRetriesUsageOnlyAndAdvancesCycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newFakeHost()
		file, document := credential("codex-a", "token-a", 1)
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document

		baseTime := time.Date(2026, 9, 22, 5, 0, 0, 0, time.UTC)
		clock := &fakeClock{now: baseTime}

		firstReset := baseTime.Unix()
		secondReset := baseTime.Unix() + 18000

		var usageCallCount atomic.Int32
		host.httpDoFunc = func(req HTTPRequest) (HTTPResponse, error) {
			count := usageCallCount.Add(1)
			reset := firstReset
			if count > 4 {
				reset = secondReset
			}
			return HTTPResponse{
				StatusCode: http.StatusOK,
				Body: []byte(fmt.Sprintf(`{
					"rate_limit": {
						"limit_window_seconds": 18000,
						"reset_at": %d
					}
				}`, reset)),
			}, nil
		}

		var streamCalls atomic.Int32
		host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
			streamCalls.Add(1)
			return successStream()
		}

		runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: new(time.Duration)})
		cfg := testConfig(t, runtime, "schedule: [\"05:00\"]\ntimezone: UTC\nretry_cooldown: 10s\n")
		cfg.StatePath = filepath.Join(t.TempDir(), "state.json")

		if err := runtime.Configure(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		defer runtime.Shutdown(t.Context())
		time.Sleep(5 * time.Second)
		synctest.Wait()
		clock.Advance(35 * time.Second)
		time.Sleep(35 * time.Second)
		synctest.Wait()
		if streamCalls.Load() != 1 {
			t.Fatalf("expected exactly 1 stream ping, got %d", streamCalls.Load())
		}
		state := runtime.store.Credential("codex-a")
		if state.Status != "stabilizing" {
			t.Fatalf("expected state status = stabilizing after stale reset, got %q", state.Status)
		}
		if !state.TargetTriggerAt.IsZero() {
			t.Fatalf("expected TargetTriggerAt to remain zero while stabilizing, got %v", state.TargetTriggerAt)
		}

		clock.Advance(20 * time.Second)
		time.Sleep(20 * time.Second)
		synctest.Wait()

		if streamCalls.Load() != 1 {
			t.Fatalf("stale stabilization retry duplicated the ping: stream calls = %d, want 1", streamCalls.Load())
		}

		state = runtime.store.Credential("codex-a")
		if state.Status != "waiting" {
			t.Fatalf("expected state status = waiting after advanced reset, got %q", state.Status)
		}
		if state.ObservedResetAt.Unix() != secondReset {
			t.Fatalf("observed_reset_at = %d, want %d", state.ObservedResetAt.Unix(), secondReset)
		}
		if state.TargetTriggerAt.Unix() != secondReset+30 {
			t.Fatalf("target_trigger_at = %d, want %d", state.TargetTriggerAt.Unix(), secondReset+30)
		}
	})
}
