package autoping

import (
	"testing"
	"time"
)

func TestNextMilestoneCalculations(t *testing.T) {
	schedule := []string{"05:00", "10:00", "15:00", "20:00"}
	loc := time.UTC

	// Case 1: Early morning before first milestone
	now := time.Date(2026, 9, 18, 4, 30, 0, 0, loc)
	nextTime, nextKey := NextMilestone(now, schedule, loc)
	if nextKey != "2026-09-18#05:00" || !nextTime.Equal(time.Date(2026, 9, 18, 5, 0, 0, 0, loc)) {
		t.Fatalf("early morning next = %s, want 2026-09-18#05:00", nextKey)
	}

	// Case 2: Exact milestone match (advances to next)
	now = time.Date(2026, 9, 18, 5, 0, 0, 0, loc)
	nextTime, nextKey = NextMilestone(now, schedule, loc)
	if nextKey != "2026-09-18#10:00" || !nextTime.Equal(time.Date(2026, 9, 18, 10, 0, 0, 0, loc)) {
		t.Fatalf("exact match next = %s, want 2026-09-18#10:00", nextKey)
	}

	// Case 3: Middle of the day
	now = time.Date(2026, 9, 18, 11, 45, 0, 0, loc)
	nextTime, nextKey = NextMilestone(now, schedule, loc)
	if nextKey != "2026-09-18#15:00" || !nextTime.Equal(time.Date(2026, 9, 18, 15, 0, 0, 0, loc)) {
		t.Fatalf("midday next = %s, want 2026-09-18#15:00", nextKey)
	}

	// Case 4: Late evening after last milestone (wraps to tomorrow)
	now = time.Date(2026, 9, 18, 22, 10, 0, 0, loc)
	nextTime, nextKey = NextMilestone(now, schedule, loc)
	if nextKey != "2026-09-19#05:00" || !nextTime.Equal(time.Date(2026, 9, 19, 5, 0, 0, 0, loc)) {
		t.Fatalf("late night next = %s, want 2026-09-19#05:00", nextKey)
	}
}

func TestCurrentOrLatestMilestone(t *testing.T) {
	schedule := []string{"05:00", "10:00", "15:00", "20:00"}
	loc := time.UTC

	// Before first milestone
	now := time.Date(2026, 9, 18, 4, 30, 0, 0, loc)
	_, _, ok := CurrentOrLatestMilestone(now, schedule, loc)
	if ok {
		t.Fatal("expected ok=false before first milestone")
	}

	// Exact at 05:00
	now = time.Date(2026, 9, 18, 5, 0, 0, 0, loc)
	target, key, ok := CurrentOrLatestMilestone(now, schedule, loc)
	if !ok || key != "2026-09-18#05:00" || !target.Equal(time.Date(2026, 9, 18, 5, 0, 0, 0, loc)) {
		t.Fatalf("at 05:00 got (%v, %s, %v)", target, key, ok)
	}

	// At 07:30
	now = time.Date(2026, 9, 18, 7, 30, 0, 0, loc)
	target, key, ok = CurrentOrLatestMilestone(now, schedule, loc)
	if !ok || key != "2026-09-18#05:00" {
		t.Fatalf("at 07:30 got (%v, %s, %v)", target, key, ok)
	}

	// At 21:00
	now = time.Date(2026, 9, 18, 21, 0, 0, 0, loc)
	target, key, ok = CurrentOrLatestMilestone(now, schedule, loc)
	if !ok || key != "2026-09-18#20:00" {
		t.Fatalf("at 21:00 got (%v, %s, %v)", target, key, ok)
	}
}
