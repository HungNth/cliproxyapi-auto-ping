package autoping

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"sync/atomic"
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
					expectedSuccess := 2
					if retry && id == "codex-a" {
						// codex-a failed at 00:01 and its retry was canceled when 00:02 arrived, then succeeded at 00:02.
						expectedSuccess = 1
					}
					if !retry && id == "codex-b" {
						// The serialized 00:01 batch reached codex-b across the 00:02 boundary, so only its fresh 00:02 attempt succeeds.
						expectedSuccess = 1
					}
					if state.LastProcessedMilestone != "2000-01-01#00:02" || state.Successes != uint64(expectedSuccess) {
						t.Errorf("%s: processed %q, successes=%d; want milestone 00:02, successes=%d", id, state.LastProcessedMilestone, state.Successes, expectedSuccess)
					}
				}
			})
		})
	}
}

func TestScheduleLoopCancelsRegularRequestAtNextMilestone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newFakeHost()
		file, document := credential("codex-a", "token-a", 1)
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document

		var requests atomic.Int32
		host.httpStreamContextFunc = func(ctx context.Context, _ HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
			if requests.Add(1) == 1 {
				<-ctx.Done()
				return HTTPStreamResponse{}, nil, context.Cause(ctx)
			}
			return successStream()
		}

		runtime := newTestRuntime(t, host, Options{StartupDelay: new(time.Duration)})
		cfg := testConfig(t, runtime, "schedule: [\"00:01\", \"00:02\", \"05:00\"]\ntimezone: UTC\nrequest_timeout: 10m\n")
		cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
		if err := runtime.Configure(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		defer runtime.Shutdown(t.Context())

		time.Sleep(2 * time.Minute)
		synctest.Wait()

		if requests.Load() != 2 {
			t.Fatalf("requests = %d, want canceled 00:01 plus fresh 00:02", requests.Load())
		}
		state := runtime.store.Credential(file.ID)
		if state.LastProcessedMilestone != "2000-01-01#00:02" {
			t.Fatalf("processed milestone = %q, want 2000-01-01#00:02", state.LastProcessedMilestone)
		}
	})
}

func TestScheduleLoopDispatchesNewMilestoneWithoutCooldownAfterRetryDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newFakeHost()
		file, document := credential("codex-a", "token-a", 1)
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document

		var requests atomic.Int32
		host.httpStreamContextFunc = func(ctx context.Context, _ HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
			switch requests.Add(1) {
			case 1:
				return HTTPStreamResponse{StatusCode: http.StatusInternalServerError}, []HTTPStreamChunk{{Done: true}}, nil
			case 2:
				<-ctx.Done()
				return HTTPStreamResponse{}, nil, context.Cause(ctx)
			default:
				return successStream()
			}
		}

		runtime := newTestRuntime(t, host, Options{StartupDelay: new(time.Duration)})
		cfg := testConfig(t, runtime, "schedule: [\"00:01\", \"00:02\", \"05:00\"]\ntimezone: UTC\nretry_cooldown: 30s\nrequest_timeout: 10m\n")
		cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
		if err := runtime.Configure(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		defer runtime.Shutdown(t.Context())

		time.Sleep(2 * time.Minute)
		synctest.Wait()

		if requests.Load() != 3 {
			t.Fatalf("requests = %d, want initial failure, canceled retry, and immediate 00:02 attempt", requests.Load())
		}
		state := runtime.store.Credential(file.ID)
		if state.LastProcessedMilestone != "2000-01-01#00:02" {
			t.Fatalf("processed milestone = %q, want 2000-01-01#00:02", state.LastProcessedMilestone)
		}
	})
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
		if state := runtime.store.Credential(file.ID); state.LastProcessedMilestone != "2000-01-01#07:04" || state.Successes != 3 {
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
func TestScheduleLoopReconfigurationWaitsForInFlightDispatchAndEvaluatesDueMilestone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newFakeHost()
		file, document := credential("codex-a", "token-a", 1)
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document

		inFlightStarted := make(chan struct{}, 1)
		unblockInFlight := make(chan struct{})
		t.Cleanup(func() {
			select {
			case <-unblockInFlight:
			default:
				close(unblockInFlight)
			}
		})

		host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
			select {
			case inFlightStarted <- struct{}{}:
			default:
			}
			<-unblockInFlight
			return successStream()
		}

		runtime := newTestRuntime(t, host, Options{StartupDelay: new(time.Duration)})
		cfg := testConfig(t, runtime, "schedule: [\"00:01\", \"05:00\"]\ntimezone: UTC\n")
		cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
		if err := runtime.Configure(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		defer runtime.Shutdown(t.Context())

		// Advance to 00:01 to trigger first dispatch
		time.Sleep(time.Minute)
		synctest.Wait()
		<-inFlightStarted

		// While first dispatch is held in-flight, reconfigure to 00:02
		updated := testConfig(t, runtime, "schedule: [\"00:02\", \"05:00\"]\ntimezone: UTC\n")
		updated.StatePath = cfg.StatePath

		reconfigureDone := make(chan error, 1)
		go func() {
			reconfigureDone <- runtime.Configure(t.Context(), updated)
		}()

		// Give Configure a bounded scheduling window; it must still be blocked on the in-flight scanner.
		time.Sleep(10 * time.Second)
		select {
		case err := <-reconfigureDone:
			t.Fatalf("reconfiguration completed before in-flight drain: %v", err)
		default:
		}

		// Now unblock the old in-flight stream so it can finish/exit
		close(unblockInFlight)
		time.Sleep(10 * time.Second)
		synctest.Wait()

		if err := <-reconfigureDone; err != nil {
			t.Fatalf("reconfigure failed: %v", err)
		}

		// Advance clock past 00:02; the new replacement scheduler must fire for 00:02
		time.Sleep(time.Minute)
		synctest.Wait()

		state := runtime.store.Credential(file.ID)
		if state.LastProcessedMilestone != "2000-01-01#00:02" {
			t.Fatalf("replacement scheduler missed due milestone: got %q, want 2000-01-01#00:02", state.LastProcessedMilestone)
		}
	})
}
func TestConcurrentConfigureSerializedCleanly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newFakeHost()
		file, document := credential("codex-a", "token-a", 1)
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document

		firstFired := make(chan struct{}, 1)
		unblockFirstStream := make(chan struct{})
		t.Cleanup(func() {
			select {
			case <-unblockFirstStream:
			default:
				close(unblockFirstStream)
			}
		})
		host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
			select {
			case firstFired <- struct{}{}:
				<-unblockFirstStream
			default:
			}
			return successStream()
		}

		runtime := newTestRuntime(t, host, Options{StartupDelay: new(time.Duration)})
		cfg := testConfig(t, runtime, "schedule: [\"00:01\", \"05:00\"]\ntimezone: UTC\n")
		cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
		if err := runtime.Configure(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		defer runtime.Shutdown(t.Context())

		// Advance to trigger first scheduleLoop at 00:01
		time.Sleep(time.Minute)
		synctest.Wait()
		<-firstFired

		cfgB := testConfig(t, runtime, "schedule: [\"00:03\", \"05:00\"]\ntimezone: UTC\n")
		cfgB.StatePath = cfg.StatePath

		cfgC := testConfig(t, runtime, "schedule: [\"00:04\", \"05:00\"]\ntimezone: UTC\n")
		cfgC.StatePath = cfg.StatePath

		errB := make(chan error, 1)
		errC := make(chan error, 1)

		go func() { errB <- runtime.Configure(t.Context(), cfgB) }()
		time.Sleep(10 * time.Millisecond)
		select {
		case err := <-errB:
			t.Fatalf("Configure B completed before old scanner drained: %v", err)
		default:
		}

		go func() { errC <- runtime.Configure(t.Context(), cfgC) }()
		// B owns configMu and is blocked draining the old scanner; C queues behind B.
		close(unblockFirstStream)
		synctest.Wait()
		if err := <-errB; err != nil {
			t.Fatalf("Configure B failed: %v", err)
		}
		if err := <-errC; err != nil {
			t.Fatalf("Configure C failed: %v", err)
		}
		request, _ := json.Marshal(managementRequest{Method: http.MethodGet, Path: "/v0/management/cliproxyapi-auto-ping/status"})
		raw := runtime.Handle(t.Context(), "management.handle", request)
		response := decodeManagementResponse(t, raw)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", response.StatusCode)
		}
		var payload struct {
			Schedule []string `json:"schedule"`
		}
		if err := json.Unmarshal(response.Body, &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Schedule) != 2 || payload.Schedule[0] != "00:04" {
			t.Fatalf("final schedule after concurrent reconfigurations = %v, want 00:04", payload.Schedule)
		}
	})
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
func TestConsecutiveMilestonesFreshAttemptAcrossFailureAndSuccessOutcomes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clock := &fakeClock{now: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}
		host := newFakeHost()
		// 4 credentials:
		// 1. codex-succ: succeeds at 00:01, must run again at 00:02
		// 2. codex-unavail: unavailable flag true, must run at both
		// 3. codex-auth: returns 401 at 00:01, must attempt again at 00:02
		// 4. codex-cool: returns 500 at 00:01 (entering cooldown), must attempt afresh at 00:02
		// 5. codex-excluded: in exclude_credentials, never attempted
		succ, docSucc := credential("codex-succ", "token-succ", 1)
		unavail, docUnavail := credential("codex-unavail", "token-unavail", 1)
		unavail.Unavailable = true
		authFail, docAuth := credential("codex-auth", "token-auth", 1)
		cool, docCool := credential("codex-cool", "token-cool", 1)
		excl, docExcl := credential("codex-excluded", "token-excl", 1)

		host.auths = []AuthFile{succ, unavail, authFail, cool, excl}
		host.docs[succ.AuthIndex] = docSucc
		host.docs[unavail.AuthIndex] = docUnavail
		host.docs[authFail.AuthIndex] = docAuth
		host.docs[cool.AuthIndex] = docCool
		host.docs[excl.AuthIndex] = docExcl

		var cycle atomic.Int32
		cycle.Store(1)
		host.httpStreamFunc = func(req HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
			acct := req.Headers.Get("ChatGPT-Account-ID")
			if cycle.Load() == 1 {
				switch acct {
				case "account-codex-auth":
					return HTTPStreamResponse{StatusCode: http.StatusUnauthorized}, []HTTPStreamChunk{{Done: true}}, nil
				case "account-codex-cool":
					return HTTPStreamResponse{StatusCode: http.StatusInternalServerError}, []HTTPStreamChunk{{Done: true}}, nil
				default:
					return successStream()
				}
			}
			// In cycle 2 (00:02), all eligible succeed
			return successStream()
		}
		runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: new(time.Duration)})
		statePath := filepath.Join(t.TempDir(), "state.json")
		cfgYAML := "schedule:\n  - \"00:01\"\n  - \"00:02\"\n  - \"05:00\"\ntimezone: UTC\nretry_cooldown: 5m\nmax_concurrency: 4\nexclude_credentials:\n  - codex-excluded\nstate_path: " + statePath + "\n"
		reconfReq, _ := json.Marshal(map[string]string{"config_yaml": cfgYAML})
		reconfResp := runtime.Handle(t.Context(), "plugin.reconfigure", reconfReq)
		var envelope struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(reconfResp, &envelope); err != nil || !envelope.OK {
			t.Fatalf("reconfigure failed: %s", string(reconfResp))
		}
		defer runtime.Shutdown(t.Context())

		// Advance time to 00:01 (milestone 1)
		clock.Advance(time.Minute)
		time.Sleep(time.Minute)
		synctest.Wait()

		// 4 requests made (succ, unavail, auth, cool); excl skipped
		if host.streamRequestCount() != 4 {
			t.Fatalf("cycle 1 requests = %d, want 4", host.streamRequestCount())
		}

		// Verify cycle 1 states via management status route
		statusReq, _ := json.Marshal(managementRequest{Method: "GET", Path: "/v0/management/cliproxyapi-auto-ping/status"})
		rawStatus := runtime.Handle(t.Context(), "management.handle", statusReq)
		decoded := decodeManagementResponse(t, rawStatus)
		var payload struct {
			Accounts []struct {
				CredentialID           string `json:"credential_id"`
				Status                 string `json:"status"`
				LastProcessedMilestone string `json:"last_processed_milestone"`
				AttemptedMilestone     string `json:"attempted_milestone"`
			} `json:"accounts"`
		}
		if err := json.Unmarshal(decoded.Body, &payload); err != nil {
			t.Fatal(err)
		}
		for _, acct := range payload.Accounts {
			switch acct.CredentialID {
			case "codex-succ", "codex-unavail":
				if acct.LastProcessedMilestone != "2000-01-01#00:01" || acct.Status != "waiting" {
					t.Fatalf("%s cycle 1 state = %#v", acct.CredentialID, acct)
				}
			case "codex-auth":
				if acct.LastProcessedMilestone != "" || acct.Status != "blocked" {
					t.Fatalf("codex-auth cycle 1 state = %#v", acct)
				}
			case "codex-cool":
				if acct.LastProcessedMilestone != "" || acct.Status != "cooldown" {
					t.Fatalf("codex-cool cycle 1 state = %#v", acct)
				}
			}
		}
		// Switch to cycle 2 and advance to 00:02.
		cycle.Store(2)
		clock.Advance(time.Minute)
		time.Sleep(time.Minute)
		synctest.Wait()

		// In cycle 2: ALL 4 credentials receive fresh attempts and succeed (+4 requests = 8 total)
		if host.streamRequestCount() != 8 {
			t.Fatalf("cycle 2 total requests = %d, want 8 (all 4 fresh attempts ran)", host.streamRequestCount())
		}

		rawStatus = runtime.Handle(t.Context(), "management.handle", statusReq)
		decoded = decodeManagementResponse(t, rawStatus)
		_ = json.Unmarshal(decoded.Body, &payload)
		for _, acct := range payload.Accounts {
			switch acct.CredentialID {
			case "codex-succ", "codex-unavail", "codex-auth", "codex-cool":
				if acct.LastProcessedMilestone != "2000-01-01#00:02" || acct.Status != "waiting" {
					t.Fatalf("%s cycle 2 state = %#v, want 2000-01-01#00:02 and waiting", acct.CredentialID, acct)
				}
			}
		}
	})
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

func TestStartupCatchUpRunsWhenLessThanOneHourBeforeNextMilestone(t *testing.T) {
	// 09:15 UTC is 45 minutes before the 10:00 milestone; with 1h threshold removed, catch-up must run!
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

	if host.streamRequestCount() != 1 {
		t.Fatalf("catch-up pings = %d, want 1 (catch-up must run even when < 1h before next milestone)", host.streamRequestCount())
	}
	state := runtime.store.Credential("codex-a")
	if state.LastProcessedMilestone != "2026-09-18#05:00" {
		t.Fatalf("catch-up milestone = %q, want 2026-09-18#05:00", state.LastProcessedMilestone)
	}
}
func TestStartupCatchUpAt0959AndCoalescesMissedMilestones(t *testing.T) {
	// 1. Startup at 09:59 with schedule 05:00, 10:00:
	// Both 05:00 was missed earlier today. Catch-up must run for 05:00 immediately.
	clock := &fakeClock{now: time.Date(2026, 9, 18, 9, 59, 0, 0, time.UTC)}
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		return successStream()
	}

	startupDelay := time.Millisecond
	runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: &startupDelay})
	cfg := testConfig(t, runtime, "schedule:\n  - \"05:00\"\n  - \"08:00\"\n  - \"10:00\"\ntimezone: UTC\n")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(t.Context())

	time.Sleep(50 * time.Millisecond)

	// Multiple missed milestones (05:00 and 08:00) coalesce to the latest elapsed milestone (08:00)
	if host.streamRequestCount() != 1 {
		t.Fatalf("catch-up pings = %d, want 1 (coalesced to latest elapsed milestone)", host.streamRequestCount())
	}
	state := runtime.store.Credential("codex-a")
	if state.LastProcessedMilestone != "2026-09-18#08:00" {
		t.Fatalf("coalesced milestone = %q, want 2026-09-18#08:00", state.LastProcessedMilestone)
	}
}

func TestScheduleLoopWakeUpCoalescesToLatestElapsedMilestone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clock := &fakeClock{now: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}
		host := newFakeHost()
		file, document := credential("codex-a", "token-a", 1)
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document
		host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
			return successStream()
		}

		runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: new(time.Duration)})
		statePath := filepath.Join(t.TempDir(), "state.json")
		cfgYAML := "schedule: [\"00:01\", \"00:02\", \"00:03\", \"05:00\"]\ntimezone: UTC\nstate_path: " + statePath + "\n"
		req, err := json.Marshal(map[string]string{"config_yaml": cfgYAML})
		if err != nil {
			t.Fatal(err)
		}
		resp := runtime.Handle(t.Context(), "plugin.reconfigure", req)
		var envelope struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(resp, &envelope); err != nil || !envelope.OK {
			t.Fatalf("reconfigure failed: %s", string(resp))
		}
		defer runtime.Shutdown(t.Context())

		// Simulate suspension until 00:03:10. The loop must coalesce 00:01/00:02/00:03 to 00:03.
		clock.Advance(3*time.Minute + 10*time.Second)
		time.Sleep(3*time.Minute + 10*time.Second)
		synctest.Wait()

		if host.streamRequestCount() != 1 {
			t.Fatalf("wake-up requests = %d, want 1 latest milestone", host.streamRequestCount())
		}
		statusReq, _ := json.Marshal(managementRequest{Method: http.MethodGet, Path: "/v0/management/cliproxyapi-auto-ping/status"})
		statusRaw := runtime.Handle(t.Context(), "management.handle", statusReq)
		statusResp := decodeManagementResponse(t, statusRaw)
		var payload struct {
			Accounts []struct {
				LastProcessedMilestone string `json:"last_processed_milestone"`
			} `json:"accounts"`
		}
		if err := json.Unmarshal(statusResp.Body, &payload); err != nil || len(payload.Accounts) != 1 {
			t.Fatalf("status decode failed: %v", err)
		}
		if payload.Accounts[0].LastProcessedMilestone != "2000-01-01#00:03" {
			t.Fatalf("wake-up milestone = %q, want 2000-01-01#00:03", payload.Accounts[0].LastProcessedMilestone)
		}
	})
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

func TestMilestoneRetryTransientFailureBeyondThreeAttemptsAndRetryAfter(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 9, 18, 5, 0, 0, 0, time.UTC)}
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document

	retryAfterHeader := ""
	returnStatusCode := http.StatusInternalServerError
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		headers := http.Header{}
		if retryAfterHeader != "" {
			headers.Set("Retry-After", retryAfterHeader)
		}
		return HTTPStreamResponse{
			StatusCode: returnStatusCode,
			Headers:    headers,
		}, []HTTPStreamChunk{{Payload: []byte(`{"error":"upstream error"}`), Done: true}}, nil
	}

	startupDelay := 24 * time.Hour
	runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: &startupDelay})
	cfg := testConfig(t, runtime, "schedule:\n  - \"05:00\"\n  - \"10:00\"\nretry_cooldown: 1m\ntimezone: UTC\n")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(t.Context())

	milestoneKey := "2026-09-18#05:00"
	milestoneTime := clock.Now()
	// Attempt 1: Milestone dispatch fails
	if err := runtime.DispatchMilestone(t.Context(), milestoneKey, milestoneTime); err != nil {
		t.Fatal(err)
	}
	state := runtime.store.Credential("codex-a")
	if state.Attempts != 1 || state.Failures != 1 || state.RetryCount != 1 {
		t.Fatalf("attempt 1 state: attempts=%d, failures=%d, retry_count=%d", state.Attempts, state.Failures, state.RetryCount)
	}
	expectedRetry1 := clock.Now().Add(time.Minute)
	if !state.NextRetryAt.Equal(expectedRetry1) {
		t.Fatalf("next_retry_at = %v, want %v", state.NextRetryAt, expectedRetry1)
	}

	// Attempt 2, 3, 4, 5: Verify no attempt cap at 3
	for attempt := 2; attempt <= 5; attempt++ {
		clock.Advance(time.Minute)
		if err := runtime.DispatchRetries(t.Context()); err != nil {
			t.Fatal(err)
		}
		state = runtime.store.Credential("codex-a")
		if state.Attempts != uint64(attempt) || state.Failures != uint64(attempt) || state.RetryCount != attempt {
			t.Fatalf("attempt %d state: attempts=%d, failures=%d, retry_count=%d", attempt, state.Attempts, state.Failures, state.RetryCount)
		}
		expectedRetry := clock.Now().Add(time.Minute)
		if !state.NextRetryAt.Equal(expectedRetry) {
			t.Fatalf("attempt %d next_retry_at = %v, want %v", attempt, state.NextRetryAt, expectedRetry)
		}
	}

	// Attempt 6: Upstream returns HTTP 429 with Retry-After: 120 (2 minutes)
	returnStatusCode = http.StatusTooManyRequests
	retryAfterHeader = "120"
	clock.Advance(time.Minute)
	if err := runtime.DispatchRetries(t.Context()); err != nil {
		t.Fatal(err)
	}
	state = runtime.store.Credential("codex-a")
	expectedRetryAfter := clock.Now().Add(2 * time.Minute)
	if !state.NextRetryAt.Equal(expectedRetryAfter) {
		t.Fatalf("Retry-After next_retry_at = %v, want %v", state.NextRetryAt, expectedRetryAfter)
	}

	// Attempt 7: Upstream recovers and succeeds on retry
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		return successStream()
	}
	clock.Advance(2 * time.Minute)
	if err := runtime.DispatchRetries(t.Context()); err != nil {
		t.Fatal(err)
	}
	state = runtime.store.Credential("codex-a")
	if state.Successes != 1 || state.RetryCount != 0 || !state.NextRetryAt.IsZero() {
		t.Fatalf("successful retry state = %#v", state)
	}
	if state.Status != "waiting" || state.Reason != "milestone_ping_succeeded" {
		t.Fatalf("successful retry status/reason = (%q, %q)", state.Status, state.Reason)
	}
	if state.LastProcessedMilestone != milestoneKey {
		t.Fatalf("successful retry milestone = %q, want %q", state.LastProcessedMilestone, milestoneKey)
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
	cfg := testConfig(t, runtime, "retry_cooldown: 1m\nschedule:\n  - \"05:00\"\n  - \"05:02\"\ntimezone: UTC\n")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(t.Context())

	// Milestone 05:00 fails, scheduling retry at 05:01 (before 05:02)
	if err := runtime.DispatchMilestone(t.Context(), "2026-09-18#05:00", clock.Now()); err != nil {
		t.Fatal(err)
	}
	state := runtime.store.Credential("codex-a")
	if state.RetryCount != 1 || !state.NextRetryAt.Equal(clock.Now().Add(time.Minute)) {
		t.Fatalf("state after 05:00 failure = %#v", state)
	}

	// Advance clock to 05:01: retry 1 fails, scheduling retry at 05:02
	// 05:02 is AT next milestone, so it hits the cycle boundary and NextRetryAt becomes zero!
	clock.Advance(time.Minute)
	if err := runtime.DispatchRetries(t.Context()); err != nil {
		t.Fatal(err)
	}
	state = runtime.store.Credential("codex-a")
	if !state.NextRetryAt.IsZero() {
		t.Fatalf("retry at or after cycle boundary (05:02) should have zero NextRetryAt, got %v", state.NextRetryAt)
	}

	nextMilestoneTarget := time.Date(2026, 9, 18, 5, 2, 0, 0, time.UTC)
	_, hasRetry := runtime.nextRetryTime(runtime.store, clock.Now(), nextMilestoneTarget, "2026-09-18#05:00")
	if hasRetry {
		t.Fatal("hasRetry should be false when retry reached next milestone boundary")
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
func TestMilestoneRetryStopsAtConfiguredTimezoneMidnight(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-09-18 23:59:00 in Asia/Ho_Chi_Minh (+07:00)
	now := time.Date(2026, 9, 18, 23, 59, 0, 0, loc)
	clock := &fakeClock{now: now}
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document

	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		return HTTPStreamResponse{StatusCode: http.StatusInternalServerError}, []HTTPStreamChunk{{Done: true}}, nil
	}

	startupDelay := 24 * time.Hour
	runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: &startupDelay})
	cfg := testConfig(t, runtime, "schedule:\n  - \"23:59\"\n  - \"05:00\"\nretry_cooldown: 2m\ntimezone: Asia/Ho_Chi_Minh\n")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(t.Context())

	// Milestone 23:59 fails. 23:59 + 2m cooldown = 00:01 next day.
	// Midnight boundary is 00:00, which is earlier than next milestone 05:00.
	// Therefore retryTarget (00:01) is after midnight (00:00) and NextRetryAt must be zero!
	milestoneKey := "2026-09-18#23:59"
	if err := runtime.DispatchMilestone(t.Context(), milestoneKey, clock.Now()); err != nil {
		t.Fatal(err)
	}
	state := runtime.store.Credential("codex-a")
	if !state.NextRetryAt.IsZero() {
		t.Fatalf("retry beyond configured timezone midnight should have zero NextRetryAt, got %v", state.NextRetryAt)
	}
}

func TestMilestoneRetryHTTPDateAndInvalidRetryAfter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clock := &fakeClock{now: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}
		host := newFakeHost()
		file, document := credential("codex-a", "token-a", 1)
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document

		var retryHeader atomic.Pointer[string]
		host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
			headers := http.Header{}
			if p := retryHeader.Load(); p != nil && *p != "" {
				headers.Set("Retry-After", *p)
			}
			return HTTPStreamResponse{StatusCode: http.StatusTooManyRequests, Headers: headers}, []HTTPStreamChunk{{Done: true}}, nil
		}

		runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: new(time.Duration)})
		statePath := filepath.Join(t.TempDir(), "state.json")
		cfgYAML := "schedule:\n  - \"00:01\"\n  - \"05:00\"\nretry_cooldown: 1m\ntimezone: UTC\nstate_path: " + statePath + "\n"
		reconfReq, _ := json.Marshal(map[string]string{"config_yaml": cfgYAML})
		reconfResp := runtime.Handle(t.Context(), "plugin.reconfigure", reconfReq)
		var envelope struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(reconfResp, &envelope); err != nil || !envelope.OK {
			t.Fatalf("reconfigure failed: %s", string(reconfResp))
		}
		defer runtime.Shutdown(t.Context())

		// Future HTTP-date: 00:04:00 (3 minutes from 00:01)
		futureDate := time.Date(2000, 1, 1, 0, 4, 0, 0, time.UTC)
		hFuture := futureDate.Format(http.TimeFormat)
		retryHeader.Store(&hFuture)

		// Advance clock into 00:01:00
		clock.Advance(time.Minute)
		time.Sleep(time.Minute)
		synctest.Wait()

		// Query status to observe next_retry_at
		statusReq, _ := json.Marshal(managementRequest{Method: "GET", Path: "/v0/management/cliproxyapi-auto-ping/status"})
		rawStatus := runtime.Handle(t.Context(), "management.handle", statusReq)
		decoded := decodeManagementResponse(t, rawStatus)
		var payload struct {
			Accounts []struct {
				NextRetryAt string `json:"next_retry_at"`
			} `json:"accounts"`
		}
		if err := json.Unmarshal(decoded.Body, &payload); err != nil || len(payload.Accounts) == 0 {
			t.Fatalf("decode status failed: %v, body: %s", err, string(decoded.Body))
		}
		expectedFuture := futureDate.Format(time.RFC3339)
		if payload.Accounts[0].NextRetryAt != expectedFuture {
			t.Fatalf("future HTTP-date next_retry_at = %q, want %q", payload.Accounts[0].NextRetryAt, expectedFuture)
		}

		// Past HTTP-date: falls back to 1m cooldown
		pastDate := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
		hPast := pastDate.Format(http.TimeFormat)
		retryHeader.Store(&hPast)

		// Advance clock to 00:04:00
		clock.Advance(3 * time.Minute)
		time.Sleep(3 * time.Minute)
		synctest.Wait()

		rawStatus = runtime.Handle(t.Context(), "management.handle", statusReq)
		decoded = decodeManagementResponse(t, rawStatus)
		_ = json.Unmarshal(decoded.Body, &payload)
		expectedFallback := time.Date(2000, 1, 1, 0, 5, 0, 0, time.UTC).Format(time.RFC3339)
		if payload.Accounts[0].NextRetryAt != expectedFallback {
			t.Fatalf("past HTTP-date next_retry_at = %q, want %q", payload.Accounts[0].NextRetryAt, expectedFallback)
		}

		// Malformed header: falls back to 1m cooldown
		hInvalid := "invalid-header"
		retryHeader.Store(&hInvalid)

		// Advance clock to 00:05:00
		clock.Advance(time.Minute)
		time.Sleep(time.Minute)
		synctest.Wait()

		rawStatus = runtime.Handle(t.Context(), "management.handle", statusReq)
		decoded = decodeManagementResponse(t, rawStatus)
		_ = json.Unmarshal(decoded.Body, &payload)
		expectedFallback2 := time.Date(2000, 1, 1, 0, 6, 0, 0, time.UTC).Format(time.RFC3339)
		if payload.Accounts[0].NextRetryAt != expectedFallback2 {
			t.Fatalf("malformed header next_retry_at = %q, want %q", payload.Accounts[0].NextRetryAt, expectedFallback2)
		}
	})
}

func TestMilestoneTerminalModelAndBusinessFailuresDoNotRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newFakeHost()
		fileA, docA := credential("codex-model", "token-a", 1)
		fileB, docB := credential("codex-biz", "token-b", 1)
		host.auths = []AuthFile{fileA, fileB}
		host.docs[fileA.AuthIndex] = docA
		host.docs[fileB.AuthIndex] = docB

		host.httpStreamFunc = func(req HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
			acct := req.Headers.Get("ChatGPT-Account-ID")
			if acct == "account-codex-model" {
				return HTTPStreamResponse{StatusCode: http.StatusNotFound}, []HTTPStreamChunk{{Done: true}}, nil
			}
			return HTTPStreamResponse{StatusCode: http.StatusUnprocessableEntity}, []HTTPStreamChunk{{Payload: []byte(`{"error":{"message":"usage_limit"}}`), Done: true}}, nil
		}

		runtime := newTestRuntime(t, host, Options{StartupDelay: new(time.Duration)})
		statePath := filepath.Join(t.TempDir(), "state.json")
		cfgYAML := "schedule:\n  - \"00:01\"\n  - \"05:00\"\nretry_cooldown: 1m\ntimezone: UTC\nmodel: gpt-5.5\nstate_path: " + statePath + "\n"
		reconfReq, _ := json.Marshal(map[string]string{"config_yaml": cfgYAML})
		reconfResp := runtime.Handle(t.Context(), "plugin.reconfigure", reconfReq)
		var envelope struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(reconfResp, &envelope); err != nil || !envelope.OK {
			t.Fatalf("reconfigure failed: %s", string(reconfResp))
		}
		defer runtime.Shutdown(t.Context())

		// Advance into 00:01 milestone
		time.Sleep(time.Minute)
		synctest.Wait()

		statusReq, _ := json.Marshal(managementRequest{Method: "GET", Path: "/v0/management/cliproxyapi-auto-ping/status"})
		rawStatus := runtime.Handle(t.Context(), "management.handle", statusReq)
		decoded := decodeManagementResponse(t, rawStatus)
		var payload struct {
			Accounts []struct {
				CredentialID string `json:"credential_id"`
				NextRetryAt  string `json:"next_retry_at"`
				Status       string `json:"status"`
			} `json:"accounts"`
		}
		if err := json.Unmarshal(decoded.Body, &payload); err != nil {
			t.Fatal(err)
		}
		for _, acct := range payload.Accounts {
			if acct.NextRetryAt != "" {
				t.Fatalf("terminal failure for %s scheduled a retry: %q", acct.CredentialID, acct.NextRetryAt)
			}
		}

		// Advance time by 2 minutes; streamRequestCount must remain 2 (no retries dispatched)
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		if host.streamRequestCount() != 2 {
			t.Fatalf("requests count = %d, want 2 (no retries for terminal failures)", host.streamRequestCount())
		}
	})
}
func TestScheduleLoopDateRolloverResetsPriorAttemptCycles(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clock := &fakeClock{now: time.Date(2000, 1, 1, 23, 50, 0, 0, time.UTC)}
		host := newFakeHost()
		file, document := credential("codex-a", "token-a", 1)
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document

		var return500 atomic.Bool
		return500.Store(true)
		host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
			if return500.Load() {
				return HTTPStreamResponse{StatusCode: 500}, []HTTPStreamChunk{{Done: true}}, nil
			}
			return successStream()
		}
		statePath := filepath.Join(t.TempDir(), "state.json")
		runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: new(time.Duration)})
		cfgYAML := "schedule:\n  - \"23:55\"\n  - \"05:00\"\nretry_cooldown: 2m\ntimezone: UTC\nstate_path: " + statePath + "\n"
		reconfReq, _ := json.Marshal(map[string]string{"config_yaml": cfgYAML})
		reconfResp := runtime.Handle(t.Context(), "plugin.reconfigure", reconfReq)
		var envelope struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(reconfResp, &envelope); err != nil || !envelope.OK {
			t.Fatalf("reconfigure failed: %s", string(reconfResp))
		}
		defer runtime.Shutdown(t.Context())

		// Advance to 23:55: milestone fails with 500, setting attempted_milestone="2000-01-01#23:55"
		clock.Advance(5 * time.Minute)
		time.Sleep(5 * time.Minute)
		synctest.Wait()

		statusReq, _ := json.Marshal(managementRequest{Method: "GET", Path: "/v0/management/cliproxyapi-auto-ping/status"})
		rawStatus := runtime.Handle(t.Context(), "management.handle", statusReq)
		decoded := decodeManagementResponse(t, rawStatus)
		var payload struct {
			Accounts []struct {
				AttemptedMilestone string `json:"attempted_milestone"`
				Status             string `json:"status"`
			} `json:"accounts"`
		}
		if err := json.Unmarshal(decoded.Body, &payload); err != nil || len(payload.Accounts) == 0 {
			t.Fatal("decode status failed")
		}
		if payload.Accounts[0].AttemptedMilestone != "2000-01-01#23:55" {
			t.Fatalf("attempted_milestone before midnight = %q, want 2000-01-01#23:55", payload.Accounts[0].AttemptedMilestone)
		}

		// Advance time across midnight to 05:00 next day (2000-01-02 05:00)
		return500.Store(false)
		clock.Advance(5*time.Hour + 5*time.Minute)
		time.Sleep(5*time.Hour + 5*time.Minute)
		synctest.Wait()

		rawStatus = runtime.Handle(t.Context(), "management.handle", statusReq)
		decoded = decodeManagementResponse(t, rawStatus)
		var payloadAfter struct {
			Accounts []struct {
				AttemptedMilestone     string `json:"attempted_milestone"`
				LastProcessedMilestone string `json:"last_processed_milestone"`
				Status                 string `json:"status"`
			} `json:"accounts"`
		}
		if err := json.Unmarshal(decoded.Body, &payloadAfter); err != nil || len(payloadAfter.Accounts) == 0 {
			t.Fatal("decode status after midnight failed")
		}
		if payloadAfter.Accounts[0].AttemptedMilestone != "2000-01-02#05:00" || payloadAfter.Accounts[0].LastProcessedMilestone != "2000-01-02#05:00" {
			t.Fatalf("next day state after rollover = %#v", payloadAfter.Accounts[0])
		}
	})
}

func TestMilestoneRestartHonorsPendingRetryAndRejectsTerminalReattempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clock := &fakeClock{now: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}
		host := newFakeHost()
		fileRecoverable, docRecoverable := credential("codex-rec", "token-a", 1)
		fileTerminal, docTerminal := credential("codex-term", "token-b", 1)
		host.auths = []AuthFile{fileRecoverable, fileTerminal}
		host.docs[fileRecoverable.AuthIndex] = docRecoverable
		host.docs[fileTerminal.AuthIndex] = docTerminal

		host.httpStreamFunc = func(req HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
			acct := req.Headers.Get("ChatGPT-Account-ID")
			if acct == "account-codex-rec" {
				return HTTPStreamResponse{StatusCode: 500}, []HTTPStreamChunk{{Done: true}}, nil
			}
			return HTTPStreamResponse{StatusCode: 401}, []HTTPStreamChunk{{Done: true}}, nil
		}

		statePath := filepath.Join(t.TempDir(), "state.json")
		runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: new(time.Duration)})
		cfgYAML := "schedule:\n  - \"00:01\"\n  - \"05:00\"\nretry_cooldown: 2m\ntimezone: UTC\nstate_path: " + statePath + "\n"
		reconfReq, _ := json.Marshal(map[string]string{"config_yaml": cfgYAML})
		reconfResp := runtime.Handle(t.Context(), "plugin.reconfigure", reconfReq)
		var envelope struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(reconfResp, &envelope); err != nil || !envelope.OK {
			t.Fatalf("reconfigure failed: %s", string(reconfResp))
		}

		// Advance into 00:01 milestone (00:01:00)
		clock.Advance(time.Minute)
		time.Sleep(time.Minute)
		synctest.Wait()

		shutdownReq, _ := json.Marshal(map[string]any{})
		_ = runtime.Handle(t.Context(), "plugin.shutdown", shutdownReq)

		// Advance 30 seconds (now 00:01:30, before next retry at 00:03:00)
		clock.Advance(30 * time.Second)
		time.Sleep(30 * time.Second)

		// Restart runtime via lifecycle Handle with the same fakeClock
		restarted := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: new(time.Duration)})
		reconfResp = restarted.Handle(t.Context(), "plugin.reconfigure", reconfReq)
		if err := json.Unmarshal(reconfResp, &envelope); err != nil || !envelope.OK {
			t.Fatalf("reconfigure on restart failed: %s", string(reconfResp))
		}
		defer restarted.Shutdown(t.Context())

		// Wait briefly; at 00:01:30 no retry should run yet
		synctest.Wait()
		if host.streamRequestCount() != 2 {
			t.Fatalf("requests at 00:01:30 = %d, want 2 (no premature retry)", host.streamRequestCount())
		}

		// Advance to 00:03:05 (past 00:03:00 retry)
		clock.Advance(95 * time.Second)
		time.Sleep(95 * time.Second)
		synctest.Wait()

		// Exactly codex-rec retries (3 total requests); codex-term does not reattempt
		if host.streamRequestCount() != 3 {
			t.Fatalf("requests after retry time = %d, want 3 (only recoverable retried)", host.streamRequestCount())
		}
	})
}

func TestMilestoneAuthFailureDoesNotRetryButNextMilestoneAttemptsAgain(t *testing.T) {
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

	// Authentication failure is terminal for this cycle and has no pending retry.
	state := runtime.store.Credential("codex-a")
	if state.Status != "blocked" {
		t.Fatalf("status = %q, want blocked", state.Status)
	}
	if state.Reason != "credential_authentication_failed" {
		t.Fatalf("reason = %q, want credential_authentication_failed", state.Reason)
	}
	if state.RetryCount != 0 || !state.NextRetryAt.IsZero() {
		t.Fatalf("auth failure should have 0 retries and zero NextRetryAt: retry_count=%d, next_retry_at=%v", state.RetryCount, state.NextRetryAt)
	}
	// nextRetryTime must return false (no retries for auth failure in current cycle)
	nextMilestoneTarget := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	_, hasRetry := runtime.nextRetryTime(runtime.store, clock.Now(), nextMilestoneTarget, "2026-09-18#05:00")
	if hasRetry {
		t.Fatal("hasRetry should be false for auth failure")
	}

	// Next milestone at 10:00: credential is fresh and attempted again even if unchanged
	clock.Advance(5 * time.Hour)
	initialRequests := host.streamRequestCount()
	if err := runtime.DispatchMilestone(t.Context(), "2026-09-18#10:00", clock.Now()); err != nil {
		t.Fatal(err)
	}
	if host.streamRequestCount() != initialRequests+1 {
		t.Fatalf("auth failure should not suppress next milestone; requests = %d, want %d", host.streamRequestCount(), initialRequests+1)
	}
	// Update the credential version before the later 15:00 milestone; it remains eligible either way.
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
	if host.streamRequestCount() != initialRequests+2 {
		t.Fatalf("unblocked credential did not receive request; requests = %d, want %d", host.streamRequestCount(), initialRequests+2)
	}
	state = runtime.store.Credential("codex-a")
	if state.Status != "waiting" || state.Reason != "milestone_ping_succeeded" {
		t.Fatalf("unblocked state: status=%q, reason=%q", state.Status, state.Reason)
	}
	if state.LastProcessedMilestone != "2026-09-18#15:00" {
		t.Fatalf("last processed milestone = %q, want 2026-09-18#15:00", state.LastProcessedMilestone)
	}
}
