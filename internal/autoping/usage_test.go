package autoping

import (
	"errors"
	"testing"
	"time"
)

func TestParseFiveHourObservationUsesDurationNotWindowPosition(t *testing.T) {
	observedAt := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	observation, err := ParseFiveHourObservation([]byte(`{
        "rate_limit": {
            "primary_window": {"limit_window_seconds": 604800, "reset_at": "2026-09-18T09:00:00Z"},
            "secondary_window": {"limit_window_seconds": 18000, "reset_at": "2026-09-11T10:00:00Z", "used_percent": 0}
        }
    }`), observedAt)
	if err != nil {
		t.Fatal(err)
	}
	if observation.LimitWindowSeconds != 18000 || observation.ResetAt.Hour() != 10 {
		t.Fatalf("observation = %#v", observation)
	}
}

func TestParseFiveHourObservationRejectsLongWindowOnly(t *testing.T) {
	_, err := ParseFiveHourObservation([]byte(`{"rate_limit":{"primary_window":{"limit_window_seconds":604800,"reset_at":"2026-09-18T09:00:00Z"}}}`), time.Now())
	if !errors.Is(err, ErrNoFiveHourWindow) {
		t.Fatalf("error = %v, want ErrNoFiveHourWindow", err)
	}
}

func TestEvaluateObservationBeforeAndAfterFixedReset(t *testing.T) {
	resetAt := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	previous := &Observation{ResetAt: resetAt, ObservedAt: resetAt.Add(-time.Minute), LimitWindowSeconds: fiveHourSeconds}
	state := CredentialState{LastObservation: previous}

	before := EvaluateObservation(state, *previous, resetAt.Add(-time.Second), 5*time.Second)
	if before.Kind != DecisionWaiting || before.Reason != "reset_not_reached" {
		t.Fatalf("before reset decision = %#v", before)
	}
	after := EvaluateObservation(state, Observation{ResetAt: resetAt, ObservedAt: resetAt.Add(10 * time.Second), LimitWindowSeconds: fiveHourSeconds}, resetAt.Add(10*time.Second), 5*time.Second)
	if after.Kind != DecisionReady || !after.Boundary.Equal(resetAt) {
		t.Fatalf("after reset decision = %#v", after)
	}
}

func TestEvaluateObservationDetectsInactiveSlidingWindow(t *testing.T) {
	previous := Observation{
		ResetAt:            time.Date(2026, 9, 11, 15, 0, 0, 0, time.UTC),
		ObservedAt:         time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC),
		LimitWindowSeconds: fiveHourSeconds,
		UsedPercent:        0, HasUsage: true,
	}
	current := previous
	current.ResetAt = current.ResetAt.Add(time.Minute)
	current.ObservedAt = current.ObservedAt.Add(time.Minute)

	decision := EvaluateObservation(CredentialState{LastObservation: &previous}, current, current.ObservedAt, 5*time.Second)
	if decision.Kind != DecisionReady || decision.Reason != "inactive_window_sliding" || !decision.Boundary.Equal(previous.ResetAt) {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestEvaluateObservationConfirmsExternalActivation(t *testing.T) {
	previous := Observation{
		ResetAt:            time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC),
		ObservedAt:         time.Date(2026, 9, 11, 9, 59, 0, 0, time.UTC),
		LimitWindowSeconds: fiveHourSeconds,
	}
	jumped := Observation{
		ResetAt:            time.Date(2026, 9, 11, 15, 1, 0, 0, time.UTC),
		ObservedAt:         time.Date(2026, 9, 11, 10, 1, 0, 0, time.UTC),
		LimitWindowSeconds: fiveHourSeconds,
	}
	pending := EvaluateObservation(CredentialState{LastObservation: &previous}, jumped, jumped.ObservedAt, 5*time.Second)
	if pending.Kind != DecisionWaiting || pending.PendingTransition == nil {
		t.Fatalf("pending decision = %#v", pending)
	}
	state := CredentialState{LastObservation: &jumped, PendingTransition: pending.PendingTransition}
	stable := jumped
	stable.ObservedAt = stable.ObservedAt.Add(time.Minute)
	decision := EvaluateObservation(state, stable, stable.ObservedAt, 5*time.Second)
	if decision.Kind != DecisionExternal || !decision.Boundary.Equal(previous.ResetAt) {
		t.Fatalf("external decision = %#v", decision)
	}
}
