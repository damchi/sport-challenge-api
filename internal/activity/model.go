package activity

import "time"

// SourceManual marks an activity entered by hand (the only source in V1).
const SourceManual = "manual"

// Activity is a row of the activities table.
type Activity struct {
	ID         string
	UserID     string
	SportCode  string
	SportLabel *string
	StartedAt  time.Time
	// DurationSeconds is always set; DistanceMeters is nil without distance.
	DurationSeconds int
	DistanceMeters  *int
	// ElevationGainMeters is nil when not entered; shown only.
	ElevationGainMeters *int
	Source              string
	// IdempotencyKey is the key the client sent with the creation, if any.
	IdempotencyKey *string
}

// Recorded is an activity once recorded, with what it brings now to each
// challenge where it counts (possibly none).
type Recorded struct {
	Activity
	Contributions []Contribution
	// Created is false when the idempotency key gave an activity recorded
	// before: nothing was written.
	Created bool
	// Locked is true once its week is locked in a challenge where it counts
	// (IsLocked): it can no longer be changed nor deleted.
	Locked bool
}

// ListQuery selects a page of the activities of some users, deleted ones
// left out: those started in [From, To), a nil bound being open, and of
// sport SportCode only when set.
type ListQuery struct {
	// UserIDs are the authors; at least one.
	UserIDs   []string
	From      *time.Time
	To        *time.Time
	SportCode *string
	Limit     int
	// After is the position of the last item of the previous page.
	After *Position
}

// Position is the place of an activity in a listing, most recent first: the
// pagination key (started_at, id), encoded in the cursor.
type Position struct {
	StartedAt time.Time `json:"s"`
	ID        string    `json:"i"`
}

// Page is a page of activities; Next is nil on the last page.
type Page struct {
	Items []Recorded
	Next  *Position
}

// Name is what a user shows the others: never the e-mail. Each field is nil
// for a name not set yet.
type Name struct {
	Pseudo    *string
	FirstName *string
	LastName  *string
}

// Authored is an activity in the list of a challenge, with the public name
// of its author.
type Authored struct {
	Recorded
	Author Name
}

// AuthoredPage is a page of the activities of a challenge; Next is nil on
// the last page.
type AuthoredPage struct {
	Items []Authored
	Next  *Position
}

// Totals sum a set of activities: their number, their durations, their
// distances and their elevation gains, an activity without distance or
// elevation gain adding 0.
type Totals struct {
	Sessions            int
	DurationSeconds     int
	DistanceMeters      int
	ElevationGainMeters int
}

// Day is a local day of a week of activities, with its totals (the distance
// and the elevation gain left out).
type Day struct {
	// Date is the local day, as a date (midnight UTC).
	Date            time.Time
	Sessions        int
	DurationSeconds int
}

// Week is a week of activities, Monday 00:00 to the next Monday 00:00 in a
// zone (localWeek), with its totals.
type Week struct {
	StartsAt time.Time
	EndsAt   time.Time
	Totals
}

// CurrentWeek is the week that holds now, with its 7 days, Monday first.
type CurrentWeek struct {
	Week
	Days []Day
}

// SportCount is the number of activities of a sport.
type SportCount struct {
	SportCode string
	Count     int
}

// Summary sums the activities of a user: the current week, the number of
// activities of each sport, most frequent first, Total in all, lasting
// TotalDurationSeconds; and ChallengeCount, the number of challenges the
// user joined.
type Summary struct {
	CurrentWeek          CurrentWeek
	Sports               []SportCount
	Total                int
	TotalDurationSeconds int
	ChallengeCount       int
}

// WeekQuery selects a page of the weeks of the activities of UserID, deleted
// ones left out, cut in the IANA zone Zone: those with an activity (of sport
// SportCode only, when set) started before Before, when set, most recent
// first.
type WeekQuery struct {
	UserID    string
	Zone      string
	SportCode *string
	Before    *time.Time
	Limit     int
}

// WeekTotals are the totals of the week whose first local day is Monday (a
// date, midnight UTC), as the storage gives them.
type WeekTotals struct {
	Monday time.Time
	Totals
}

// WeekPosition is the place of a week in a listing, most recent first: its
// first local day, encoded in the cursor.
type WeekPosition struct {
	Monday time.Time `json:"m"`
}

// WeekPage is a page of weeks; Next is nil on the last page.
type WeekPage struct {
	Items []Week
	Next  *WeekPosition
}
