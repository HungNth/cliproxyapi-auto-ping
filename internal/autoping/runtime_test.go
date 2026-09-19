package autoping

import (
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"
)

func TestScheduledMilestoneDispatchMultipleAccountsAndSurvivesRestart(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 9, 18, 5, 0, 0, 0, time.UTC)}
	host := newFakeHost()
	for _, id := range []string{"codex-a", "codex-b", "codex-c"} {
		file, document := credential(id, "token-"+id, 1)
		host.auths = append(host.auths, file)
		host.docs[file.AuthIndex] = document
	}
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		return successStream()
	}

	startupDelay := 24 * time.Hour
	runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: &startupDelay})
	cfg := testConfig(t, runtime, "max_concurrency: 2\nscheduler_boost_fallback: false\ntimezone: UTC\n")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Shutdown(t.Context()) })

	milestoneKey := "2026-09-18#05:00"
	if err := runtime.DispatchMilestone(t.Context(), milestoneKey, clock.Now()); err != nil {
		t.Fatal(err)
	}
	if host.streamRequestCount() != 3 {
		t.Fatalf("pings = %d, want 3", host.streamRequestCount())
	}
	if runtime.store.LastProcessedMilestone() != milestoneKey {
		t.Fatalf("global last milestone = %q, want %q", runtime.store.LastProcessedMilestone(), milestoneKey)
	}
	for _, id := range []string{"codex-a", "codex-b", "codex-c"} {
		state := runtime.store.Credential(id)
		if state.LastProcessedMilestone != milestoneKey || state.LastAttemptStatus != "success" {
			t.Fatalf("credential %s state = %#v", id, state)
		}
	}

	// Idempotent: dispatching same milestone again does not duplicate
	if err := runtime.DispatchMilestone(t.Context(), milestoneKey, clock.Now()); err != nil {
		t.Fatal(err)
	}
	if host.streamRequestCount() != 3 {
		t.Fatalf("duplicate milestone dispatch sent another ping: %d", host.streamRequestCount())
	}

	// Survives restart without duplicate pings
	if err := runtime.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	restarted := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: &startupDelay})
	if err := restarted.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	defer restarted.Shutdown(t.Context())
	if err := restarted.DispatchMilestone(t.Context(), milestoneKey, clock.Now()); err != nil {
		t.Fatal(err)
	}
	if host.streamRequestCount() != 3 {
		t.Fatalf("restart duplicated a processed milestone: %d", host.streamRequestCount())
	}

	// Advancing to next milestone dispatches new cycle
	clock.Advance(5 * time.Hour) // 10:00
	nextMilestone := "2026-09-18#10:00"
	if err := restarted.DispatchMilestone(t.Context(), nextMilestone, clock.Now()); err != nil {
		t.Fatal(err)
	}
	if host.streamRequestCount() != 6 {
		t.Fatalf("total pings = %d, want 6", host.streamRequestCount())
	}
}

func TestScheduleLoopPreservesMilestonesWhileBusy(t *testing.T) {
	for _, retry := range []bool{false, true} {
		name := "slow_batch"
		if retry {
			name = "slow_retry"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				host := newFakeHost()
				for _, id := range []string{"codex-a", "codex-b"} {
					file, document := credential(id, "token-"+id, 1)
					host.auths = append(host.auths, file)
					host.docs[file.AuthIndex] = document
				}
				requests := map[string]int{}
				host.httpStreamFunc = func(request HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
					id := request.Headers.Get("ChatGPT-Account-ID")
					requests[id]++
					if retry && id == "account-codex-a" && requests[id] == 1 {
						return HTTPStreamResponse{StatusCode: http.StatusInternalServerError}, []HTTPStreamChunk{{Done: true}}, nil
					}
					if !retry || (id == "account-codex-a" && requests[id] == 2) {
						time.Sleep(40 * time.Second)
					}
					return successStream()
				}
				runtime := newTestRuntime(t, host, Options{StartupDelay: new(time.Duration)})
				cfg := testConfig(t, runtime, "schedule: [\"00:01\", \"00:02\", \"05:00\"]\ntimezone: UTC\nmax_concurrency: 1\nretry_cooldown: 30s\nscheduler_boost_fallback: false\n")
				cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
				if err := runtime.Configure(t.Context(), cfg); err != nil {
					t.Fatal(err)
				}
				defer runtime.Shutdown(t.Context())

				// Exercise the real timers, workers and retry loop; no direct dispatch calls.
				time.Sleep(time.Minute)
				synctest.Wait()
				if retry {
					failed := runtime.store.Credential("codex-a")
					next := runtime.store.Credential("codex-b")
					if failed.Failures != 1 || failed.Successes != 0 || next.LastProcessedMilestone != "2000-01-01#00:01" || next.Successes != 1 {
						t.Fatalf("first failure blocked the next credential: first failures=%d successes=%d; next milestone=%q successes=%d", failed.Failures, failed.Successes, next.LastProcessedMilestone, next.Successes)
					}
				}
				time.Sleep(3 * time.Minute)
				synctest.Wait()
				for _, id := range []string{"codex-a", "codex-b"} {
					state := runtime.store.Credential(id)
					if state.LastProcessedMilestone != "2000-01-01#00:02" || state.Successes != 2 {
						t.Errorf("%s: processed %q, successes=%d; want both 00:01 and 00:02 milestones", id, state.LastProcessedMilestone, state.Successes)
					}
				}
			})
		})
	}
}

func TestScheduleLoopRetriesUnavailableCredentialMaterial(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newFakeHost()
		file, document := credential("codex-a", "token-a", 1)
		host.auths = []AuthFile{file}
		host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
			return successStream()
		}
		runtime := newTestRuntime(t, host, Options{StartupDelay: new(time.Duration)})
		cfg := testConfig(t, runtime, "schedule: [\"00:01\", \"05:00\"]\ntimezone: UTC\nretry_cooldown: 30s\n")
		cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
		if err := runtime.Configure(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		defer runtime.Shutdown(t.Context())

		time.Sleep(time.Minute)
		synctest.Wait()
		// The host can read the same credential again; no token refresh occurred.
		host.mu.Lock()
		host.docs[file.AuthIndex] = document
		host.mu.Unlock()
		time.Sleep(time.Minute)
		synctest.Wait()
		state := runtime.store.Credential(file.ID)
		if state.LastProcessedMilestone != "2000-01-01#00:01" || state.Successes != 1 {
			t.Fatalf("recovered credential was not pinged: milestone=%q, successes=%d, reason=%s", state.LastProcessedMilestone, state.Successes, state.Reason)
		}
	})
}

func TestScheduleLoopWaitsForManualPing(t *testing.T) {
	for _, mark := range []bool{false, true} {
		name := "unmarked"
		if mark {
			name = "marked"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				host := newFakeHost()
				file, document := credential("codex-a", "token-a", 1)
				host.auths = []AuthFile{file}
				host.docs[file.AuthIndex] = document
				host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
					time.Sleep(40 * time.Second)
					return successStream()
				}
				runtime := newTestRuntime(t, host, Options{StartupDelay: new(time.Duration)})
				cfg := testConfig(t, runtime, "schedule: [\"00:01\", \"05:00\"]\ntimezone: UTC\n")
				cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
				if err := runtime.Configure(t.Context(), cfg); err != nil {
					t.Fatal(err)
				}
				defer runtime.Shutdown(t.Context())

				time.Sleep(40 * time.Second)
				go func() {
					if _, err := runtime.ManualPing(t.Context(), ManualPingRequest{CredentialID: file.ID, MarkCycleProcessed: mark}); err != nil {
						t.Errorf("manual ping: %v", err)
					}
				}()
				time.Sleep(2 * time.Minute)
				synctest.Wait()
				want := uint64(2)
				if mark {
					want = 1
				}
				state := runtime.store.Credential(file.ID)
				if state.LastProcessedMilestone != "2000-01-01#00:01" || state.Successes != want {
					t.Fatalf("milestone overlapping manual ping: processed=%q, successes=%d; want 00:01, %d", state.LastProcessedMilestone, state.Successes, want)
				}
			})
		})
	}
}

func TestScheduleLoopReconfigurationCancelsOldTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newFakeHost()
		file, document := credential("codex-a", "token-a", 1)
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document
		host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
			return successStream()
		}
		runtime := newTestRuntime(t, host, Options{StartupDelay: new(time.Duration)})
		cfg := testConfig(t, runtime, "schedule: [\"00:01\", \"05:00\"]\ntimezone: UTC\n")
		cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
		if err := runtime.Configure(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		defer runtime.Shutdown(t.Context())
		synctest.Wait()

		updated := testConfig(t, runtime, "schedule: [\"07:02\", \"07:03\", \"07:04\"]\ntimezone: Asia/Ho_Chi_Minh\n")
		updated.StatePath = cfg.StatePath
		if err := runtime.Configure(t.Context(), updated); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		if host.streamRequestCount() != 0 {
			t.Fatal("obsolete UTC milestone still dispatched after reconfiguration")
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		if state := runtime.store.Credential(file.ID); state.LastProcessedMilestone != "2000-01-01#07:02" {
			t.Fatalf("new timezone/schedule did not dispatch: %q", state.LastProcessedMilestone)
		}
		updated.AutoPingEnabled = false
		if err := runtime.Configure(t.Context(), updated); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		if count := host.streamRequestCount(); count != 1 {
			t.Fatalf("disabled timer dispatched: requests=%d", count)
		}
		updated.AutoPingEnabled = true
		if err := runtime.Configure(t.Context(), updated); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		if state := runtime.store.Credential(file.ID); state.LastProcessedMilestone != "2000-01-01#07:04" || state.Successes != 2 {
			t.Fatalf("re-enabled schedule did not resume: processed=%q, successes=%d", state.LastProcessedMilestone, state.Successes)
		}
	})
}

func TestScheduleLoopRetriesCredentialListing(t *testing.T) {
	for _, milestone := range []string{"00:00", "00:01"} {
		t.Run(milestone, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				host := newFakeHost()
				host.listErr = errors.New("host temporarily unavailable")
				file, document := credential("codex-a", "token-a", 1)
				host.auths = []AuthFile{file}
				host.docs[file.AuthIndex] = document
				host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
					return successStream()
				}
				runtime := newTestRuntime(t, host, Options{StartupDelay: new(time.Duration)})
				cfg := testConfig(t, runtime, "schedule: [\""+milestone+"\", \"05:00\"]\ntimezone: UTC\nretry_cooldown: 30s\n")
				cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
				if err := runtime.Configure(t.Context(), cfg); err != nil {
					t.Fatal(err)
				}
				defer runtime.Shutdown(t.Context())
				time.Sleep(time.Minute)
				synctest.Wait()
				host.mu.Lock()
				host.listErr = nil
				host.mu.Unlock()
				time.Sleep(time.Minute)
				synctest.Wait()
				state := runtime.store.Credential(file.ID)
				if state.LastProcessedMilestone != "2000-01-01#"+milestone || state.Successes != 1 {
					t.Fatalf("milestone lost after listing recovered: processed=%q, successes=%d", state.LastProcessedMilestone, state.Successes)
				}
			})
		})
	}
}

func TestScheduleLoopReconfigurationCancelsQueuedCredentials(t *testing.T) {
	for _, milestone := range []string{"00:01", "00:02"} {
		t.Run(milestone, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				host := newFakeHost()
				for _, id := range []string{"codex-a", "codex-b"} {
					file, document := credential(id, "token-"+id, 1)
					host.auths = append(host.auths, file)
					host.docs[file.AuthIndex] = document
				}
				host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
					time.Sleep(40 * time.Second)
					return successStream()
				}
				runtime := newTestRuntime(t, host, Options{StartupDelay: new(time.Duration)})
				cfg := testConfig(t, runtime, "schedule: [\"00:01\", \"05:00\"]\ntimezone: UTC\nmax_concurrency: 1\n")
				cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
				if err := runtime.Configure(t.Context(), cfg); err != nil {
					t.Fatal(err)
				}
				defer runtime.Shutdown(t.Context())
				time.Sleep(70 * time.Second)
				synctest.Wait()
				updated := testConfig(t, runtime, "schedule: [\""+milestone+"\", \"05:00\"]\ntimezone: UTC\nmax_concurrency: 1\nretry_cooldown: 30s\n")
				updated.StatePath = cfg.StatePath
				if err := runtime.Configure(t.Context(), updated); err != nil {
					t.Fatal(err)
				}
				time.Sleep(3 * time.Minute)
				synctest.Wait()
				for _, id := range []string{"codex-a", "codex-b"} {
					if state := runtime.store.Credential(id); state.LastProcessedMilestone != "2000-01-01#"+milestone {
						t.Errorf("%s missed reconfigured milestone: %q", id, state.LastProcessedMilestone)
					}
				}
				if count := host.streamRequestCount(); count != 3 {
					t.Errorf("queued credential ran under canceled config: requests=%d, want one old in-flight plus two new", count)
				}
			})
		})
	}
}

func TestScheduledMilestoneSkipsExcludedAndIneligibleCredentials(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 9, 18, 5, 0, 0, 0, time.UTC)}
	host := newFakeHost()
	active, docActive := credential("codex-active", "token-active", 1)
	excluded, docExcluded := credential("codex-excluded", "token-excluded", 1)
	disabled, docDisabled := credential("codex-disabled", "token-disabled", 1)
	disabled.Disabled = true

	host.auths = []AuthFile{active, excluded, disabled}
	host.docs[active.AuthIndex] = docActive
	host.docs[excluded.AuthIndex] = docExcluded
	host.docs[disabled.AuthIndex] = docDisabled

	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		return successStream()
	}

	startupDelay := 24 * time.Hour
	runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: &startupDelay})
	cfg := testConfig(t, runtime, "exclude_credentials:\n  - codex-excluded\ntimezone: UTC\n")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(t.Context())

	milestoneKey := "2026-09-18#05:00"
	if err := runtime.DispatchMilestone(t.Context(), milestoneKey, clock.Now()); err != nil {
		t.Fatal(err)
	}
	if host.streamRequestCount() != 1 {
		t.Fatalf("pings = %d, want only active", host.streamRequestCount())
	}
	if state := runtime.store.Credential("codex-excluded"); state.Reason != "excluded" {
		t.Fatalf("excluded state = %#v", state)
	}
	if state := runtime.store.Credential("codex-disabled"); state.Reason != "disabled" {
		t.Fatalf("disabled state = %#v", state)
	}
}

func TestScheduledMilestonePingsUnavailableCredential(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)}
	host := newFakeHost()
	unavail, docUnavail := credential("codex-unavailable", "token-unavail", 1)
	unavail.Unavailable = true

	host.auths = []AuthFile{unavail}
	host.docs[unavail.AuthIndex] = docUnavail
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		return successStream()
	}

	startupDelay := 24 * time.Hour
	runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: &startupDelay})
	cfg := testConfig(t, runtime, "timezone: UTC\n")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(t.Context())

	milestoneKey := "2026-09-18#10:00"
	if err := runtime.DispatchMilestone(t.Context(), milestoneKey, clock.Now()); err != nil {
		t.Fatal(err)
	}
	if host.streamRequestCount() != 1 {
		t.Fatalf("pings = %d, want 1 (scheduled milestone must re-test unavailable credential)", host.streamRequestCount())
	}
	state := runtime.store.Credential("codex-unavailable")
	if state.LastProcessedMilestone != milestoneKey || state.Status != "waiting" || state.Reason != "milestone_ping_succeeded" {
		t.Fatalf("unavailable credential state = %#v", state)
	}
}

func TestStartupCatchUpRunsWhenMoreThanOneHourBeforeNextMilestone(t *testing.T) {
	// 07:30 UTC is after 05:00 milestone, and 10:00 milestone is 2.5 hours away (>= 1 hour)
	clock := &fakeClock{now: time.Date(2026, 9, 18, 7, 30, 0, 0, time.UTC)}
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		return successStream()
	}

	startupDelay := time.Millisecond
	runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: &startupDelay})
	cfg := testConfig(t, runtime, "timezone: UTC\n")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(t.Context())

	// Give scheduleLoop a moment to run catch-up
	time.Sleep(50 * time.Millisecond)

	if host.streamRequestCount() != 1 {
		t.Fatalf("catch-up pings = %d, want 1", host.streamRequestCount())
	}
	state := runtime.store.Credential("codex-a")
	if state.LastProcessedMilestone != "2026-09-18#05:00" {
		t.Fatalf("catch-up milestone = %q, want 2026-09-18#05:00", state.LastProcessedMilestone)
	}
}

func TestStartupCatchUpSkippedWhenLessThanOneHourBeforeNextMilestone(t *testing.T) {
	// 09:15 UTC is 45 minutes before the 10:00 milestone (< 1 hour)
	clock := &fakeClock{now: time.Date(2026, 9, 18, 9, 15, 0, 0, time.UTC)}
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		return successStream()
	}

	startupDelay := time.Millisecond
	runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: &startupDelay})
	cfg := testConfig(t, runtime, "timezone: UTC\n")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(t.Context())

	time.Sleep(50 * time.Millisecond)

	if host.streamRequestCount() != 0 {
		t.Fatalf("catch-up pings = %d, want 0 (skipped due to < 1h threshold)", host.streamRequestCount())
	}
}

func TestStartupCatchUpSkippedIfAlreadyProcessed(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 9, 18, 7, 30, 0, 0, time.UTC)}
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		return successStream()
	}

	statePath := filepath.Join(t.TempDir(), "state.json")
	store, _ := LoadStateStore(t.Context(), statePath)
	_ = store.SetLastMilestone(t.Context(), "2026-09-18#05:00")
	_ = store.Update(t.Context(), "codex-a", func(s *CredentialState) {
		s.LastProcessedMilestone = "2026-09-18#05:00"
	})

	startupDelay := time.Millisecond
	runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: &startupDelay})
	cfg := testConfig(t, runtime, "timezone: UTC\n")
	cfg.StatePath = statePath
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(t.Context())

	time.Sleep(50 * time.Millisecond)

	if host.streamRequestCount() != 0 {
		t.Fatalf("catch-up pings = %d, want 0 because milestone was already processed", host.streamRequestCount())
	}
}

func TestStartupCatchUpRunsForNewlyAvailableCredential(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 9, 18, 7, 30, 0, 0, time.UTC)}
	host := newFakeHost()
	fileA, docA := credential("codex-a", "token-a", 1)
	fileB, docB := credential("codex-b", "token-b", 1)
	host.auths = []AuthFile{fileA, fileB}
	host.docs[fileA.AuthIndex] = docA
	host.docs[fileB.AuthIndex] = docB
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		return successStream()
	}

	statePath := filepath.Join(t.TempDir(), "state.json")
	store, _ := LoadStateStore(t.Context(), statePath)
	_ = store.SetLastMilestone(t.Context(), "2026-09-18#05:00")
	// codex-a already processed 05:00
	_ = store.Update(t.Context(), "codex-a", func(s *CredentialState) {
		s.LastProcessedMilestone = "2026-09-18#05:00"
	})
	// codex-b was previously skipped (e.g. unavailable) with no LastProcessedMilestone
	_ = store.Update(t.Context(), "codex-b", func(s *CredentialState) {
		s.Status = "skipped"
		s.Reason = "unavailable"
		s.Skipped = 1
	})

	startupDelay := time.Millisecond
	runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: &startupDelay})
	cfg := testConfig(t, runtime, "timezone: UTC\n")
	cfg.StatePath = statePath
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(t.Context())

	time.Sleep(50 * time.Millisecond)

	// Only codex-b should be pinged, codex-a skipped
	if host.streamRequestCount() != 1 {
		t.Fatalf("catch-up pings = %d, want 1 (for codex-b only)", host.streamRequestCount())
	}
	stateA := runtime.store.Credential("codex-a")
	if stateA.LastProcessedMilestone != "2026-09-18#05:00" {
		t.Fatalf("codex-a milestone = %q", stateA.LastProcessedMilestone)
	}
	stateB := runtime.store.Credential("codex-b")
	if stateB.LastProcessedMilestone != "2026-09-18#05:00" || stateB.Status != "waiting" || stateB.Reason != "milestone_ping_succeeded" {
		t.Fatalf("codex-b state after catch-up = %#v", stateB)
	}
}

func TestStartupCatchUpSkippedBeforeFirstMilestone(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 9, 18, 4, 0, 0, 0, time.UTC)} // 04:00 is before 05:00
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		return successStream()
	}

	startupDelay := time.Millisecond
	runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: &startupDelay})
	cfg := testConfig(t, runtime, "timezone: UTC\n")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(t.Context())

	time.Sleep(50 * time.Millisecond)

	if host.streamRequestCount() != 0 {
		t.Fatalf("catch-up pings = %d, want 0 before first milestone", host.streamRequestCount())
	}
}

func TestManualPingProcessesMilestoneWhenFlagged(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 9, 18, 5, 5, 0, 0, time.UTC)}
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		return successStream()
	}

	runtime := newTestRuntime(t, host, Options{Now: clock.Now})
	cfg := testConfig(t, runtime, "auto_ping_disabled: true\ntimezone: UTC\n")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	response, err := runtime.ManualPing(t.Context(), ManualPingRequest{CredentialID: "codex-a", MarkCycleProcessed: true})
	if err != nil || !response.Success {
		t.Fatalf("response = %#v, error = %v", response, err)
	}
	state := runtime.store.Credential("codex-a")
	if state.LastProcessedMilestone != "2026-09-18#05:00" {
		t.Fatalf("manual ping processed milestone = %q, want 2026-09-18#05:00", state.LastProcessedMilestone)
	}
}

func TestDisabledAutoPingSendsNoRequests(t *testing.T) {
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		t.Fatal("disabled auto-ping must not send requests")
		return HTTPStreamResponse{}, nil, nil
	}
	runtime := newTestRuntime(t, host, Options{})
	cfg := testConfig(t, runtime, "auto_ping_disabled: true\n")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ScanOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestMilestoneRetryTransientFailureAndMaxAttempts(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 9, 18, 5, 0, 0, 0, time.UTC)}
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document

	// Simulate 500 internal server error (transient failure)
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		return HTTPStreamResponse{StatusCode: http.StatusInternalServerError}, []HTTPStreamChunk{{Payload: []byte(`{"error":"server error"}`), Done: true}}, nil
	}

	startupDelay := 24 * time.Hour
	runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: &startupDelay})
	cfg := testConfig(t, runtime, "retry_cooldown: 2m\ntimezone: UTC\n")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(t.Context())

	milestoneKey := "2026-09-18#05:00"
	milestoneTime := clock.Now()
	nextMilestoneTarget := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)

	// Attempt 1: Milestone dispatch fails
	if err := runtime.DispatchMilestone(t.Context(), milestoneKey, milestoneTime); err != nil {
		t.Fatal(err)
	}
	state := runtime.store.Credential("codex-a")
	if state.Attempts != 1 || state.Failures != 1 || state.RetryCount != 1 {
		t.Fatalf("attempt 1 state: attempts=%d, failures=%d, retry_count=%d", state.Attempts, state.Failures, state.RetryCount)
	}
	if state.Status != "cooldown" {
		t.Fatalf("attempt 1 status = %q, want cooldown", state.Status)
	}
	expectedRetry1 := clock.Now().Add(2 * time.Minute)
	if !state.NextRetryAt.Equal(expectedRetry1) {
		t.Fatalf("next_retry_at = %v, want %v", state.NextRetryAt, expectedRetry1)
	}

	retryTime, hasRetry := runtime.nextRetryTime(runtime.store, clock.Now(), nextMilestoneTarget)
	if !hasRetry || !retryTime.Equal(expectedRetry1) {
		t.Fatalf("nextRetryTime = (%v, %v), want (%v, true)", retryTime, hasRetry, expectedRetry1)
	}

	// Advance clock to first retry time (05:02)
	clock.Advance(2 * time.Minute)

	// Attempt 2: Retry 1 fails
	if err := runtime.DispatchRetries(t.Context()); err != nil {
		t.Fatal(err)
	}
	state = runtime.store.Credential("codex-a")
	if state.Attempts != 2 || state.Failures != 2 || state.RetryCount != 2 {
		t.Fatalf("attempt 2 state: attempts=%d, failures=%d, retry_count=%d", state.Attempts, state.Failures, state.RetryCount)
	}
	expectedRetry2 := clock.Now().Add(2 * time.Minute)
	if !state.NextRetryAt.Equal(expectedRetry2) {
		t.Fatalf("attempt 2 next_retry_at = %v, want %v", state.NextRetryAt, expectedRetry2)
	}

	// Advance clock to second retry time (05:04)
	clock.Advance(2 * time.Minute)

	// Attempt 3: Retry 2 fails (max 3 attempts exhausted)
	if err := runtime.DispatchRetries(t.Context()); err != nil {
		t.Fatal(err)
	}
	state = runtime.store.Credential("codex-a")
	if state.Attempts != 3 || state.Failures != 3 || state.RetryCount != 3 {
		t.Fatalf("attempt 3 state: attempts=%d, failures=%d, retry_count=%d", state.Attempts, state.Failures, state.RetryCount)
	}
	if !state.NextRetryAt.IsZero() {
		t.Fatalf("after 3 attempts, next_retry_at should be zero, got %v", state.NextRetryAt)
	}

	// Check nextRetryTime reports no more retries
	_, hasRetry = runtime.nextRetryTime(runtime.store, clock.Now(), nextMilestoneTarget)
	if hasRetry {
		t.Fatal("hasRetry should be false after 3 attempts")
	}

	// Advancing and dispatching retries again should do nothing
	clock.Advance(2 * time.Minute)
	if err := runtime.DispatchRetries(t.Context()); err != nil {
		t.Fatal(err)
	}
	state = runtime.store.Credential("codex-a")
	if state.Attempts != 3 {
		t.Fatalf("attempts after exhausted retries = %d, want 3", state.Attempts)
	}

	// Verify successful retry behavior on a separate fresh credential / reset
	file2, document2 := credential("codex-b", "token-b", 1)
	host.auths = append(host.auths, file2)
	host.docs[file2.AuthIndex] = document2

	// Initial attempt for codex-b fails
	if err := runtime.DispatchMilestone(t.Context(), milestoneKey, milestoneTime); err != nil {
		t.Fatal(err)
	}
	stateB := runtime.store.Credential("codex-b")
	if stateB.RetryCount != 1 {
		t.Fatalf("codex-b retry_count = %d, want 1", stateB.RetryCount)
	}

	// Upstream recovers and succeeds
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		return successStream()
	}
	clock.Advance(2 * time.Minute)
	if err := runtime.DispatchRetries(t.Context()); err != nil {
		t.Fatal(err)
	}
	stateB = runtime.store.Credential("codex-b")
	if stateB.Attempts != 2 || stateB.Successes != 1 || stateB.RetryCount != 0 {
		t.Fatalf("successful retry state: attempts=%d, successes=%d, retry_count=%d", stateB.Attempts, stateB.Successes, stateB.RetryCount)
	}
	if !stateB.NextRetryAt.IsZero() {
		t.Fatalf("successful retry should have zero next_retry_at, got %v", stateB.NextRetryAt)
	}
	if stateB.Status != "waiting" || stateB.Reason != "milestone_ping_succeeded" {
		t.Fatalf("successful retry status/reason = (%q, %q)", stateB.Status, stateB.Reason)
	}
	if stateB.LastProcessedMilestone != milestoneKey {
		t.Fatalf("successful retry milestone = %q, want %q", stateB.LastProcessedMilestone, milestoneKey)
	}
}

func TestMilestoneRetrySupersededByNextMilestone(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 9, 18, 5, 0, 0, 0, time.UTC)}
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document

	// Milestone 05:00 fails
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		return HTTPStreamResponse{StatusCode: http.StatusInternalServerError}, []HTTPStreamChunk{{Done: true}}, nil
	}

	startupDelay := 24 * time.Hour
	runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: &startupDelay})
	cfg := testConfig(t, runtime, "retry_cooldown: 5m\nschedule:\n  - \"05:00\"\n  - \"05:02\"\ntimezone: UTC\n")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(t.Context())

	// Milestone 05:00 fails, scheduling retry at 05:05 (cooldown 5m)
	if err := runtime.DispatchMilestone(t.Context(), "2026-09-18#05:00", clock.Now()); err != nil {
		t.Fatal(err)
	}
	state := runtime.store.Credential("codex-a")
	if state.RetryCount != 1 || !state.NextRetryAt.Equal(clock.Now().Add(5*time.Minute)) {
		t.Fatalf("state after 05:00 failure = %#v", state)
	}

	// Next milestone target is 05:02:00
	nextMilestoneTarget := time.Date(2026, 9, 18, 5, 2, 0, 0, time.UTC)
	// Pending retry is at 05:05:00, which is AFTER 05:02:00. Next milestone supersedes pending retry.
	_, hasRetry := runtime.nextRetryTime(runtime.store, clock.Now(), nextMilestoneTarget)
	if hasRetry {
		t.Fatal("pending retry at 05:05 should be superseded by next milestone at 05:02")
	}

	// Advance clock to next milestone 05:02:00
	clock.Advance(2 * time.Minute)
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		return successStream()
	}

	// New milestone dispatches and supersedes
	if err := runtime.DispatchMilestone(t.Context(), "2026-09-18#05:02", clock.Now()); err != nil {
		t.Fatal(err)
	}
	state = runtime.store.Credential("codex-a")
	if state.LastProcessedMilestone != "2026-09-18#05:02" || state.RetryCount != 0 || !state.NextRetryAt.IsZero() {
		t.Fatalf("superseding milestone state = %#v", state)
	}
	if state.Status != "waiting" || state.Reason != "milestone_ping_succeeded" {
		t.Fatalf("superseding milestone status = (%q, %q)", state.Status, state.Reason)
	}
}

func TestMilestoneAuthBlockImmediateAndUnblockOnVersionChange(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 9, 18, 5, 0, 0, 0, time.UTC)}
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	file.UpdatedAt = clock.Now()
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document

	// Simulate 401 Unauthorized
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		return HTTPStreamResponse{StatusCode: http.StatusUnauthorized}, []HTTPStreamChunk{{Payload: []byte(`{"error":"invalid_token"}`), Done: true}}, nil
	}

	startupDelay := 24 * time.Hour
	runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: &startupDelay})
	cfg := testConfig(t, runtime, "retry_cooldown: 2m\ntimezone: UTC\n")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(t.Context())

	// Dispatch milestone 05:00: fails with 401
	if err := runtime.DispatchMilestone(t.Context(), "2026-09-18#05:00", clock.Now()); err != nil {
		t.Fatal(err)
	}

	// Immediately marked blocked, reason credential_unchanged_after_auth_failure, zero retries
	state := runtime.store.Credential("codex-a")
	if state.Status != "blocked" {
		t.Fatalf("status = %q, want blocked", state.Status)
	}
	if state.Reason != "credential_unchanged_after_auth_failure" {
		t.Fatalf("reason = %q, want credential_unchanged_after_auth_failure", state.Reason)
	}
	if state.BlockedCredentialVersion != file.VersionKey() {
		t.Fatalf("blocked version = %q, want %q", state.BlockedCredentialVersion, file.VersionKey())
	}
	if state.RetryCount != 0 || !state.NextRetryAt.IsZero() {
		t.Fatalf("auth failure should have 0 retries and zero NextRetryAt: retry_count=%d, next_retry_at=%v", state.RetryCount, state.NextRetryAt)
	}

	// nextRetryTime must return false (no retries for auth failure)
	nextMilestoneTarget := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	_, hasRetry := runtime.nextRetryTime(runtime.store, clock.Now(), nextMilestoneTarget)
	if hasRetry {
		t.Fatal("hasRetry should be false for auth-blocked credential")
	}

	// Next milestone at 10:00: credential is still unchanged, so it should be skipped
	clock.Advance(5 * time.Hour)
	initialRequests := host.streamRequestCount()
	if err := runtime.DispatchMilestone(t.Context(), "2026-09-18#10:00", clock.Now()); err != nil {
		t.Fatal(err)
	}
	if host.streamRequestCount() != initialRequests {
		t.Fatalf("auth-blocked credential was not skipped; requests = %d, want %d", host.streamRequestCount(), initialRequests)
	}
	state = runtime.store.Credential("codex-a")
	if state.Status != "blocked" || state.Reason != "credential_unchanged_after_auth_failure" {
		t.Fatalf("state on subsequent dispatch = %#v", state)
	}

	// Now update credential version (token refresh)
	clock.Advance(1 * time.Hour)
	newFile, newDoc := credential("codex-a", "token-a-refreshed", 1)
	newFile.UpdatedAt = clock.Now()
	host.auths = []AuthFile{newFile}
	host.docs[newFile.AuthIndex] = newDoc
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		return successStream()
	}

	// Next milestone at 15:00: credential version changed, so it unblocks and succeeds
	clock.Advance(4 * time.Hour)
	if err := runtime.DispatchMilestone(t.Context(), "2026-09-18#15:00", clock.Now()); err != nil {
		t.Fatal(err)
	}
	if host.streamRequestCount() != initialRequests+1 {
		t.Fatalf("unblocked credential did not receive request; requests = %d, want %d", host.streamRequestCount(), initialRequests+1)
	}
	state = runtime.store.Credential("codex-a")
	if state.Status != "waiting" || state.Reason != "milestone_ping_succeeded" {
		t.Fatalf("unblocked state: status=%q, reason=%q", state.Status, state.Reason)
	}
	if state.BlockedCredentialVersion != "" {
		t.Fatalf("blocked credential version should be cleared, got %q", state.BlockedCredentialVersion)
	}
	if state.LastProcessedMilestone != "2026-09-18#15:00" {
		t.Fatalf("last processed milestone = %q, want 2026-09-18#15:00", state.LastProcessedMilestone)
	}
}
