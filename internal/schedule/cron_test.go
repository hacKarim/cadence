package schedule

import (
	"testing"
	"time"
)

var paris = mustLoad("Europe/Paris")

func mustLoad(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, paris)
	if err != nil {
		panic(err)
	}
	return t
}

// Every day at 07:00, six-field cron.
const daily7 = "0 0 7 * * *"

func TestNotDueBeforeTheBoundary(t *testing.T) {
	fire, _, err := Due(daily7, paris, at("2026-03-04 07:00:00"), at("2026-03-04 18:00:00"), false)
	if err != nil {
		t.Fatal(err)
	}
	if fire {
		t.Error("fired before the next boundary")
	}
}

func TestDueAtTheBoundary(t *testing.T) {
	fire, _, err := Due(daily7, paris, at("2026-03-04 07:00:00"), at("2026-03-05 07:00:01"), false)
	if err != nil {
		t.Fatal(err)
	}
	if !fire {
		t.Error("did not fire at the boundary")
	}
}

// The daemon was down for a week. Firing once per missed slot would launch a
// burst of agent runs nobody asked for.
func TestCatchupFalseDropsMissedSlots(t *testing.T) {
	last := at("2026-03-04 07:00:00")
	now := at("2026-03-11 09:00:00")
	fire, baseline, err := Due(daily7, paris, last, now, false)
	if err != nil {
		t.Fatal(err)
	}
	if fire {
		t.Error("fired for a missed slot although catchup is off")
	}
	if !baseline.After(last) {
		t.Errorf("baseline %v was not advanced past the missed slots", baseline)
	}
	if baseline.After(now) {
		t.Errorf("baseline %v is in the future", baseline)
	}
}

func TestCatchupTrueFiresOnceNotPerSlot(t *testing.T) {
	last := at("2026-03-04 07:00:00")
	now := at("2026-03-11 09:00:00")
	fire, _, err := Due(daily7, paris, last, now, true)
	if err != nil {
		t.Fatal(err)
	}
	if !fire {
		t.Error("catchup did not fire after downtime")
	}
	// Firing once means the very next evaluation, with the baseline moved to
	// now, must not fire again.
	fire2, _, err := Due(daily7, paris, now, now, true)
	if err != nil {
		t.Fatal(err)
	}
	if fire2 {
		t.Error("catchup fired a second time for the same downtime")
	}
}

func TestDueRejectsFiveFieldCron(t *testing.T) {
	if _, _, err := Due("0 7 * * *", paris, at("2026-03-04 07:00:00"), at("2026-03-05 08:00:00"), false); err == nil {
		t.Error("accepted a five-field expression")
	}
}

func TestDueRespectsTimezone(t *testing.T) {
	utc := time.UTC
	// Paris is UTC+2 in July, so the 07:00 boundary falls at 05:00 UTC.
	// At 05:30 UTC on the 2nd the Paris boundary has passed and the UTC one
	// (07:00 UTC) has not.
	last := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	now := time.Date(2026, 7, 2, 5, 30, 0, 0, time.UTC)
	fireParis, _, _ := Due(daily7, paris, last, now, false)
	fireUTC, _, _ := Due(daily7, utc, last, now, false)
	if !fireParis {
		t.Error("should be due in Paris")
	}
	if fireUTC {
		t.Error("should not be due in UTC")
	}
}
