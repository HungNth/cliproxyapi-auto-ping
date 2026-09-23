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

func TestDynamicScheduleLastMilestoneOfDayWaitsForNextDayInitialAnchor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newFakeHost()
		file, document := credential("codex-a", "token-a", 1)
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document

		baseTime := time.Date(2026, 9, 22, 20, 0, 0, 0, time.UTC)
		clock := &fakeClock{now: baseTime}

		reset2000 := baseTime.Unix()
		reset0100 := baseTime.Add(5 * time.Hour).Unix() // 2026-09-23 01:00:00 UTC
		reset1000 := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC).Unix()

		var usageCallCount atomic.Int32
		host.httpDoFunc = func(req HTTPRequest) (HTTPResponse, error) {
			count := usageCallCount.Add(1)
			reset := reset0100
			if count > 2 {
				reset = reset1000
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

		statePath := filepath.Join(t.TempDir(), "state.json")
		store, err := LoadStateStore(t.Context(), statePath)
		if err != nil {
			t.Fatal(err)
		}
		// Pre-anchor to 20:00 milestone (from earlier 15:00 window)
		if err := store.Update(t.Context(), "codex-a", func(state *CredentialState) {
			state.Status = "waiting"
			state.ObservedResetAt = time.Unix(reset2000, 0).UTC()
			state.TargetTriggerAt = time.Unix(reset2000+30, 0).UTC()
		}); err != nil {
			t.Fatal(err)
		}

		runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: new(time.Duration)})
		cfg := testConfig(t, runtime, "schedule: [\"05:00\", \"10:00\", \"15:00\", \"20:00\"]\ntimezone: UTC\n")
		cfg.StatePath = statePath

		if err := runtime.Configure(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		defer runtime.Shutdown(t.Context())

		// Initial discovery and ping at 20:00
		time.Sleep(5 * time.Second)
		synctest.Wait()
		clock.Advance(35 * time.Second)
		time.Sleep(35 * time.Second)
		synctest.Wait()

		if streamCalls.Load() != 1 {
			t.Fatalf("expected 1 stream ping at 20:00 milestone, got %d", streamCalls.Load())
		}

		// Advance to 01:00:30 (when the 5h window from 20:00 expires).
		// Since 20:00 was the last milestone of the day, no ping should occur overnight at 01:00.
		clock.Advance(5*time.Hour + 30*time.Second)
		time.Sleep(5*time.Hour + 30*time.Second)
		synctest.Wait()

		if streamCalls.Load() != 1 {
			t.Fatalf("expected 0 pings overnight at 01:00 after last milestone of day, got %d stream calls", streamCalls.Load())
		}

		// Advance to 05:00 of the next day (the mandatory initial daily milestone anchor).
		clock.Advance(3*time.Hour + 59*time.Minute + 30*time.Second)
		time.Sleep(3*time.Hour + 59*time.Minute + 30*time.Second)
		synctest.Wait()

		if streamCalls.Load() != 2 {
			t.Fatalf("expected mandatory ping at 05:00 next day, got %d stream calls", streamCalls.Load())
		}
	})
}

func TestDynamicScheduleMandatory0500PingOverridesLaterTarget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newFakeHost()
		file, document := credential("codex-a", "token-a", 1)
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document

		// Clock starts at exactly 05:00 UTC
		baseTime := time.Date(2026, 9, 23, 5, 0, 0, 0, time.UTC)
		clock := &fakeClock{now: baseTime}

		reset1000 := baseTime.Add(5 * time.Hour).Unix() // 10:00 UTC

		host.httpDoFunc = func(req HTTPRequest) (HTTPResponse, error) {
			return HTTPResponse{
				StatusCode: http.StatusOK,
				Body: []byte(fmt.Sprintf(`{
					"rate_limit": {
						"limit_window_seconds": 18000,
						"reset_at": %d
					}
				}`, reset1000)),
			}, nil
		}

		var streamCalls atomic.Int32
		host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
			streamCalls.Add(1)
			return successStream()
		}

		statePath := filepath.Join(t.TempDir(), "state.json")
		store, err := LoadStateStore(t.Context(), statePath)
		if err != nil {
			t.Fatal(err)
		}
		// Pre-populate state where a previous overnight ping set TargetTriggerAt to 06:00 UTC
		if err := store.Update(t.Context(), "codex-a", func(state *CredentialState) {
			state.Status = "waiting"
			state.LastPingAt = time.Date(2026, 9, 23, 1, 0, 0, 0, time.UTC) // prior 01:00 ping before 05:00
			state.ObservedResetAt = time.Date(2026, 9, 23, 6, 0, 0, 0, time.UTC)
			state.TargetTriggerAt = time.Date(2026, 9, 23, 6, 0, 30, 0, time.UTC) // 06:00:30 UTC
		}); err != nil {
			t.Fatal(err)
		}

		runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: new(time.Duration)})
		cfg := testConfig(t, runtime, "schedule: [\"05:00\", \"10:00\", \"15:00\", \"20:00\"]\ntimezone: UTC\n")
		cfg.StatePath = statePath

		if err := runtime.Configure(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		defer runtime.Shutdown(t.Context())

		// At 05:00 UTC, the mandatory daily anchor ping must execute immediately, NOT wait for 06:00
		time.Sleep(5 * time.Second)
		synctest.Wait()

		if streamCalls.Load() != 1 {
			t.Fatalf("expected mandatory ping at 05:00 UTC despite TargetTriggerAt being 06:00, got %d stream calls", streamCalls.Load())
		}

		state := runtime.store.Credential("codex-a")
		if state.LastAttemptStatus != "success" {
			t.Fatalf("expected last attempt status = success, got %q", state.LastAttemptStatus)
		}
		// Target should now be dynamic reset from the 05:00 ping (around 10:00:30)
		if state.TargetTriggerAt.Unix() != reset1000+30 {
			t.Fatalf("expected new target to be 10:00:30 (%d), got %d", reset1000+30, state.TargetTriggerAt.Unix())
		}
	})
}


