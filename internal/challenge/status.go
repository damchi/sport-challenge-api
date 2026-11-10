package challenge

import "time"

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
