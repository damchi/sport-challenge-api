package leaderboard

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

type fakeChallenges struct {
	c   Challenge
	err error
	// asked records the actor and the challenge of the last call.
	asked [2]string
}

func (f *fakeChallenges) Challenge(_ context.Context, actorID, id string) (Challenge, error) {
	f.asked = [2]string{actorID, id}
	return f.c, f.err
}

type fakeRepo struct {
	participants []scoring.Participant
	activities   []scoring.Activity
	// snapshots and frozenAt are what Snapshots gives; it is not counted in
	// calls, which count what a ranking is computed from.
	snapshots map[string]json.RawMessage
	frozenAt  time.Time
	// snapshotsErr is what Snapshots fails with.
	snapshotsErr error
	// deleted are the deleted accounts; deletedErr what DeletedAccounts
	// fails with.
	deleted    map[string]bool
	deletedErr error
	err        error
	calls      int
	// from and to are the window the activities were asked for, weekly the
	// sport summed per week.
	from, to time.Time
	weekly   string
}

func (f *fakeRepo) Participants(context.Context, string) ([]scoring.Participant, error) {
	f.calls++
	return f.participants, f.err
}

func (f *fakeRepo) Activities(_ context.Context, _ string, w scoring.Window, weeklySport string) ([]scoring.Activity, error) {
	f.calls++
	f.from, f.to, f.weekly = w.Start, w.End, weeklySport
	return f.activities, f.err
}

func (f *fakeRepo) Snapshots(context.Context, string) (map[string]json.RawMessage, time.Time, error) {
	return f.snapshots, f.frozenAt, f.snapshotsErr
}

func (f *fakeRepo) DeletedAccounts(context.Context, []string) (map[string]bool, error) {
	return f.deleted, f.deletedErr
}

type fakeProfiles struct {
	names map[string]Name
	err   error
}

func (f fakeProfiles) Names(context.Context, []string) (map[string]Name, error) {
	return f.names, f.err
}

func toronto(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func meters(m int) *int { return &m }

func pseudo(s string) *string { return &s }

// twoWeeks is a challenge of 2 weeks from June 1, 2026 (Toronto) with the
// three categories, the way the challenge module freezes them.
func twoWeeks(t *testing.T) Challenge {
	t.Helper()
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, toronto(t))
	return Challenge{
		ID: "c1", Timezone: "America/Toronto", StartsAt: start.UTC(), EndsAt: start.AddDate(0, 0, 14).UTC(),
		Categories: []Category{
			{Type: CategoryCumulativeTime},
			{Type: CategoryCumulativeDistance, Factors: map[string]float64{"run": 1, "ride": 0.3}},
			{Type: CategoryRunElimination, Sport: "run", StepMeters: 2000},
		},
	}
}

// annName and bobName are the public names of participants a and b.
var (
	annName = Name{Pseudo: pseudo("Ann"), FirstName: pseudo("Ann"), LastName: pseudo("Archer")}
	bobName = Name{Pseudo: pseudo("Bob"), FirstName: pseudo("Bob"), LastName: pseudo("Bélanger")}
	pseudos = fakeProfiles{names: map[string]Name{"a": annName, "b": bobName}}
)

func TestGetRanksEveryCategory(t *testing.T) {
	c := twoWeeks(t)
	loc := toronto(t)
	at := func(day, hour int) time.Time { return time.Date(2026, 6, day, hour, 0, 0, 0, loc).UTC() }
	act := func(user, sport string, started time.Time, seconds, distance int) scoring.Activity {
		return scoring.Activity{UserID: user, SportCode: sport, StartedAt: started, DurationSeconds: seconds, DistanceMeters: meters(distance)}
	}
	repo := &fakeRepo{
		participants: []scoring.Participant{{UserID: "a"}, {UserID: "b"}, {UserID: "c", Abandoned: true}},
		activities: []scoring.Activity{
			act("a", "run", at(2, 7), 3600, 10000), act("a", "run", at(9, 7), 3600, 12000), act("a", "ride", at(3, 7), 1800, 20000),
			act("b", "run", at(2, 7), 3600, 5000), // nothing in week 2: eliminated
			act("c", "run", at(2, 7), 3600, 99000),
		},
	}
	now := c.EndsAt
	svc := NewService(&fakeChallenges{c: c}, pseudos, repo, clock.NewFake(now), 24*time.Hour)

	got, err := svc.Get(t.Context(), "actor", "c1")
	if err != nil {
		t.Fatal(err)
	}
	// c abandoned, and the fake gives it no name: the live path lists it.
	want := Leaderboard{ComputedAt: now, Categories: []CategoryRanking{
		{Type: CategoryCumulativeTime, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 9000}, Name: annName},
			{Entry: scoring.Entry{UserID: "b", Rank: 2, Value: 3600}, Name: bobName},
			{Entry: scoring.Entry{UserID: "c", Abandoned: true}},
		}},
		{Type: CategoryCumulativeDistance, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 28000}, Name: annName},
			{Entry: scoring.Entry{UserID: "b", Rank: 2, Value: 5000}, Name: bobName},
			{Entry: scoring.Entry{UserID: "c", Abandoned: true}},
		}},
		{Type: CategoryRunElimination, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 22000, Progress: &scoring.Progress{}}, Name: annName},
			{Entry: scoring.Entry{UserID: "b", Rank: 2, Value: 5000, Progress: &scoring.Progress{Status: scoring.StatusEliminated, EliminatedWeek: 2}}, Name: bobName},
			{Entry: scoring.Entry{UserID: "c", Abandoned: true}},
		}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Get() = %+v, want %+v", got, want)
	}
	if !repo.from.Equal(c.StartsAt) || !repo.to.Equal(c.EndsAt) {
		t.Fatalf("activities asked for [%s, %s), want the window [%s, %s)", repo.from, repo.to, c.StartsAt, c.EndsAt)
	}
	// The totals per week are those of the elimination sport (SC-API-082).
	if repo.weekly != "run" {
		t.Fatalf("weekly sport = %q, want run, the sport of the elimination category", repo.weekly)
	}
}

func TestWeeklySport(t *testing.T) {
	if got := weeklySport(twoWeeks(t).Categories); got != "run" {
		t.Fatalf("with an elimination category: %q, want run", got)
	}
	if got := weeklySport([]Category{{Type: CategoryCumulativeTime}, {Type: CategoryCumulativeDistance}}); got != "" {
		t.Fatalf("without an elimination category: %q, want empty", got)
	}
}

func TestGetJudgesWeeksAtTheClockInstant(t *testing.T) {
	c := twoWeeks(t)
	loc := toronto(t)
	// a runs 5 km then 6.999 km: eliminated, once week 2 ends. b runs 1 km
	// then 3 km: exactly the minimum.
	repo := &fakeRepo{
		participants: []scoring.Participant{{UserID: "a"}, {UserID: "b"}},
		activities: []scoring.Activity{
			{UserID: "a", SportCode: "run", StartedAt: time.Date(2026, 6, 2, 7, 0, 0, 0, loc), DurationSeconds: 60, DistanceMeters: meters(5000)},
			{UserID: "a", SportCode: "run", StartedAt: time.Date(2026, 6, 9, 7, 0, 0, 0, loc), DurationSeconds: 60, DistanceMeters: meters(6999)},
			{UserID: "b", SportCode: "run", StartedAt: time.Date(2026, 6, 2, 7, 0, 0, 0, loc), DurationSeconds: 60, DistanceMeters: meters(1000)},
			{UserID: "b", SportCode: "run", StartedAt: time.Date(2026, 6, 9, 7, 0, 0, 0, loc), DurationSeconds: 60, DistanceMeters: meters(3000)},
		},
	}
	fake := clock.NewFake(c.EndsAt.Add(-time.Second))
	svc := NewService(&fakeChallenges{c: c}, pseudos, repo, fake, 24*time.Hour)

	rank := func() []Ranked {
		t.Helper()
		got, err := svc.Get(t.Context(), "actor", "c1")
		if err != nil {
			t.Fatal(err)
		}
		return got.Categories[2].Entries
	}
	if got := rank(); got[0].UserID != "a" {
		t.Fatalf("one second before the end: %+v, want a (not yet judged) first", got)
	}
	fake.Set(c.EndsAt)
	if got := rank(); got[0].UserID != "b" {
		t.Fatalf("at the end of week 2: %+v, want b first, a eliminated", got)
	}
}

func TestGetUsesTheAtRiskSetting(t *testing.T) {
	c := twoWeeks(t)
	loc := toronto(t)
	repo := &fakeRepo{
		participants: []scoring.Participant{{UserID: "a"}},
		activities: []scoring.Activity{
			{UserID: "a", SportCode: "run", StartedAt: time.Date(2026, 6, 2, 7, 0, 0, 0, loc), DurationSeconds: 60, DistanceMeters: meters(5000)},
			{UserID: "a", SportCode: "run", StartedAt: time.Date(2026, 6, 9, 7, 0, 0, 0, loc), DurationSeconds: 60, DistanceMeters: meters(6000)},
		},
	}
	// Two hours before the end of week 2, 1 km short of the minimum.
	now := c.EndsAt.Add(-2 * time.Hour)
	tests := []struct {
		name   string
		atRisk time.Duration
		want   string
	}{
		{name: "inside the setting", atRisk: 3 * time.Hour, want: scoring.StatusAtRisk},
		{name: "exactly the setting", atRisk: 2 * time.Hour, want: scoring.StatusAtRisk},
		{name: "outside the setting", atRisk: time.Hour, want: scoring.StatusInProgress},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeChallenges{c: c}, pseudos, repo, clock.NewFake(now), tt.atRisk)
			got, err := svc.Get(t.Context(), "actor", "c1")
			if err != nil {
				t.Fatal(err)
			}
			if status := got.Categories[2].Entries[0].Progress.Status; status != tt.want {
				t.Fatalf("status = %q, want %q", status, tt.want)
			}
		})
	}
}

func TestGetFailures(t *testing.T) {
	boom := errors.New("boom")
	oneCategory := Challenge{Timezone: "America/Toronto", Categories: []Category{{Type: CategoryCumulativeTime}}}
	tests := []struct {
		name       string
		challenges *fakeChallenges
		profiles   fakeProfiles
		repo       *fakeRepo
		wantCalls  int
	}{
		{name: "challenge the actor may not read: nothing is loaded", challenges: &fakeChallenges{err: boom}, repo: &fakeRepo{}},
		{name: "storage failure", challenges: &fakeChallenges{c: oneCategory}, repo: &fakeRepo{err: boom}, wantCalls: 1},
		{name: "profiles failure", challenges: &fakeChallenges{c: oneCategory}, profiles: fakeProfiles{err: boom}, repo: &fakeRepo{}, wantCalls: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(tt.challenges, tt.profiles, tt.repo, clock.NewFake(time.Now()), time.Hour)
			if _, err := svc.Get(t.Context(), "actor", "c1"); !errors.Is(err, boom) {
				t.Fatalf("Get() error = %v, want %v", err, boom)
			}
			if tt.repo.calls != tt.wantCalls {
				t.Fatalf("storage called %d times, want %d", tt.repo.calls, tt.wantCalls)
			}
		})
	}
}

func TestGetRejectsWhatItCannotRank(t *testing.T) {
	tests := []struct {
		name string
		c    Challenge
	}{
		{name: "unknown category type", c: Challenge{Timezone: "America/Toronto", Categories: []Category{{Type: "swim_only"}}}},
		{name: "unknown time zone", c: Challenge{Timezone: "Mars/Olympus", Categories: []Category{{Type: CategoryCumulativeTime}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{}
			svc := NewService(&fakeChallenges{c: tt.c}, pseudos, repo, clock.NewFake(time.Now()), time.Hour)
			if _, err := svc.Get(t.Context(), "actor", "c1"); err == nil {
				t.Fatal("Get() succeeded, want an error")
			}
			if repo.calls != 0 {
				t.Fatalf("storage called %d times, want none before the challenge is understood", repo.calls)
			}
		})
	}
}

func TestGetAsksForTheActorAndTheChallenge(t *testing.T) {
	challenges := &fakeChallenges{c: twoWeeks(t)}
	svc := NewService(challenges, pseudos, &fakeRepo{}, clock.NewFake(time.Now()), time.Hour)
	if _, err := svc.Get(t.Context(), "actor", "c1"); err != nil {
		t.Fatal(err)
	}
	if challenges.asked != [2]string{"actor", "c1"} {
		t.Fatalf("challenge asked as %v, want [actor c1]", challenges.asked)
	}
}

func TestGetServesTheFrozenResults(t *testing.T) {
	c := twoWeeks(t)
	frozenAt := c.EndsAt.Add(time.Hour)
	// Activities that would change the ranking if it were computed.
	repo := &fakeRepo{
		participants: []scoring.Participant{{UserID: "a"}},
		activities:   []scoring.Activity{{UserID: "a", SportCode: "run", StartedAt: c.StartsAt, DurationSeconds: 1, DistanceMeters: meters(1)}},
		snapshots: map[string]json.RawMessage{
			CategoryCumulativeTime:     json.RawMessage(`{"entries":[{"user_id":"a","rank":1,"value":9000,"abandoned":false},{"user_id":"c","rank":0,"value":0,"abandoned":true}]}`),
			CategoryCumulativeDistance: json.RawMessage(`{"entries":[{"user_id":"a","rank":1,"value":28000,"abandoned":false},{"user_id":"c","rank":0,"value":0,"abandoned":true}]}`),
			CategoryRunElimination: json.RawMessage(`{"entries":[{"user_id":"a","rank":1,"value":22000,"abandoned":false},` +
				`{"user_id":"b","rank":2,"value":5000,"abandoned":false,"eliminated_week":2},{"user_id":"c","rank":0,"value":0,"abandoned":true}]}`),
		},
		frozenAt: frozenAt,
	}
	// Long after the end, and c has no name: it is not deleted, still listed.
	svc := NewService(&fakeChallenges{c: c}, pseudos, repo, clock.NewFake(c.EndsAt.AddDate(0, 1, 0)), 24*time.Hour)

	got, err := svc.Get(t.Context(), "actor", "c1")
	if err != nil {
		t.Fatal(err)
	}
	want := Leaderboard{ComputedAt: frozenAt, Categories: []CategoryRanking{
		{Type: CategoryCumulativeTime, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 9000}, Name: annName},
			{Entry: scoring.Entry{UserID: "c", Abandoned: true}},
		}},
		{Type: CategoryCumulativeDistance, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 28000}, Name: annName},
			{Entry: scoring.Entry{UserID: "c", Abandoned: true}},
		}},
		{Type: CategoryRunElimination, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 22000, Progress: &scoring.Progress{}}, Name: annName},
			{Entry: scoring.Entry{UserID: "b", Rank: 2, Value: 5000, Progress: &scoring.Progress{Status: scoring.StatusEliminated, EliminatedWeek: 2}}, Name: bobName},
			{Entry: scoring.Entry{UserID: "c", Abandoned: true}},
		}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Get() = %+v, want %+v", got, want)
	}
	if repo.calls != 0 {
		t.Fatalf("activities read %d times, want the snapshots alone", repo.calls)
	}
}

// TestGetFrozenLeavesOutDeletedAccounts: an account deleted after the end
// is left out of the frozen results, and the others move up, equal values
// still sharing a rank.
func TestGetFrozenLeavesOutDeletedAccounts(t *testing.T) {
	c := twoWeeks(t)
	repo := &fakeRepo{snapshots: map[string]json.RawMessage{
		CategoryCumulativeTime: json.RawMessage(`{"entries":[{"user_id":"d","rank":1,"value":10000},` +
			`{"user_id":"a","rank":2,"value":9000},{"user_id":"b","rank":2,"value":9000},{"user_id":"e","rank":0,"value":0,"abandoned":true}]}`),
		CategoryCumulativeDistance: json.RawMessage(`{"entries":[{"user_id":"a","rank":1,"value":28000},` +
			`{"user_id":"d","rank":1,"value":28000},{"user_id":"b","rank":3,"value":5000}]}`),
		CategoryRunElimination: json.RawMessage(`{"entries":[{"user_id":"a","rank":1,"value":22000},` +
			`{"user_id":"d","rank":2,"value":9000,"eliminated_week":2},{"user_id":"b","rank":3,"value":5000,"eliminated_week":1}]}`),
	}, deleted: map[string]bool{"d": true, "e": true}}
	svc := NewService(&fakeChallenges{c: c}, pseudos, repo, clock.NewFake(c.EndsAt), time.Hour)

	got, err := svc.Get(t.Context(), "actor", "c1")
	if err != nil {
		t.Fatal(err)
	}
	want := []CategoryRanking{
		{Type: CategoryCumulativeTime, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 9000}, Name: annName},
			{Entry: scoring.Entry{UserID: "b", Rank: 1, Value: 9000}, Name: bobName},
		}},
		{Type: CategoryCumulativeDistance, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 28000}, Name: annName},
			{Entry: scoring.Entry{UserID: "b", Rank: 2, Value: 5000}, Name: bobName},
		}},
		{Type: CategoryRunElimination, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 22000, Progress: &scoring.Progress{}}, Name: annName},
			{Entry: scoring.Entry{UserID: "b", Rank: 2, Value: 5000, Progress: &scoring.Progress{Status: scoring.StatusEliminated, EliminatedWeek: 1}}, Name: bobName},
		}},
	}
	if !reflect.DeepEqual(got.Categories, want) {
		t.Fatalf("Get() = %+v, want %+v", got.Categories, want)
	}
}

func TestGetComputesWhatIsNotFrozenYet(t *testing.T) {
	c := twoWeeks(t)
	tests := []struct {
		name      string
		now       time.Time
		snapshots map[string]json.RawMessage
	}{
		{name: "over, the worker has not run", now: c.EndsAt},
		{name: "over, a category is missing", now: c.EndsAt, snapshots: map[string]json.RawMessage{CategoryCumulativeTime: json.RawMessage(`{"entries":[]}`)}},
		{name: "still running, snapshots are not read", now: c.EndsAt.Add(-time.Second), snapshots: map[string]json.RawMessage{
			CategoryCumulativeTime:     json.RawMessage(`{"entries":[]}`),
			CategoryCumulativeDistance: json.RawMessage(`{"entries":[]}`),
			CategoryRunElimination:     json.RawMessage(`{"entries":[]}`),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{participants: []scoring.Participant{{UserID: "a"}}, snapshots: tt.snapshots}
			svc := NewService(&fakeChallenges{c: c}, pseudos, repo, clock.NewFake(tt.now), 24*time.Hour)
			if _, err := svc.Get(t.Context(), "actor", "c1"); err != nil {
				t.Fatal(err)
			}
			if repo.calls != 2 {
				t.Fatalf("storage called %d times, want the ranking computed (2)", repo.calls)
			}
		})
	}
}

func TestGetFrozenFailures(t *testing.T) {
	boom := errors.New("boom")
	c := twoWeeks(t)
	bad := json.RawMessage(`not json`)
	tests := []struct {
		name string
		repo *fakeRepo
	}{
		{name: "storage failure", repo: &fakeRepo{snapshotsErr: boom}},
		{name: "deleted accounts not read", repo: &fakeRepo{deletedErr: boom, snapshots: map[string]json.RawMessage{
			CategoryCumulativeTime:     json.RawMessage(`{"entries":[]}`),
			CategoryCumulativeDistance: json.RawMessage(`{"entries":[]}`),
			CategoryRunElimination:     json.RawMessage(`{"entries":[]}`),
		}}},
		{name: "invalid payload: an error, not a computed ranking", repo: &fakeRepo{snapshots: map[string]json.RawMessage{
			CategoryCumulativeTime: bad, CategoryCumulativeDistance: bad, CategoryRunElimination: bad,
		}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeChallenges{c: c}, pseudos, tt.repo, clock.NewFake(c.EndsAt), time.Hour)
			if _, err := svc.Get(t.Context(), "actor", "c1"); err == nil {
				t.Fatal("Get() succeeded, want an error")
			}
			if tt.repo.calls != 0 {
				t.Fatalf("activities read %d times, want no fallback on a failure", tt.repo.calls)
			}
		})
	}
}

// TestGetFrozenNamesEveryCategory: the names are asked for the participants
// of all the categories, each once.
func TestGetFrozenNamesEveryCategory(t *testing.T) {
	c := twoWeeks(t)
	repo := &fakeRepo{snapshots: map[string]json.RawMessage{
		CategoryCumulativeTime:     json.RawMessage(`{"entries":[{"user_id":"a","rank":1,"value":1}]}`),
		CategoryCumulativeDistance: json.RawMessage(`{"entries":[{"user_id":"a","rank":1,"value":1},{"user_id":"b","rank":2,"value":0}]}`),
		CategoryRunElimination:     json.RawMessage(`{"entries":[{"user_id":"b","rank":1,"value":1}]}`),
	}}
	profiles := &recordingProfiles{fakeProfiles: pseudos}
	svc := NewService(&fakeChallenges{c: c}, profiles, repo, clock.NewFake(c.EndsAt), time.Hour)
	got, err := svc.Get(t.Context(), "actor", "c1")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b"}; !reflect.DeepEqual(profiles.asked, want) {
		t.Fatalf("names asked for %v, want %v", profiles.asked, want)
	}
	if n := got.Categories[2].Entries[0].Name; !reflect.DeepEqual(n, bobName) {
		t.Fatalf("name in the third category = %+v, want %+v", n, bobName)
	}
}

// recordingProfiles remembers the ids it was asked for.
type recordingProfiles struct {
	fakeProfiles
	asked []string
}

func (r *recordingProfiles) Names(ctx context.Context, ids []string) (map[string]Name, error) {
	r.asked = ids
	return r.fakeProfiles.Names(ctx, ids)
}
