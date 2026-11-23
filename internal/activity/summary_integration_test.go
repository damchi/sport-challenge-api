//go:build integration

package activity

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

// session stores an activity of userID of sport, with label for the sport
// other, started at startedAt for duration seconds, with distance meters
// when set, deleted when deleted.
func (f fixture) session(userID, sport string, label *string, startedAt time.Time, duration int, distance *int, deleted bool) {
	f.t.Helper()
	var deletedAt *time.Time
	if deleted {
		deletedAt = &recordNow
	}
	if _, err := f.pool.Exec(f.t.Context(), `INSERT INTO activities (user_id, sport_code, sport_label, started_at, duration_seconds, distance_meters, deleted_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, userID, sport, label, startedAt, duration, distance, deletedAt); err != nil {
		f.t.Fatal(err)
	}
}

func (f fixture) otherUser() string {
	f.t.Helper()
	var id string
	if err := f.pool.QueryRow(f.t.Context(), "INSERT INTO users (auth_subject, pseudo) VALUES ('email|other', 'other') RETURNING id").Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	return id
}

// TestMySummary sums the week of recordNow (Thursday June 11, 2026): an
// activity started at 23:30 on Sunday June 14 in Toronto is already Monday
// in Tokyo, the next week there.
func TestMySummary(t *testing.T) {
	f := setupRecord(t)
	other := f.otherUser()
	padel, squash := ptr("Padel"), ptr("Squash")
	lateSunday := time.Date(2026, 6, 15, 3, 30, 0, 0, time.UTC) // 23:30 in Toronto, 12:30 Monday in Tokyo
	f.session(f.user, "run", nil, toronto(t, 2026, 6, 8, 7), 1800, ptr(5000), false)
	f.session(f.user, "run", nil, toronto(t, 2026, 6, 8, 18), 1500, ptr(4000), false)
	f.session(f.user, "yoga", nil, toronto(t, 2026, 6, 10, 7), 3600, nil, false)
	f.session(f.user, "run", nil, lateSunday, 1200, ptr(3000), false)
	f.session(f.user, "other", padel, toronto(t, 2026, 6, 1, 7), 3600, nil, false)    // the week before
	f.session(f.user, "other", squash, toronto(t, 2026, 5, 1, 7), 3600, nil, false)   // long before
	f.session(f.user, "run", nil, toronto(t, 2026, 6, 9, 7), 3600, ptr(10_000), true) // deleted
	f.session(other, "run", nil, toronto(t, 2026, 6, 9, 7), 3600, ptr(10_000), false) // someone else
	svc := f.service()

	sum, err := svc.MySummary(t.Context(), f.user, "America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	w := sum.CurrentWeek
	if w.Totals != (Totals{Sessions: 4, DurationSeconds: 8100, DistanceMeters: 12_000}) {
		t.Fatalf("Toronto week totals %+v, want the 4 activities of June 8 to 14", w.Totals)
	}
	wantDays := []Day{
		{Date: date(2026, 6, 8), Sessions: 2, DurationSeconds: 3300},
		{Date: date(2026, 6, 9)},
		{Date: date(2026, 6, 10), Sessions: 1, DurationSeconds: 3600},
		{Date: date(2026, 6, 11)},
		{Date: date(2026, 6, 12)},
		{Date: date(2026, 6, 13)},
		{Date: date(2026, 6, 14), Sessions: 1, DurationSeconds: 1200},
	}
	if !reflect.DeepEqual(w.Days, wantDays) {
		t.Fatalf("Toronto days %+v, want %+v", w.Days, wantDays)
	}
	wantSports := []SportCount{{SportCode: "run", Count: 3}, {SportCode: "other", Count: 2}, {SportCode: "yoga", Count: 1}}
	if !reflect.DeepEqual(sum.Sports, wantSports) || sum.Total != 6 {
		t.Fatalf("sports %+v, total %d, want %+v and 6: the others grouped, deleted ones left out", sum.Sports, sum.Total, wantSports)
	}
	if sum.TotalDurationSeconds != 15_300 {
		t.Fatalf("total duration %d, want 15300: the 6 activities counted, the deleted one and the other user's left out", sum.TotalDurationSeconds)
	}

	// In Tokyo, recordNow (01:00 Friday June 12) is in the same week, but
	// the Monday evening run of Toronto falls on Tuesday, and the Sunday
	// evening one on the next Monday.
	sum, err = svc.MySummary(t.Context(), f.user, "Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	if w := sum.CurrentWeek; w.Sessions != 3 || w.DistanceMeters != 9000 || w.Days[0].Sessions != 1 || w.Days[1].Sessions != 1 || w.Days[6].Sessions != 0 {
		t.Fatalf("Tokyo week %+v, want 3 activities, one on Monday, one on Tuesday, the late Sunday run left for the next week", w)
	}
}

func TestMyWeeks(t *testing.T) {
	f := setupRecord(t)
	// Fall back on Sunday November 1, 2026: the week of October 26 lasts
	// 169 hours, and an activity at 23:30 on that Sunday still belongs to it.
	f.session(f.user, "run", nil, time.Date(2026, 11, 2, 4, 30, 0, 0, time.UTC), 1800, ptr(5000), false) // Sunday Nov 1, 23:30 EST
	f.session(f.user, "run", nil, toronto(t, 2026, 10, 26, 0), 1200, ptr(3000), false)                   // Monday Oct 26, 00:00 EDT
	f.session(f.user, "yoga", nil, toronto(t, 2026, 11, 2, 0), 3600, nil, false)                         // Monday Nov 2, 00:00 EST
	f.session(f.user, "other", ptr("Padel"), toronto(t, 2026, 11, 4, 18), 3600, nil, false)
	f.session(f.user, "run", nil, toronto(t, 2026, 10, 12, 7), 600, ptr(2000), false)
	f.session(f.user, "run", nil, toronto(t, 2026, 10, 20, 7), 600, ptr(2000), true) // deleted: no week of October 19
	f.session(f.otherUser(), "run", nil, toronto(t, 2026, 10, 20, 7), 600, ptr(2000), false)
	svc := f.service()

	var all []Week
	var after *WeekPosition
	for range 5 {
		page, err := svc.MyWeeks(t.Context(), f.user, "America/Toronto", nil, 2, after)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, page.Items...)
		if page.Next == nil {
			break
		}
		after = page.Next
	}
	want := []Week{
		{StartsAt: toronto(t, 2026, 11, 2, 0), EndsAt: toronto(t, 2026, 11, 9, 0), Totals: Totals{Sessions: 2, DurationSeconds: 7200}},
		{StartsAt: toronto(t, 2026, 10, 26, 0), EndsAt: toronto(t, 2026, 11, 2, 0), Totals: Totals{Sessions: 2, DurationSeconds: 3000, DistanceMeters: 8000}},
		{StartsAt: toronto(t, 2026, 10, 12, 0), EndsAt: toronto(t, 2026, 10, 19, 0), Totals: Totals{Sessions: 1, DurationSeconds: 600, DistanceMeters: 2000}},
	}
	if !reflect.DeepEqual(all, want) {
		t.Fatalf("weeks %+v, want %+v", all, want)
	}
	if d := all[1].EndsAt.Sub(all[1].StartsAt); d != 169*time.Hour {
		t.Fatalf("week of October 26 lasts %s, want 169h", d)
	}

	// The filter keeps the weeks of the sport only; other groups the labels.
	for _, tt := range []struct {
		sport string
		want  int
	}{{"run", 2}, {"other", 1}, {"yoga", 1}, {"swim", 0}, {"curling", 0}} {
		sport := tt.sport
		page, err := svc.MyWeeks(t.Context(), f.user, "America/Toronto", &sport, 10, nil)
		if err != nil || len(page.Items) != tt.want {
			t.Fatalf("weeks of %s = %+v, %v, want %d", sport, page.Items, err, tt.want)
		}
	}
}

// TestWeeksUnknownZone gives errUnknownZone for a zone PostgreSQL does not
// know, whatever Go knows.
func TestWeeksUnknownZone(t *testing.T) {
	f := setupRecord(t)
	f.session(f.user, "run", nil, toronto(t, 2026, 10, 12, 7), 600, nil, false)
	_, err := NewRepository(f.gdb).Weeks(t.Context(), WeekQuery{UserID: f.user, Zone: "Mars/Olympus_Mons", Limit: 10})
	if !errors.Is(err, errUnknownZone) {
		t.Fatalf("error = %v, want errUnknownZone", err)
	}
}

func TestListFiltersBySport(t *testing.T) {
	f := setupRecord(t)
	f.session(f.user, "run", nil, toronto(t, 2026, 6, 3, 7), 600, nil, false)
	f.session(f.user, "other", ptr("Padel"), toronto(t, 2026, 6, 4, 7), 600, nil, false)
	f.session(f.user, "other", ptr("Squash"), toronto(t, 2026, 6, 5, 7), 600, nil, false)
	repo := NewRepository(f.gdb)
	for sport, want := range map[string]int{"other": 2, "run": 1, "curling": 0} {
		got, err := repo.List(t.Context(), ListQuery{UserIDs: []string{f.user}, SportCode: &sport, Limit: 10})
		if err != nil || len(got) != want {
			t.Fatalf("List(%s) = %d activities, %v, want %d", sport, len(got), err, want)
		}
		for _, a := range got {
			if a.SportCode != sport {
				t.Fatalf("List(%s) gave an activity of %s", sport, a.SportCode)
			}
		}
	}
}

// climb stores a run of userID started at startedAt, of 5 km, with gain
// meters of elevation gain when set, deleted when deleted.
func (f fixture) climb(userID string, startedAt time.Time, gain *int, deleted bool) {
	f.t.Helper()
	var deletedAt *time.Time
	if deleted {
		deletedAt = &recordNow
	}
	if _, err := f.pool.Exec(f.t.Context(), `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters, elevation_gain_meters, deleted_at)
		VALUES ($1, 'run', $2, 1800, 5000, $3, $4)`, userID, startedAt, gain, deletedAt); err != nil {
		f.t.Fatal(err)
	}
}

// TestWeeklyElevationGain sums the elevation gains of a week (SC-API-150):
// an activity without one adds 0, a deleted one and someone else's are left
// out, and a week holds what started from Monday 00:00 to the next, local.
func TestWeeklyElevationGain(t *testing.T) {
	f := setupRecord(t)
	svc := f.service()

	// A week without any activity: 0.
	sum, err := svc.MySummary(t.Context(), f.user, "America/Toronto")
	if err != nil || sum.CurrentWeek.ElevationGainMeters != 0 {
		t.Fatalf("empty week = %+v, %v, want 0 m", sum.CurrentWeek, err)
	}

	f.climb(f.user, toronto(t, 2026, 6, 8, 0), ptr(300), false)                     // first instant of the week of recordNow
	f.climb(f.user, toronto(t, 2026, 6, 9, 7), nil, false)                          // no elevation gain: adds 0
	f.climb(f.user, toronto(t, 2026, 6, 10, 7), ptr(0), false)                      // entered as 0
	f.climb(f.user, time.Date(2026, 6, 15, 3, 59, 0, 0, time.UTC), ptr(450), false) // Sunday 23:59 in Toronto
	f.climb(f.user, toronto(t, 2026, 6, 15, 0), ptr(1000), false)                   // next Monday 00:00: the next week
	f.climb(f.user, toronto(t, 2026, 6, 8, 0).Add(-time.Minute), ptr(2000), false)  // previous Sunday 23:59
	f.climb(f.user, toronto(t, 2026, 6, 11, 7), ptr(5000), true)                    // deleted
	f.climb(f.otherUser(), toronto(t, 2026, 6, 11, 7), ptr(5000), false)            // someone else

	sum, err = svc.MySummary(t.Context(), f.user, "America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	if w := sum.CurrentWeek; w.Sessions != 4 || w.ElevationGainMeters != 750 {
		t.Fatalf("week of recordNow = %+v, want 4 activities and 750 m", w.Totals)
	}

	page, err := svc.MyWeeks(t.Context(), f.user, "America/Toronto", nil, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]int, 0, len(page.Items))
	for _, w := range page.Items {
		got = append(got, w.ElevationGainMeters)
	}
	// Weeks of June 15, June 8 and June 1, most recent first.
	if want := []int{1000, 750, 2000}; !reflect.DeepEqual(got, want) {
		t.Fatalf("weekly elevation gains %v, want %v", got, want)
	}

	// The sport filter keeps the same activities as for the distance.
	yoga := "yoga"
	f.session(f.user, "yoga", nil, toronto(t, 2026, 6, 12, 7), 3600, nil, false)
	page, err = svc.MyWeeks(t.Context(), f.user, "America/Toronto", &yoga, 10, nil)
	if err != nil || len(page.Items) != 1 || page.Items[0].ElevationGainMeters != 0 {
		t.Fatalf("yoga weeks = %+v, %v, want one week with 0 m", page.Items, err)
	}
}
