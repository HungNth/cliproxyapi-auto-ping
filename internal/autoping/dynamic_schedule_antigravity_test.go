package autoping

import (
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestDynamicScheduleAntigravityBootstrapWaitsBeforeFirstMilestone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newFakeHost()
		file, document := antigravityCredential("ag-boot-1")
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document

		var modelExecCalls int
		var mu sync.Mutex
		host.modelExecuteFunc = func(req ModelExecuteRequest) (ModelExecuteResponse, error) {
			mu.Lock()
			modelExecCalls++
			mu.Unlock()
			return ModelExecuteResponse{StatusCode: http.StatusOK, Body: []byte(`{"type":"response.completed"}`)}, nil
		}

		// Initial milestone is 05:00. Current time is 04:30.
		clock := &fakeClock{now: time.Date(2026, 10, 14, 4, 30, 0, 0, time.Local)}
		runtime := newTestRuntime(t, host, Options{
			Now:          clock.Now,
			StartupDelay: new(time.Duration),
		})

		cfg := testConfig(t, runtime, "schedule: [\"05:00\"]\ntimezone: \"Local\"")
		cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
		if err := runtime.Configure(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = runtime.Shutdown(t.Context()) }()

		// Allow startup delay to elapse
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()

		mu.Lock()
		callsBefore0500 := modelExecCalls
		mu.Unlock()
		if callsBefore0500 != 0 {
			t.Fatalf("expected 0 model executions before 05:00 anchor, got %d", callsBefore0500)
		}
	})
}

func TestDynamicScheduleAntigravityBootstrapAt0500ExecutesBothTargetsAndAnchors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newFakeHost()
		file, document := antigravityCredential("ag-boot-2")
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document

		var executedModels []string
		var mu sync.Mutex
		host.modelExecuteFunc = func(req ModelExecuteRequest) (ModelExecuteResponse, error) {
			mu.Lock()
			executedModels = append(executedModels, req.Model)
			mu.Unlock()
			if req.ForcedProvider != ProviderAntigravity || req.AuthID != "ag-boot-2" {
				t.Errorf("unexpected execute params: forced_provider=%q, auth_id=%q", req.ForcedProvider, req.AuthID)
			}
			return ModelExecuteResponse{StatusCode: http.StatusOK, Body: []byte(`{"type":"response.completed"}`)}, nil
		}

		host.httpDoFunc = func(req HTTPRequest) (HTTPResponse, error) {
			if req.URL != antigravityQuotaURL {
				t.Errorf("unexpected HTTP URL: %s", req.URL)
			}
			return HTTPResponse{
				StatusCode: http.StatusOK,
				Body: []byte(`{
					"groups": [
						{
							"displayName": "Gemini Models",
							"buckets": [
								{"bucketId": "gemini-5h", "window": "5h", "resetTime": "2026-10-14T10:00:00Z"},
								{"bucketId": "gemini-weekly", "window": "weekly", "resetTime": "2026-10-21T05:00:00Z"}
							]
						},
						{
							"displayName": "Claude and GPT models",
							"buckets": [
								{"bucketId": "3p-5h", "window": "5h", "resetTime": "2026-10-14T11:00:00Z"},
								{"bucketId": "3p-weekly", "window": "weekly", "resetTime": "2026-10-21T05:00:00Z"}
							]
						}
					]
				}`),
			}, nil
		}

		clock := &fakeClock{now: time.Date(2026, 10, 14, 4, 59, 59, 0, time.Local)}
		runtime := newTestRuntime(t, host, Options{
			Now:          clock.Now,
			StartupDelay: new(time.Duration),
		})

		cfg := testConfig(t, runtime, "schedule: [\"05:00\"]\ntimezone: \"Local\"\nantigravity_reset_margin: \"30s\"")
		cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
		if err := runtime.Configure(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = runtime.Shutdown(t.Context()) }()

		// Advance clock past 05:00
		clock.Advance(2 * time.Second)
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()

		mu.Lock()
		models := append([]string(nil), executedModels...)
		mu.Unlock()

		if len(models) != 2 {
			t.Fatalf("expected 2 bootstrap target model executions, got %d: %#v", len(models), models)
		}
		if models[0] != AntigravityModelGemini || models[1] != AntigravityModelClaude {
			t.Fatalf("unexpected executed models: %#v, want [%s, %s]", models, AntigravityModelGemini, AntigravityModelClaude)
		}

		state := runtime.store.Credential("ag-boot-2")
		geminiTarget := state.Targets[TargetKeyGemini5h]
		if geminiTarget.Status != "waiting" || geminiTarget.Reason != "trigger_scheduled" {
			t.Fatalf("geminiTarget status/reason = %q/%q, want waiting/trigger_scheduled", geminiTarget.Status, geminiTarget.Reason)
		}
		expectedGeminiReset := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
		if !geminiTarget.ObservedResetAt.Equal(expectedGeminiReset) {
			t.Fatalf("geminiTarget ObservedResetAt = %v, want %v", geminiTarget.ObservedResetAt, expectedGeminiReset)
		}
		if !geminiTarget.TargetTriggerAt.Equal(expectedGeminiReset.Add(30 * time.Second)) {
			t.Fatalf("geminiTarget TargetTriggerAt = %v, want %v", geminiTarget.TargetTriggerAt, expectedGeminiReset.Add(30*time.Second))
		}

		claudeTarget := state.Targets[TargetKeyClaude5h]
		if claudeTarget.Status != "waiting" || claudeTarget.Reason != "trigger_scheduled" {
			t.Fatalf("claudeTarget status/reason = %q/%q, want waiting/trigger_scheduled", claudeTarget.Status, claudeTarget.Reason)
		}
		expectedClaudeReset := time.Date(2026, 10, 14, 11, 0, 0, 0, time.UTC)
		if !claudeTarget.ObservedResetAt.Equal(expectedClaudeReset) {
			t.Fatalf("claudeTarget ObservedResetAt = %v, want %v", claudeTarget.ObservedResetAt, expectedClaudeReset)
		}
		if !claudeTarget.TargetTriggerAt.Equal(expectedClaudeReset.Add(30 * time.Second)) {
			t.Fatalf("claudeTarget TargetTriggerAt = %v, want %v", claudeTarget.TargetTriggerAt, expectedClaudeReset.Add(30*time.Second))
		}
	})
}

func TestDynamicScheduleAntigravityStartupCatchUpAfter0500(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newFakeHost()
		file, document := antigravityCredential("ag-catchup-1")
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document

		var execCount int
		var mu sync.Mutex
		host.modelExecuteFunc = func(req ModelExecuteRequest) (ModelExecuteResponse, error) {
			mu.Lock()
			execCount++
			mu.Unlock()
			return ModelExecuteResponse{StatusCode: http.StatusOK, Body: []byte(`{"type":"response.completed"}`)}, nil
		}

		host.httpDoFunc = func(req HTTPRequest) (HTTPResponse, error) {
			return HTTPResponse{
				StatusCode: http.StatusOK,
				Body: []byte(`{
					"groups": [
						{"displayName": "Gemini Models", "buckets": [{"bucketId": "gemini-5h", "window": "5h", "resetTime": "2026-10-14T12:00:00Z"}]},
						{"displayName": "Claude and GPT models", "buckets": [{"bucketId": "3p-5h", "window": "5h", "resetTime": "2026-10-14T13:00:00Z"}]}
					]
				}`),
			}, nil
		}

		// Current time is 06:30 (after 05:00 on the same day)
		clock := &fakeClock{now: time.Date(2026, 10, 14, 6, 30, 0, 0, time.Local)}
		runtime := newTestRuntime(t, host, Options{
			Now:          clock.Now,
			StartupDelay: new(time.Duration),
		})

		cfg := testConfig(t, runtime, "schedule: [\"05:00\"]\ntimezone: \"Local\"")
		cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
		if err := runtime.Configure(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = runtime.Shutdown(t.Context()) }()

		// Catch-up should dispatch immediately after startup delay
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()

		mu.Lock()
		count := execCount
		mu.Unlock()
		if count != 2 {
			t.Fatalf("expected 2 catch-up executions, got %d", count)
		}
	})
}

func TestDynamicScheduleAntigravityBootstrapAuthFailureSkipsSibling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newFakeHost()
		file, document := antigravityCredential("ag-authfail-1")
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document

		var executedModels []string
		var mu sync.Mutex
		host.modelExecuteFunc = func(req ModelExecuteRequest) (ModelExecuteResponse, error) {
			mu.Lock()
			executedModels = append(executedModels, req.Model)
			mu.Unlock()
			// Gemini returns 401 Unauthorized
			return ModelExecuteResponse{
				StatusCode: http.StatusUnauthorized,
				Body:       []byte(`{"error":{"message":"invalid_grant"}}`),
			}, nil
		}

		// Start after 05:00
		clock := &fakeClock{now: time.Date(2026, 10, 14, 6, 0, 0, 0, time.Local)}
		runtime := newTestRuntime(t, host, Options{
			Now:          clock.Now,
			StartupDelay: new(time.Duration),
		})
		cfg := testConfig(t, runtime, "schedule: [\"05:00\"]\ntimezone: \"Local\"")
		cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
		if err := runtime.Configure(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = runtime.Shutdown(t.Context()) }()

		time.Sleep(100 * time.Millisecond)
		synctest.Wait()

		mu.Lock()
		models := append([]string(nil), executedModels...)
		mu.Unlock()

		// Sibling (Claude) must be skipped because Gemini experienced credential-wide auth failure!
		if len(models) != 1 || models[0] != AntigravityModelGemini {
			t.Fatalf("expected only Gemini to be attempted before auth failure skipped Claude, got %#v", models)
		}

		state := runtime.store.Credential("ag-authfail-1")
		if state.FailureKind != string(FailureAuth) {
			t.Fatalf("state.FailureKind = %q, want %q", state.FailureKind, FailureAuth)
		}
		geminiTarget := state.Targets[TargetKeyGemini5h]
		if geminiTarget.FailureKind != string(FailureAuth) {
			t.Fatalf("geminiTarget.FailureKind = %q, want %q", geminiTarget.FailureKind, FailureAuth)
		}
	})
}

func TestDynamicScheduleAntigravitySiblingIsolation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newFakeHost()
		file, document := antigravityCredential("ag-iso-1")
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document

		host.modelExecuteFunc = func(req ModelExecuteRequest) (ModelExecuteResponse, error) {
			if req.Model == AntigravityModelGemini {
				return ModelExecuteResponse{StatusCode: http.StatusOK, Body: []byte(`{"type":"response.completed"}`)}, nil
			}
			// Claude fails with model error (HTTP 400)
			return ModelExecuteResponse{StatusCode: http.StatusBadRequest, Body: []byte(`{"error":{"message":"unsupported model"}}`)}, nil
		}

		host.httpDoFunc = func(req HTTPRequest) (HTTPResponse, error) {
			return HTTPResponse{
				StatusCode: http.StatusOK,
				Body: []byte(`{
					"groups": [
						{"displayName": "Gemini Models", "buckets": [{"bucketId": "gemini-5h", "window": "5h", "resetTime": "2026-10-14T14:00:00Z"}]}
					]
				}`),
			}, nil
		}

		clock := &fakeClock{now: time.Date(2026, 10, 14, 6, 0, 0, 0, time.Local)}
		runtime := newTestRuntime(t, host, Options{
			Now:          clock.Now,
			StartupDelay: new(time.Duration),
		})

		cfg := testConfig(t, runtime, "schedule: [\"05:00\"]\ntimezone: \"Local\"")
		cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
		if err := runtime.Configure(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = runtime.Shutdown(t.Context()) }()

		time.Sleep(100 * time.Millisecond)
		synctest.Wait()

		state := runtime.store.Credential("ag-iso-1")
		// Credential must NOT be marked terminal auth failure
		if state.FailureKind == string(FailureAuth) {
			t.Fatalf("state.FailureKind should not be auth failure: %q", state.FailureKind)
		}

		geminiTarget := state.Targets[TargetKeyGemini5h]
		if geminiTarget.Status != "waiting" || geminiTarget.Reason != "trigger_scheduled" {
			t.Fatalf("geminiTarget status/reason = %q/%q, want waiting/trigger_scheduled", geminiTarget.Status, geminiTarget.Reason)
		}

		claudeTarget := state.Targets[TargetKeyClaude5h]
		if claudeTarget.Status != "cooldown" || claudeTarget.FailureKind != string(FailureModel) {
			t.Fatalf("claudeTarget status/failureKind = %q/%q, want cooldown/model", claudeTarget.Status, claudeTarget.FailureKind)
		}
	})
}

func TestDynamicScheduleMixedCodexAndAntigravityNoCrossRouting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newFakeHost()
		codexFile, codexDoc := credential("codex-mix-1", "token-codex", 1)
		agFile, agDoc := antigravityCredential("ag-mix-1")
		host.auths = []AuthFile{codexFile, agFile}
		host.docs[codexFile.AuthIndex] = codexDoc
		host.docs[agFile.AuthIndex] = agDoc

		var codexStreamCalls int
		var agModelExecCalls int
		var mu sync.Mutex

		host.httpStreamFunc = func(req HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
			mu.Lock()
			codexStreamCalls++
			mu.Unlock()
			return successStream()
		}

		host.modelExecuteFunc = func(req ModelExecuteRequest) (ModelExecuteResponse, error) {
			mu.Lock()
			agModelExecCalls++
			mu.Unlock()
			return ModelExecuteResponse{StatusCode: http.StatusOK, Body: []byte(`{"type":"response.completed"}`)}, nil
		}

		host.httpDoFunc = func(req HTTPRequest) (HTTPResponse, error) {
			if req.URL == codexUsageURL {
				return HTTPResponse{
					StatusCode: http.StatusOK,
					Body:       []byte(`{"rate_limit": {"limit_window_seconds": 18000, "reset_at": 1790084449}}`),
				}, nil
			}
			if req.URL == antigravityQuotaURL {
				return HTTPResponse{
					StatusCode: http.StatusOK,
					Body: []byte(`{
						"groups": [
							{"displayName": "Gemini Models", "buckets": [{"bucketId": "gemini-5h", "window": "5h", "resetTime": "2026-10-14T15:00:00Z"}]},
							{"displayName": "Claude and GPT models", "buckets": [{"bucketId": "3p-5h", "window": "5h", "resetTime": "2026-10-14T16:00:00Z"}]}
						]
					}`),
				}, nil
			}
			return HTTPResponse{StatusCode: http.StatusNotFound}, nil
		}

		clock := &fakeClock{now: time.Date(2026, 10, 14, 6, 0, 0, 0, time.Local)}
		runtime := newTestRuntime(t, host, Options{
			Now:          clock.Now,
			StartupDelay: new(time.Duration),
		})

		cfg := testConfig(t, runtime, "schedule: [\"05:00\"]\ntimezone: \"Local\"")
		cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
		if err := runtime.Configure(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = runtime.Shutdown(t.Context()) }()

		time.Sleep(100 * time.Millisecond)
		synctest.Wait()

		mu.Lock()
		cStream := codexStreamCalls
		agModel := agModelExecCalls
		mu.Unlock()

		if cStream < 1 {
			t.Fatalf("expected at least 1 codex stream call, got %d", cStream)
		}
		if agModel != 2 {
			t.Fatalf("expected exactly 2 antigravity model execute calls, got %d", agModel)
		}

		codexState := runtime.store.Credential("codex-mix-1")
		if codexState.Provider != "codex" {
			t.Fatalf("codexState.Provider = %q, want codex", codexState.Provider)
		}
		agState := runtime.store.Credential("ag-mix-1")
		if agState.Provider != "antigravity" {
			t.Fatalf("agState.Provider = %q, want antigravity", agState.Provider)
		}
	})
}

func TestDynamicScheduleAntigravityStaggeredRollingCycles(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newFakeHost()
		file, document := antigravityCredential("ag-stagger-1")
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document

		var geminiExecs, claudeExecs int
		var mu sync.Mutex
		host.modelExecuteFunc = func(req ModelExecuteRequest) (ModelExecuteResponse, error) {
			mu.Lock()
			if req.Model == AntigravityModelGemini {
				geminiExecs++
			} else if req.Model == AntigravityModelClaude {
				claudeExecs++
			}
			mu.Unlock()
			return ModelExecuteResponse{StatusCode: http.StatusOK, Body: []byte(`{"type":"response.completed"}`)}, nil
		}

		// Start with pre-anchored targets in state:
		// Gemini resets at 10:00:00 (due at 10:00:30)
		// Claude resets at 11:00:00 (due at 11:00:30)
		now := time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)
		clock := &fakeClock{now: now}
		runtime := newTestRuntime(t, host, Options{
			Now:          clock.Now,
			StartupDelay: new(time.Duration),
		})

		statePath := filepath.Join(t.TempDir(), "state.json")
		store, err := LoadStateStore(t.Context(), statePath)
		if err != nil {
			t.Fatal(err)
		}
		geminiReset := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
		claudeReset := time.Date(2026, 10, 14, 11, 0, 0, 0, time.UTC)
		_ = store.Update(t.Context(), "ag-stagger-1", func(current *CredentialState) {
			current.Provider = ProviderAntigravity
			current.Status = "waiting"
			current.Targets = map[string]TargetState{
				TargetKeyGemini5h: {
					Target:          TargetKeyGemini5h,
					ModelFamily:     "gemini",
					SelectedModel:   AntigravityModelGemini,
					Status:          "waiting",
					Reason:          "trigger_scheduled",
					ObservedResetAt: geminiReset,
					TargetTriggerAt: geminiReset.Add(30 * time.Second),
				},
				TargetKeyClaude5h: {
					Target:          TargetKeyClaude5h,
					ModelFamily:     "claude",
					SelectedModel:   AntigravityModelClaude,
					Status:          "waiting",
					Reason:          "trigger_scheduled",
					ObservedResetAt: claudeReset,
					TargetTriggerAt: claudeReset.Add(30 * time.Second),
				},
			}
		})

		// Quota response returns advanced resets
		host.httpDoFunc = func(req HTTPRequest) (HTTPResponse, error) {
			return HTTPResponse{
				StatusCode: http.StatusOK,
				Body: []byte(`{
					"groups": [
						{"displayName": "Gemini Models", "buckets": [{"bucketId": "gemini-5h", "window": "5h", "resetTime": "2026-10-14T15:00:00Z"}]},
						{"displayName": "Claude and GPT models", "buckets": [{"bucketId": "3p-5h", "window": "5h", "resetTime": "2026-10-14T16:00:00Z"}]}
					]
				}`),
			}, nil
		}

		cfg := testConfig(t, runtime, "schedule: [\"05:00\"]\ntimezone: UTC\nantigravity_reset_margin: \"30s\"")
		cfg.StatePath = statePath
		if err := runtime.Configure(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = runtime.Shutdown(t.Context()) }()

		time.Sleep(5 * time.Second)
		synctest.Wait()

		// Advance clock past Gemini trigger (10:00:30) but before Claude (11:00:30)
		clock.Advance(31 * time.Minute) // 10:01:00
		time.Sleep(31 * time.Minute)
		synctest.Wait()

		mu.Lock()
		gExec1 := geminiExecs
		cExec1 := claudeExecs
		mu.Unlock()

		if gExec1 != 1 {
			t.Fatalf("expected Gemini to execute 1 time at 10:00:30, got %d", gExec1)
		}
		if cExec1 != 0 {
			t.Fatalf("expected Claude NOT to execute before 11:00:30, got %d", cExec1)
		}

		// Advance clock past Claude trigger (11:00:30)
		clock.Advance(60 * time.Minute) // 11:01:00
		time.Sleep(60 * time.Minute)
		synctest.Wait()
		mu.Lock()
		gExec2 := geminiExecs
		cExec2 := claudeExecs
		mu.Unlock()

		if gExec2 != 1 {
			t.Fatalf("expected Gemini still 1 execution, got %d", gExec2)
		}
		if cExec2 != 1 {
			t.Fatalf("expected Claude to execute 1 time at 11:00:30, got %d", cExec2)
		}
	})
}

func TestDynamicScheduleAntigravityStabilizationRestartDoesNotDuplicatePing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newFakeHost()
		file, document := antigravityCredential("ag-stab-1")
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document

		var modelExecs int
		var mu sync.Mutex
		host.modelExecuteFunc = func(req ModelExecuteRequest) (ModelExecuteResponse, error) {
			mu.Lock()
			modelExecs++
			mu.Unlock()
			return ModelExecuteResponse{StatusCode: http.StatusOK, Body: []byte(`{"type":"response.completed"}`)}, nil
		}

		// Target in state is already in "stabilizing" status (ping completed before crash/restart)
		statePath := filepath.Join(t.TempDir(), "state.json")
		store, err := LoadStateStore(t.Context(), statePath)
		if err != nil {
			t.Fatal(err)
		}
		priorReset := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
		_ = store.Update(t.Context(), "ag-stab-1", func(current *CredentialState) {
			current.Provider = ProviderAntigravity
			current.Status = "waiting"
			current.Targets = map[string]TargetState{
				TargetKeyGemini5h: {
					Target:            TargetKeyGemini5h,
					ModelFamily:       "gemini",
					SelectedModel:     AntigravityModelGemini,
					Status:            "stabilizing",
					Reason:            "awaiting_new_reset",
					ObservedResetAt:   priorReset,
					LastAttemptStatus: "success",
					Attempts:          1,
					Successes:         1,
				},
				TargetKeyClaude5h: {
					Target:          TargetKeyClaude5h,
					ModelFamily:     "claude",
					SelectedModel:   AntigravityModelClaude,
					Status:          "waiting",
					Reason:          "trigger_scheduled",
					ObservedResetAt: priorReset,
					TargetTriggerAt: priorReset.Add(4 * time.Hour),
				},
			}
		})

		newReset := time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)
		var quotaCalls int
		host.httpDoFunc = func(req HTTPRequest) (HTTPResponse, error) {
			mu.Lock()
			quotaCalls++
			mu.Unlock()
			return HTTPResponse{
				StatusCode: http.StatusOK,
				Body: []byte(`{
					"groups": [
						{"displayName": "Gemini Models", "buckets": [{"bucketId": "gemini-5h", "window": "5h", "resetTime": "2026-10-14T15:00:00Z"}]}
					]
				}`),
			}, nil
		}

		clock := &fakeClock{now: time.Date(2026, 10, 14, 10, 1, 0, 0, time.UTC)}
		runtime := newTestRuntime(t, host, Options{
			Now:          clock.Now,
			StartupDelay: new(time.Duration),
		})

		cfg := testConfig(t, runtime, "schedule: [\"05:00\"]\ntimezone: UTC")
		cfg.StatePath = statePath
		if err := runtime.Configure(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = runtime.Shutdown(t.Context()) }()

		time.Sleep(100 * time.Millisecond)
		synctest.Wait()

		mu.Lock()
		pings := modelExecs
		qCalls := quotaCalls
		mu.Unlock()

		// MUST NOT send duplicate model execution ping!
		if pings != 0 {
			t.Fatalf("stabilizing restart must not resend ping, got %d executions", pings)
		}
		if qCalls != 1 {
			t.Fatalf("expected 1 quota observation call, got %d", qCalls)
		}

		state := runtime.store.Credential("ag-stab-1")
		geminiTarget := state.Targets[TargetKeyGemini5h]
		if geminiTarget.Status != "waiting" || geminiTarget.Reason != "trigger_scheduled" {
			t.Fatalf("geminiTarget status/reason = %q/%q, want waiting/trigger_scheduled", geminiTarget.Status, geminiTarget.Reason)
		}
		if !geminiTarget.ObservedResetAt.Equal(newReset) {
			t.Fatalf("geminiTarget.ObservedResetAt = %v, want %v", geminiTarget.ObservedResetAt, newReset)
		}
	})
}

func TestDynamicScheduleAntigravityReconfigurationRecalculatesMargin(t *testing.T) {
	host := newFakeHost()
	agFile, agDoc := antigravityCredential("ag-reconfig-1")
	codexFile, codexDoc := credential("codex-reconfig-1", "token-codex", 1)
	host.auths = []AuthFile{agFile, codexFile}
	host.docs[agFile.AuthIndex] = agDoc
	host.docs[codexFile.AuthIndex] = codexDoc

	statePath := filepath.Join(t.TempDir(), "state.json")
	store, err := LoadStateStore(t.Context(), statePath)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: now}
	runtime := newTestRuntime(t, host, Options{
		Now:          clock.Now,
		StartupDelay: new(time.Duration),
	})

	resetTime := time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)
	_ = store.Update(t.Context(), "ag-reconfig-1", func(current *CredentialState) {
		current.Provider = ProviderAntigravity
		current.Status = "waiting"
		current.Targets = map[string]TargetState{
			TargetKeyGemini5h: {
				Target:          TargetKeyGemini5h,
				ModelFamily:     "gemini",
				SelectedModel:   AntigravityModelGemini,
				Status:          "waiting",
				Reason:          "trigger_scheduled",
				ObservedResetAt: resetTime,
				TargetTriggerAt: resetTime.Add(30 * time.Second),
			},
		}
	})
	_ = store.Update(t.Context(), "codex-reconfig-1", func(current *CredentialState) {
		current.Provider = "codex"
		current.Status = "waiting"
		current.ObservedResetAt = resetTime
		current.TargetTriggerAt = resetTime.Add(30 * time.Second)
	})

	cfg := testConfig(t, runtime, "schedule: [\"05:00\"]\ntimezone: \"Local\"\nantigravity_reset_margin: \"30s\"")
	cfg.StatePath = statePath
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runtime.Shutdown(t.Context()) }()

	// Reconfigure with margin = 2m
	cfg2 := testConfig(t, runtime, "schedule: [\"05:00\"]\ntimezone: \"Local\"\nantigravity_reset_margin: \"2m\"")
	cfg2.StatePath = statePath
	if err := runtime.Configure(t.Context(), cfg2); err != nil {
		t.Fatal(err)
	}

	// Antigravity target trigger must be recalculated to resetTime + 2m
	agState := runtime.store.Credential("ag-reconfig-1")
	expectedAGTrigger := resetTime.Add(2 * time.Minute)
	if !agState.Targets[TargetKeyGemini5h].TargetTriggerAt.Equal(expectedAGTrigger) {
		t.Fatalf("agState TargetTriggerAt = %v, want %v", agState.Targets[TargetKeyGemini5h].TargetTriggerAt, expectedAGTrigger)
	}

	// Codex trigger must remain unchanged at resetTime + 30s
	codexState := runtime.store.Credential("codex-reconfig-1")
	expectedCodexTrigger := resetTime.Add(30 * time.Second)
	if !codexState.TargetTriggerAt.Equal(expectedCodexTrigger) {
		t.Fatalf("codexState TargetTriggerAt = %v, want %v", codexState.TargetTriggerAt, expectedCodexTrigger)
	}
}

func TestDynamicScheduleAntigravityTerminalModelFailureRecoversAtNextMilestone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newFakeHost()
		file, document := antigravityCredential("ag-term-recovery-1")
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document

		var claudeAttempts int
		var mu sync.Mutex
		host.modelExecuteFunc = func(req ModelExecuteRequest) (ModelExecuteResponse, error) {
			mu.Lock()
			defer mu.Unlock()
			if req.Model == AntigravityModelGemini {
				return ModelExecuteResponse{StatusCode: http.StatusOK, Body: []byte(`{"type":"response.completed"}`)}, nil
			}
			if req.Model == AntigravityModelClaude {
				claudeAttempts++
				if claudeAttempts == 1 {
					// First attempt at 06:00 fails with terminal model failure (HTTP 400)
					return ModelExecuteResponse{StatusCode: http.StatusBadRequest, Body: []byte(`{"error":{"message":"model not available"}}`)}, nil
				}
				// Second attempt after 10:00 milestone recovers and succeeds
				return ModelExecuteResponse{StatusCode: http.StatusOK, Body: []byte(`{"type":"response.completed"}`)}, nil
			}
			return ModelExecuteResponse{StatusCode: http.StatusNotFound}, nil
		}

		host.httpDoFunc = func(req HTTPRequest) (HTTPResponse, error) {
			return HTTPResponse{
				StatusCode: http.StatusOK,
				Body: []byte(`{
					"groups": [
						{"displayName": "Gemini Models", "buckets": [{"bucketId": "gemini-5h", "window": "5h", "resetTime": "2026-10-14T15:00:00Z"}]},
						{"displayName": "Claude and GPT models", "buckets": [{"bucketId": "3p-5h", "window": "5h", "resetTime": "2026-10-14T16:00:00Z"}]}
					]
				}`),
			}, nil
		}

		// Start at 06:00 UTC (after 05:00 initial anchor, next anchor is 10:00 UTC)
		clock := &fakeClock{now: time.Date(2026, 10, 14, 6, 0, 0, 0, time.UTC)}
		runtime := newTestRuntime(t, host, Options{
			Now:          clock.Now,
			StartupDelay: new(time.Duration),
		})

		cfg := testConfig(t, runtime, "schedule: [\"05:00\", \"10:00\"]\ntimezone: UTC\nretry_cooldown: 1m\n")
		cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
		if err := runtime.Configure(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = runtime.Shutdown(t.Context()) }()

		time.Sleep(100 * time.Millisecond)
		synctest.Wait()

		mu.Lock()
		attemptsAt0600 := claudeAttempts
		mu.Unlock()
		if attemptsAt0600 != 1 {
			t.Fatalf("expected 1 initial Claude attempt, got %d", attemptsAt0600)
		}

		state := runtime.store.Credential("ag-term-recovery-1")
		claudeTarget := state.Targets[TargetKeyClaude5h]
		if claudeTarget.FailureKind != string(FailureModel) {
			t.Fatalf("expected Claude FailureKind = %q, got %q", FailureModel, claudeTarget.FailureKind)
		}

		// Advance clock to 07:00, 08:00, 09:00: Claude MUST NOT be retried before 10:00
		clock.Advance(3 * time.Hour) // Now 09:00 UTC
		time.Sleep(3 * time.Hour)
		synctest.Wait()

		mu.Lock()
		attemptsAt0900 := claudeAttempts
		mu.Unlock()
		if attemptsAt0900 != 1 {
			t.Fatalf("Claude must not retry before 10:00 milestone, got %d attempts", attemptsAt0900)
		}

		// Advance clock past 10:00 milestone (e.g. 10:01 UTC)
		clock.Advance(61 * time.Minute) // Now 10:01 UTC
		time.Sleep(61 * time.Minute)
		synctest.Wait()

		mu.Lock()
		attemptsAfter1000 := claudeAttempts
		mu.Unlock()
		if attemptsAfter1000 != 2 {
			t.Fatalf("expected Claude to be re-attempted after 10:00 milestone, got %d attempts", attemptsAfter1000)
		}

		state = runtime.store.Credential("ag-term-recovery-1")
		claudeTarget = state.Targets[TargetKeyClaude5h]
		if claudeTarget.Status != "waiting" || claudeTarget.Reason != "trigger_scheduled" {
			t.Fatalf("expected Claude target status/reason = waiting/trigger_scheduled, got %q/%q", claudeTarget.Status, claudeTarget.Reason)
		}
		if claudeTarget.FailureKind != "" {
			t.Fatalf("expected Claude FailureKind cleared, got %q", claudeTarget.FailureKind)
		}
	})
}
