// Package schedule decides when a workflow is due and runs the background
// daemon that triggers it.
package schedule

import (
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

// parser accepts the 6-field form, where the leading field is seconds.
var parser = cron.NewParser(
	cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

// Due reports whether a workflow should run now, given when it last ran.
//
// It also returns the baseline to record for the next evaluation. When more
// than one boundary has passed, the daemon was down: firing once per missed
// slot would launch a burst of agent runs nobody asked for, so by default the
// missed slots are dropped and only the baseline moves. With catchup set, a
// single run fires to cover the gap.
func Due(schedule string, loc *time.Location, last, now time.Time, catchup bool) (bool, time.Time, error) {
	if loc == nil {
		loc = time.Local
	}
	sched, err := parser.Parse(schedule)
	if err != nil {
		return false, last, fmt.Errorf("schedule %q is not a 6-field cron expression: %w", schedule, err)
	}

	next := sched.Next(last.In(loc))
	if next.After(now) {
		return false, last, nil
	}

	missedMore := !sched.Next(next).After(now)
	if !missedMore {
		return true, next, nil
	}
	if catchup {
		return true, now, nil
	}

	// Walk forward to the most recent boundary at or before now, so the next
	// evaluation waits for a real boundary instead of firing immediately.
	baseline := next
	for {
		n := sched.Next(baseline)
		if n.After(now) {
			return false, baseline, nil
		}
		baseline = n
	}
}

// Next returns the next scheduled time after a given instant.
func Next(schedule string, loc *time.Location, after time.Time) (time.Time, error) {
	if loc == nil {
		loc = time.Local
	}
	sched, err := parser.Parse(schedule)
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(after.In(loc)), nil
}
