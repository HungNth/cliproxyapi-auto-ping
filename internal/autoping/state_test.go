package autoping

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestStateSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := LoadStateStore(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	milestoneTime := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	milestoneKey := "2026-09-11#10:00"
	if err := store.Update(t.Context(), "codex-a", func(state *CredentialState) {
		state.AttemptedMilestone = milestoneKey
		state.LastProcessedMilestone = milestoneKey
		state.LastProcessedMilestoneAt = milestoneTime
		state.LastPingAt = milestoneTime.Add(5 * time.Second)
		state.Status = "waiting"
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetLastMilestone(t.Context(), milestoneKey); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadStateStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	state := reloaded.Credential("codex-a")
	if state.LastProcessedMilestone != milestoneKey || !state.LastProcessedMilestoneAt.Equal(milestoneTime) {
		t.Fatalf("reloaded state = %#v", state)
	}
	if reloaded.LastProcessedMilestone() != milestoneKey {
		t.Fatalf("reloaded global milestone = %q, want %q", reloaded.LastProcessedMilestone(), milestoneKey)
	}
}

func TestLifecycleRejectsPriorStateSchemaDestructively(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state_v1.json")
	v1Data := []byte(`{"version": 1, "credentials": {"codex-a": {"credential_id": "codex-a", "status": "waiting"}}}`)
	if err := os.WriteFile(statePath, v1Data, 0o600); err != nil {
		t.Fatal(err)
	}

	runtime := newTestRuntime(t, newFakeHost(), Options{})
	cfgYAML := "state_path: " + statePath + "\n"
	req, _ := json.Marshal(map[string]string{"config_yaml": cfgYAML})
	resp := runtime.Handle(t.Context(), "plugin.reconfigure", req)

	var envelope struct {
		OK    bool `json:"ok"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.OK || envelope.Error.Code != "configure_failed" {
		t.Fatalf("reconfigure with state v1 response = %s, want configure_failed error", string(resp))
	}
}

func TestLifecycleStatusDistinguishesAttemptAndProcessedMilestonesAcrossReload(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		statePath := filepath.Join(t.TempDir(), "state_v2.json")
		host := newFakeHost()
		fileA, docA := credential("codex-fail", "token-fail", 1)
		fileB, docB := credential("codex-ok", "token-ok", 1)
		host.auths = []AuthFile{fileA, fileB}
		host.docs[fileA.AuthIndex] = docA
		host.docs[fileB.AuthIndex] = docB

		// Simulate failure for codex-fail and success for codex-ok
		host.httpStreamFunc = func(req HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
			acct := req.Headers.Get("ChatGPT-Account-ID")
			if acct == "account-codex-fail" {
				return HTTPStreamResponse{StatusCode: 500}, []HTTPStreamChunk{{Done: true}}, nil
			}
			return successStream()
		}

		milestoneKey := "2000-01-01#00:01"
		runtime := newTestRuntime(t, host, Options{StartupDelay: new(time.Duration)})
		cfgYAML := "schedule:\n  - \"00:01\"\n  - \"05:00\"\ntimezone: UTC\nretry_cooldown: 1m\nscheduler_boost_fallback: false\nstate_path: " + statePath + "\n"
		reconfReq, _ := json.Marshal(map[string]string{"config_yaml": cfgYAML})
		reconfResp := runtime.Handle(t.Context(), "plugin.reconfigure", reconfReq)
		var envelope struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(reconfResp, &envelope); err != nil || !envelope.OK {
			t.Fatalf("initial reconfigure failed: %s", string(reconfResp))
		}

		// Advance time into 00:01 milestone to run scheduleLoop dispatch naturally
		time.Sleep(time.Minute)
		synctest.Wait()

		shutdownReq, _ := json.Marshal(map[string]any{})
		_ = runtime.Handle(t.Context(), "plugin.shutdown", shutdownReq)

		// Reload with a long startup delay so status reflects persisted state before automatic catch-up can mutate it.
		verificationDelay := 24 * time.Hour
		reloaded := newTestRuntime(t, host, Options{StartupDelay: &verificationDelay})
		reloadedResp := reloaded.Handle(t.Context(), "plugin.reconfigure", reconfReq)
		if err := json.Unmarshal(reloadedResp, &envelope); err != nil || !envelope.OK {
			t.Fatalf("reloaded reconfigure failed: %s", string(reloadedResp))
		}
		defer reloaded.Shutdown(t.Context())

		// Query status via management route
		statusReq, _ := json.Marshal(managementRequest{Method: "GET", Path: "/v0/management/cliproxyapi-auto-ping/status"})
		rawStatus := reloaded.Handle(t.Context(), "management.handle", statusReq)
		decodedStatus := decodeManagementResponse(t, rawStatus)
		var payload struct {
			Accounts []struct {
				CredentialID           string `json:"credential_id"`
				Status                 string `json:"status"`
				AttemptedMilestone     string `json:"attempted_milestone"`
				LastProcessedMilestone string `json:"last_processed_milestone"`
				NextRetryAt            string `json:"next_retry_at"`
			} `json:"accounts"`
		}
		if err := json.Unmarshal(decodedStatus.Body, &payload); err != nil {
			t.Fatal(err)
		}

		foundFail := false
		foundOK := false
		for _, acct := range payload.Accounts {
			switch acct.CredentialID {
			case "codex-fail":
				foundFail = true
				if acct.LastProcessedMilestone != "" {
					t.Fatalf("failed credential must have empty LastProcessedMilestone, got %q", acct.LastProcessedMilestone)
				}
				if acct.AttemptedMilestone != milestoneKey {
					t.Fatalf("failed credential attempted_milestone = %q, want %q", acct.AttemptedMilestone, milestoneKey)
				}
				if acct.Status != "cooldown" {
					t.Fatalf("failed credential status = %q, want cooldown", acct.Status)
				}
			case "codex-ok":
				foundOK = true
				if acct.LastProcessedMilestone != milestoneKey {
					t.Fatalf("successful credential last_processed_milestone = %q, want %q", acct.LastProcessedMilestone, milestoneKey)
				}
				if acct.AttemptedMilestone != milestoneKey {
					t.Fatalf("successful credential attempted_milestone = %q, want %q", acct.AttemptedMilestone, milestoneKey)
				}
				if acct.Status != "waiting" {
					t.Fatalf("successful credential status = %q, want waiting", acct.Status)
				}
			}
		}
		if !foundFail || !foundOK {
			t.Fatalf("missing accounts in status payload: fail=%v ok=%v", foundFail, foundOK)
		}
	})
}

func TestLifecycleDoesNotProcessMilestoneWhenSuccessStateCannotPersist(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		statePath := filepath.Join(t.TempDir(), "state.json")
		host := newFakeHost()
		file, document := credential("codex-a", "token-a", 1)
		host.auths = []AuthFile{file}
		host.docs[file.AuthIndex] = document

		var sabotage atomic.Bool
		host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
			if sabotage.CompareAndSwap(false, true) {
				if err := os.Remove(statePath); err != nil {
					return HTTPStreamResponse{}, nil, err
				}
				if err := os.Mkdir(statePath, 0o700); err != nil {
					return HTTPStreamResponse{}, nil, err
				}
			}
			return successStream()
		}

		runtime := newTestRuntime(t, host, Options{StartupDelay: new(time.Duration)})
		cfgYAML := "schedule: [\"00:01\", \"05:00\"]\ntimezone: UTC\nretry_cooldown: 24h\nstate_path: " + statePath + "\n"
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

		time.Sleep(time.Minute)
		synctest.Wait()

		statusReq, _ := json.Marshal(managementRequest{Method: "GET", Path: "/v0/management/cliproxyapi-auto-ping/status"})
		statusRaw := runtime.Handle(t.Context(), "management.handle", statusReq)
		statusResp := decodeManagementResponse(t, statusRaw)
		var payload struct {
			Accounts []struct {
				Status                 string `json:"status"`
				LastProcessedMilestone string `json:"last_processed_milestone"`
				Successes              uint64 `json:"successes"`
			} `json:"accounts"`
		}
		if err := json.Unmarshal(statusResp.Body, &payload); err != nil || len(payload.Accounts) != 1 {
			t.Fatalf("status decode failed: %v, body=%s", err, string(statusResp.Body))
		}
		account := payload.Accounts[0]
		if account.LastProcessedMilestone != "" || account.Successes != 0 || account.Status != "in_flight" {
			t.Fatalf("persistence failure incorrectly processed milestone: %#v", account)
		}
	})
}
