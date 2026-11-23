package activity

import (
	"errors"
	"time"

	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// errUnknownZone means a time zone name that is not an IANA zone.
var errUnknownZone = errors.New("unknown time zone")

// loadZone loads the IANA time zone name. It refuses the empty name and
// "Local", which time.LoadLocation turns into UTC and the server's zone.
func loadZone(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return nil, errUnknownZone
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, errUnknownZone
	}
	return loc, nil
}

// localWeek is a week of the activities of a user, from Monday 00:00 to
// the next Monday 00:00 in a zone: not the week of a challenge. A day
// starts at local midnight, or at the first instant it has where a time
// change skips midnight (scoring.StartOfDay), so a week lasts 167, 168 or
// 169 hours.
type localWeek struct {
	// Monday is the first local day of the week, as a date (midnight UTC).
	Monday time.Time
	// Bounds[i] is the start of day i (Monday is 0), Bounds[7] the end of
	// the week: day i is [Bounds[i], Bounds[i+1]).
	Bounds [8]time.Time
}

// weekStarting returns the week whose first local day, in loc, is the date
// of monday (its year, month and day, read in UTC).
func weekStarting(monday time.Time, loc *time.Location) localWeek {
	y, m, d := monday.UTC().Date()
	w := localWeek{Monday: time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
	for i := range w.Bounds {
		w.Bounds[i] = scoring.StartOfDay(y, m, d+i, loc)
	}
	return w
}

// weekOf returns the week, in loc, that holds the instant now.
func weekOf(now time.Time, loc *time.Location) localWeek {
	local := now.In(loc)
	y, m, d := local.Date()
	// time.Weekday counts from Sunday (0); the week starts on Monday.
	back := (int(local.Weekday()) + 6) % 7
	return weekStarting(time.Date(y, m, d-back, 0, 0, 0, 0, time.UTC), loc)
}

// Start is the first instant of the week.
func (w localWeek) Start() time.Time { return w.Bounds[0] }

// End is the end of the week, excluded: the start of the next one.
func (w localWeek) End() time.Time { return w.Bounds[7] }

// Day is the date of day i of the week (Monday is 0), midnight UTC.
func (w localWeek) Day(i int) time.Time { return w.Monday.AddDate(0, 0, i) }
