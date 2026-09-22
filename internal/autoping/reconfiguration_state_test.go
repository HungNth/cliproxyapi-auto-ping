package autoping

import (
	"path/filepath"
	"testing"
	"time"
)

func TestReconfigurationPreservesDynamicRetryAndTerminalState(t *testing.T) {
	startupDelay := 24 * time.Hour
	runtime := newTestRuntime(t, newFakeHost(), Options{StartupDelay: &startupDelay})
	cfg := testConfig(t, runtime, "retry_cooldown: 1m\n")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(t.Context())

	retryAt := time.Now().Add(time.Minute).UTC()
	if err := runtime.store.Update(t.Context(), "retry", func(state *CredentialState) {
		state.Status = "cooldown"
		state.FailureKind = string(FailureRetryable)
		state.NextRetryAt = retryAt
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.store.Update(t.Context(), "terminal", func(state *CredentialState) {
		state.Status = "blocked"
		state.FailureKind = string(FailureAuth)
		state.LastAttemptAt = time.Now().UTC()
	}); err != nil {
		t.Fatal(err)
	}

	reconfigured := cfg
	reconfigured.RetryCooldown = 2 * time.Minute
	if err := runtime.Configure(t.Context(), reconfigured); err != nil {
		t.Fatal(err)
	}

	retryState := runtime.store.Credential("retry")
	if retryState.FailureKind != string(FailureRetryable) || !retryState.NextRetryAt.Equal(retryAt) {
		t.Fatalf("retry state changed during reconfiguration: %#v", retryState)
	}
	terminalState := runtime.store.Credential("terminal")
	if terminalState.Status != "blocked" || terminalState.FailureKind != string(FailureAuth) {
		t.Fatalf("terminal state changed during reconfiguration: %#v", terminalState)
	}
}
