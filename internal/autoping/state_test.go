package autoping

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLifecycleRejectsPriorStateSchemaDestructively(t *testing.T) {
	for _, v := range []int{1, 2} {
		t.Run(fmt.Sprintf("version %d", v), func(t *testing.T) {
			statePath := filepath.Join(t.TempDir(), fmt.Sprintf("state_v%d.json", v))
			vData := fmt.Sprintf(`{"version": %d, "credentials": {"codex-a": {"credential_id": "codex-a", "status": "waiting"}}}`, v)
			if err := os.WriteFile(statePath, []byte(vData), 0o600); err != nil {
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
				t.Fatalf("reconfigure with state v%d response = %s, want configure_failed error", v, string(resp))
			}
			if !strings.Contains(envelope.Error.Message, "unsupported state schema version: please remove stale state file") {
				t.Fatalf("error message = %q, want 'unsupported state schema version: please remove stale state file'", envelope.Error.Message)
			}
		})
	}
}

func TestVersion3StateRoundTripsDynamicFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state_v3.json")
	store, err := LoadStateStore(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	resetAt := time.Date(2026, 9, 22, 8, 0, 3, 0, time.UTC)
	targetAt := resetAt.Add(30 * time.Second)
	processedAt := resetAt.Add(-5 * time.Hour)

	if err := store.Update(t.Context(), "codex-a", func(state *CredentialState) {
		state.ObservedResetAt = resetAt
		state.TargetTriggerAt = targetAt
		state.LastProcessedResetAt = processedAt
		state.Status = "waiting"
		state.Reason = "trigger_scheduled"
	}); err != nil {
		t.Fatal(err)
	}

	reloaded, err := LoadStateStore(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	account := reloaded.Credential("codex-a")
	if !account.ObservedResetAt.Equal(resetAt) {
		t.Fatalf("observed_reset_at = %v, want %v", account.ObservedResetAt, resetAt)
	}
	if !account.TargetTriggerAt.Equal(targetAt) {
		t.Fatalf("target_trigger_at = %v, want %v", account.TargetTriggerAt, targetAt)
	}
	if !account.LastProcessedResetAt.Equal(processedAt) {
		t.Fatalf("last_processed_reset_at = %v, want %v", account.LastProcessedResetAt, processedAt)
	}
}
