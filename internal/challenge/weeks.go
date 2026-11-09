package challenge

import "time"

// The weeks of a challenge are counted in local days of its time zone
// (AddDate), never as fixed 7 × 24 h durations: a week across a time change
// lasts 167 or 169 hours.

// RegistrationDeadline is the end of week 1, when joining closes.
func RegistrationDeadline(startsAt time.Time, loc *time.Location) time.Time {
	return startsAt.In(loc).AddDate(0, 0, 7).UTC()
}

// CurrentWeek returns the number of the week holding now (1 for the first)
// and the instant it ends, or 0 and the zero time outside [startsAt,
// endsAt).
func CurrentWeek(now, startsAt, endsAt time.Time, loc *time.Location) (int, time.Time) {
	if now.Before(startsAt) || !now.Before(endsAt) {
		return 0, time.Time{}
	}
	start := startsAt.In(loc)
	for week := 1; ; week++ {
		end := start.AddDate(0, 0, 7*week).UTC()
		// A window holds whole weeks, but its end stays the limit.
		if end.After(endsAt) {
			end = endsAt.UTC()
		}
		if now.Before(end) {
			return week, end
		}
	}
}

// StatusAt is the status of a challenge at now.
func StatusAt(now, startsAt, endsAt time.Time) string {
	switch {
	case now.Before(startsAt):
		return StatusUpcoming
	case now.Before(endsAt):
		return StatusOngoing
	default:
		return StatusFinished
	}
}
