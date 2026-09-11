package autoping

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestStateSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := LoadStateStore(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	resetAt := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	if err := store.Update(t.Context(), "codex-a", func(state *CredentialState) {
		state.LastProcessedResetAt = resetAt
		state.LastPingAt = resetAt.Add(5 * time.Second)
		state.ActivationSource = "auto_ping"
		state.Status = "waiting"
	}); err != nil {
		t.Fatal(err)
	}

	reloaded, err := LoadStateStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	state := reloaded.Credential("codex-a")
	if !state.LastProcessedResetAt.Equal(resetAt) || state.ActivationSource != "auto_ping" {
		t.Fatalf("reloaded state = %#v", state)
	}
}
