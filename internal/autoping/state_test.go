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
	milestoneTime := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	milestoneKey := "2026-09-11#10:00"
	if err := store.Update(t.Context(), "codex-a", func(state *CredentialState) {
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
