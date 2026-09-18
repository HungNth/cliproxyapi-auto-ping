package autoping

import (
	"fmt"
	"time"
)

// MilestoneKey formats a milestone timestamp into a canonical milestone key ("2006-01-02#15:04").
func MilestoneKey(t time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	return t.In(loc).Format("2006-01-02#15:04")
}

// NextMilestone returns the next upcoming milestone wall-clock time and its milestone key.
// If now is equal to or after a milestone, it advances to the next chronological milestone.
func NextMilestone(now time.Time, schedule []string, loc *time.Location) (time.Time, string) {
	if loc == nil {
		loc = time.Local
	}
	current := now.In(loc)
	for _, item := range schedule {
		var hh, mm int
		fmt.Sscanf(item, "%d:%d", &hh, &mm)
		target := time.Date(current.Year(), current.Month(), current.Day(), hh, mm, 0, 0, loc)
		if target.After(current) {
			return target, MilestoneKey(target, loc)
		}
	}
	var firstHH, firstMM int
	fmt.Sscanf(schedule[0], "%d:%d", &firstHH, &firstMM)
	tomorrow := current.AddDate(0, 0, 1)
	target := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), firstHH, firstMM, 0, 0, loc)
	return target, MilestoneKey(target, loc)
}

// CurrentOrLatestMilestone returns the most recent milestone of the day that has already elapsed (<= now).
// If now is before the first milestone of the day, it returns ok=false.
func CurrentOrLatestMilestone(now time.Time, schedule []string, loc *time.Location) (time.Time, string, bool) {
	if loc == nil {
		loc = time.Local
	}
	current := now.In(loc)
	for i := len(schedule) - 1; i >= 0; i-- {
		item := schedule[i]
		var hh, mm int
		fmt.Sscanf(item, "%d:%d", &hh, &mm)
		target := time.Date(current.Year(), current.Month(), current.Day(), hh, mm, 0, 0, loc)
		if !target.After(current) { // target <= current
			return target, MilestoneKey(target, loc), true
		}
	}
	return time.Time{}, "", false
}
