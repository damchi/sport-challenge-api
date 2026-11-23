package activity

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/httpx"
)

// wantInvalid fails unless err is 400 validation_failed on field.
func wantInvalid(t *testing.T, err error, field string) {
	t.Helper()
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != apperr.CodeValidationFailed || appErr.Params[apperr.FieldsParam] != field {
		t.Fatalf("error = %v, want validation_failed on %s", err, field)
	}
}

// TestServiceMySummary sums the week of the clock (Thursday June 11, 2026)
// in the zone asked, day by day.
func TestServiceMySummary(t *testing.T) {
	days := make([]Totals, 7)
	days[0] = Totals{Sessions: 2, DurationSeconds: 5400, DistanceMeters: 12_000}
	days[3] = Totals{Sessions: 1, DurationSeconds: 3600}
	sports := []SportCount{{SportCode: "run", Count: 4}, {SportCode: "bike", Count: 1}, {SportCode: "other", Count: 1}}
	repo := &fakeRepository{days: days, sports: sports, sportsDuration: 21_600}
	joined := fakeParticipations{challenges: []Participation{challengeOf(t, "c1"), challengeOf(t, "c2")}}
	sum, err := newService(t, repo, joined).MySummary(t.Context(), actor, "America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	w := sum.CurrentWeek
	if !w.StartsAt.Equal(toronto(t, 2026, 6, 8, 0)) || !w.EndsAt.Equal(toronto(t, 2026, 6, 15, 0)) {
		t.Fatalf("week [%s, %s), want Monday June 8 to Monday June 15, Toronto", w.StartsAt, w.EndsAt)
	}
	if w.Totals != (Totals{Sessions: 3, DurationSeconds: 9000, DistanceMeters: 12_000}) {
		t.Fatalf("week totals %+v, want the sum of the days", w.Totals)
	}
	if len(w.Days) != 7 || !w.Days[0].Date.Equal(date(2026, 6, 8)) || !w.Days[6].Date.Equal(date(2026, 6, 14)) {
		t.Fatalf("days %+v, want Monday June 8 to Sunday June 14", w.Days)
	}
	if w.Days[0].Sessions != 2 || w.Days[0].DurationSeconds != 5400 || w.Days[3].Sessions != 1 || w.Days[1].Sessions != 0 {
		t.Fatalf("days %+v, want the totals of each day", w.Days)
	}
	if len(repo.bounds) != 8 || !repo.bounds[1].Equal(toronto(t, 2026, 6, 9, 0)) || !repo.bounds[7].Equal(w.EndsAt) {
		t.Fatalf("bounds %v, want the 8 local midnights of the week", repo.bounds)
	}
	if !reflect.DeepEqual(sum.Sports, sports) || sum.Total != 6 {
		t.Fatalf("sports %+v, total %d, want the counts and their sum 6", sum.Sports, sum.Total)
	}
	if sum.TotalDurationSeconds != 21_600 || sum.ChallengeCount != 2 {
		t.Fatalf("total duration %d, challenge count %d, want 21600 and the 2 challenges joined", sum.TotalDurationSeconds, sum.ChallengeCount)
	}
}

func TestServiceMySummaryEmptyWeek(t *testing.T) {
	sum, err := newService(t, &fakeRepository{}, fakeParticipations{}).MySummary(t.Context(), actor, "Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	w := sum.CurrentWeek
	if w.Totals != (Totals{}) || len(w.Days) != 7 || sum.Total != 0 || len(sum.Sports) != 0 || sum.TotalDurationSeconds != 0 || sum.ChallengeCount != 0 {
		t.Fatalf("summary %+v, want zeros and 7 days", sum)
	}
	for i, d := range w.Days {
		if !d.Date.Equal(date(2026, 6, 8+i)) || d.Sessions != 0 || d.DurationSeconds != 0 {
			t.Fatalf("day %d = %+v, want June %d, empty", i, d, 8+i)
		}
	}
	// Midnight in Tokyo is 15:00 UTC the day before.
	if !w.StartsAt.Equal(time.Date(2026, 6, 7, 15, 0, 0, 0, time.UTC)) {
		t.Fatalf("week starts at %s, want Monday 00:00 in Tokyo", w.StartsAt)
	}
}

func TestServiceMySummaryRefusals(t *testing.T) {
	repo := &fakeRepository{}
	_, err := newService(t, repo, fakeParticipations{}).MySummary(t.Context(), actor, "Mars/Olympus_Mons")
	wantInvalid(t, err, "tz")
	if repo.bounds != nil {
		t.Fatal("activities read despite the refusal")
	}

	storageErr := errors.New("connection reset")
	if _, err := newService(t, &fakeRepository{err: storageErr}, fakeParticipations{}).MySummary(t.Context(), actor, "UTC"); !errors.Is(err, storageErr) {
		t.Fatalf("error = %v, want the cause kept", err)
	}
	if _, err := newService(t, &fakeRepository{}, fakeParticipations{err: storageErr}).MySummary(t.Context(), actor, "UTC"); !errors.Is(err, storageErr) {
		t.Fatalf("participations failure: error = %v, want the cause kept", err)
	}
}

func TestServiceMyWeeks(t *testing.T) {
	sport := "run"
	repo := &fakeRepository{weeks: []WeekTotals{
		{Monday: date(2026, 11, 2), Totals: Totals{Sessions: 4, DurationSeconds: 9360, DistanceMeters: 26_000}},
		{Monday: date(2026, 10, 26), Totals: Totals{Sessions: 1, DurationSeconds: 1800}},
		{Monday: date(2026, 10, 19), Totals: Totals{Sessions: 1, DurationSeconds: 60}},
	}}
	svc := newService(t, repo, fakeParticipations{})
	page, err := svc.MyWeeks(t.Context(), actor, "America/Toronto", &sport, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if q := repo.weekQuery; q.UserID != actor || q.Zone != "America/Toronto" || q.SportCode != &sport || q.Limit != 3 || q.Before != nil {
		t.Fatalf("query %+v, want the actor's weeks of run in Toronto, one more than the limit", q)
	}
	if len(page.Items) != 2 || page.Next == nil || !page.Next.Monday.Equal(date(2026, 10, 26)) {
		t.Fatalf("page %+v, want 2 weeks and a next page after October 26", page)
	}
	if got := page.Items[0]; !got.StartsAt.Equal(toronto(t, 2026, 11, 2, 0)) || got.Totals != repo.weeks[0].Totals {
		t.Fatalf("first week %+v, want Monday November 2 with its totals", got)
	}
	// The week of the fall back lasts 169 hours.
	if got := page.Items[1]; !got.StartsAt.Equal(toronto(t, 2026, 10, 26, 0)) || got.EndsAt.Sub(got.StartsAt) != 169*time.Hour {
		t.Fatalf("second week [%s, %s), want the 169 h week of October 26", got.StartsAt, got.EndsAt)
	}

	repo.weeks = repo.weeks[2:]
	page, err = svc.MyWeeks(t.Context(), actor, "America/Toronto", nil, 2, page.Next)
	if err != nil {
		t.Fatal(err)
	}
	if b := repo.weekQuery.Before; b == nil || !b.Equal(toronto(t, 2026, 10, 26, 0)) {
		t.Fatalf("before %v, want the start of the week of the cursor", b)
	}
	if len(page.Items) != 1 || page.Next != nil {
		t.Fatalf("page %+v, want the last week and no next page", page)
	}
}

func TestServiceMyWeeksRefusals(t *testing.T) {
	tests := []struct {
		name  string
		tz    string
		limit int
		repo  *fakeRepository
		field string
	}{
		{name: "empty page", tz: "UTC", limit: 0, repo: &fakeRepository{}, field: "limit"},
		{name: "unknown zone", tz: "Mars/Olympus_Mons", limit: 20, repo: &fakeRepository{}, field: "tz"},
		{name: "Local", tz: "Local", limit: 20, repo: &fakeRepository{}, field: "tz"},
		{name: "zone the database does not know", tz: "UTC", limit: 20, repo: &fakeRepository{weeksErr: fmt.Errorf("list weeks: %w", errUnknownZone)}, field: "tz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newService(t, tt.repo, fakeParticipations{}).MyWeeks(t.Context(), actor, tt.tz, nil, tt.limit, nil)
			wantInvalid(t, err, tt.field)
		})
	}
}

func TestServiceMineSport(t *testing.T) {
	sport := otherSport
	repo := &fakeRepository{}
	if _, err := newService(t, repo, fakeParticipations{}).Mine(t.Context(), actor, Filter{Sport: &sport}, 20, nil); err != nil {
		t.Fatal(err)
	}
	if q := repo.listed; q == nil || q.SportCode == nil || *q.SportCode != otherSport {
		t.Fatalf("query %+v, want the activities of the sport other", q)
	}
}

func TestGetMyActivitySummary(t *testing.T) {
	days := make([]Totals, 7)
	days[2] = Totals{Sessions: 1, DurationSeconds: 3600, DistanceMeters: 10_000}
	repo := &fakeRepository{days: days, sports: []SportCount{{SportCode: "run", Count: 1}}, sportsDuration: 3600}
	joined := fakeParticipations{challenges: []Participation{challengeOf(t, "c1")}}
	rec, body := serve(t, newService(t, repo, joined), http.MethodGet, "/api/v1/me/activities/summary?tz=America/Toronto", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d (%s), want 200", rec.Code, rec.Body)
	}
	week, _ := body["current_week"].(map[string]any)
	if week["starts_at"] != "2026-06-08T04:00:00Z" || week["ends_at"] != "2026-06-15T04:00:00Z" || week["sessions"] != 1.0 || week["distance_meters"] != 10_000.0 {
		t.Fatalf("current_week %v, want the Toronto week with its totals", week)
	}
	ds, _ := week["days"].([]any)
	if len(ds) != 7 {
		t.Fatalf("days %v, want 7", week["days"])
	}
	if wed, _ := ds[2].(map[string]any); wed["date"] != "2026-06-10" || wed["duration_seconds"] != 3600.0 {
		t.Fatalf("Wednesday %v, want June 10 with its hour", wed)
	}
	if body["total"] != 1.0 || body["total_duration_seconds"] != 3600.0 || body["challenge_count"] != 1.0 {
		t.Fatalf("total %v, total_duration_seconds %v, challenge_count %v, want 1, 3600 and 1", body["total"], body["total_duration_seconds"], body["challenge_count"])
	}
}

func TestMyActivityRoutesRefusals(t *testing.T) {
	tuesdayCursor, err := httpx.EncodeCursor(WeekPosition{Monday: date(2026, 6, 9)})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		target  string
		headers map[string]string
		status  int
		code    string
	}{
		{name: "summary without a token", target: "/api/v1/me/activities/summary?tz=UTC", headers: map[string]string{"Authorization": ""}, status: http.StatusUnauthorized, code: "unauthenticated"},
		{name: "summary without tz", target: "/api/v1/me/activities/summary", status: http.StatusBadRequest, code: "validation_failed"},
		{name: "summary with an unknown tz", target: "/api/v1/me/activities/summary?tz=Mars/Olympus_Mons", status: http.StatusBadRequest, code: "validation_failed"},
		{name: "weeks without a token", target: "/api/v1/me/activities/weeks?tz=UTC", headers: map[string]string{"Authorization": ""}, status: http.StatusUnauthorized, code: "unauthenticated"},
		{name: "weeks with an unknown tz", target: "/api/v1/me/activities/weeks?tz=Nowhere", status: http.StatusBadRequest, code: "validation_failed"},
		{name: "weeks with a malformed sport", target: "/api/v1/me/activities/weeks?tz=UTC&sport=Run!", status: http.StatusBadRequest, code: "validation_failed"},
		{name: "weeks with a bad cursor", target: "/api/v1/me/activities/weeks?tz=UTC&cursor=%25%25", status: http.StatusBadRequest, code: "validation_failed"},
		{name: "weeks with a cursor without its week", target: "/api/v1/me/activities/weeks?tz=UTC&cursor=e30", status: http.StatusBadRequest, code: "validation_failed"},
		{name: "weeks with a cursor not on a Monday", target: "/api/v1/me/activities/weeks?tz=UTC&cursor=" + tuesdayCursor, status: http.StatusBadRequest, code: "validation_failed"},
		{name: "activities with a malformed sport", target: "/api/v1/me/activities?sport=RUN", status: http.StatusBadRequest, code: "validation_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{}
			rec, body := serve(t, newService(t, repo, fakeParticipations{}), http.MethodGet, tt.target, "", tt.headers)
			if rec.Code != tt.status || errorCode(body) != tt.code {
				t.Fatalf("got %d %v, want %d %s", rec.Code, body, tt.status, tt.code)
			}
			if repo.bounds != nil || repo.weekQuery != nil || repo.listed != nil {
				t.Fatal("activities read despite the refusal")
			}
		})
	}
}

func TestListMyActivityWeeks(t *testing.T) {
	repo := &fakeRepository{weeks: []WeekTotals{
		{Monday: date(2026, 6, 8), Totals: Totals{Sessions: 4, DurationSeconds: 9360, DistanceMeters: 26_000}},
		{Monday: date(2026, 6, 1), Totals: Totals{Sessions: 1, DurationSeconds: 600}},
	}}
	svc := newService(t, repo, fakeParticipations{})
	rec, body := serve(t, svc, http.MethodGet, "/api/v1/me/activities/weeks?tz=America/Toronto&sport=run&limit=1", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d (%s), want 200", rec.Code, rec.Body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items %v, want one", body["items"])
	}
	if w, _ := items[0].(map[string]any); w["starts_at"] != "2026-06-08T04:00:00Z" || w["sessions"] != 4.0 || w["distance_meters"] != 26_000.0 {
		t.Fatalf("week %v, want June 8 with its totals", w)
	}
	if q := repo.weekQuery; q == nil || q.SportCode == nil || *q.SportCode != "run" {
		t.Fatalf("query %+v, want the weeks of run", q)
	}

	cursor, _ := body["next_cursor"].(string)
	if cursor == "" {
		t.Fatalf("next_cursor %v, want one", body["next_cursor"])
	}
	repo.weeks = repo.weeks[1:]
	rec, body = serve(t, svc, http.MethodGet, "/api/v1/me/activities/weeks?tz=America/Toronto&limit=1&cursor="+cursor, "", nil)
	if rec.Code != http.StatusOK || body["next_cursor"] != nil {
		t.Fatalf("status %d, body %v, want the last page", rec.Code, body)
	}
	if b := repo.weekQuery.Before; b == nil || !b.Equal(toronto(t, 2026, 6, 8, 0)) {
		t.Fatalf("before %v, want the start of the week of the cursor", b)
	}
}
