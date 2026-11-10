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
	err       error
	calls     int
	// from and to are the window the activities were asked for.
	from, to time.Time
}

func (f *fakeRepo) Participants(context.Context, string) ([]scoring.Participant, error) {
	f.calls++
	return f.participants, f.err
}

func (f *fakeRepo) Activities(_ context.Context, _ string, from, to time.Time) ([]scoring.Activity, error) {
	f.calls++
	f.from, f.to = from, to
	return f.activities, f.err
}

func (f *fakeRepo) Snapshots(context.Context, string) (map[string]json.RawMessage, time.Time, error) {
	return f.snapshots, f.frozenAt, nil
}

type fakeProfiles struct {
	pseudos map[string]string
	err     error
}

func (f fakeProfiles) Pseudos(context.Context, []string) (map[string]string, error) {
	return f.pseudos, f.err
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

var pseudos = fakeProfiles{pseudos: map[string]string{"a": "Ann", "b": "Bob"}}

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
	// c is an anonymized account: no pseudo.
	want := Leaderboard{ComputedAt: now, Categories: []CategoryRanking{
		{Type: CategoryCumulativeTime, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 9000}, Pseudo: pseudo("Ann")},
			{Entry: scoring.Entry{UserID: "b", Rank: 2, Value: 3600}, Pseudo: pseudo("Bob")},
			{Entry: scoring.Entry{UserID: "c", Abandoned: true}},
		}},
		{Type: CategoryCumulativeDistance, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 28000}, Pseudo: pseudo("Ann")},
			{Entry: scoring.Entry{UserID: "b", Rank: 2, Value: 5000}, Pseudo: pseudo("Bob")},
			{Entry: scoring.Entry{UserID: "c", Abandoned: true}},
		}},
		{Type: CategoryRunElimination, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 22000, Progress: &scoring.Progress{}}, Pseudo: pseudo("Ann")},
			{Entry: scoring.Entry{UserID: "b", Rank: 2, Value: 5000, Progress: &scoring.Progress{Status: scoring.StatusEliminated, EliminatedWeek: 2}}, Pseudo: pseudo("Bob")},
			{Entry: scoring.Entry{UserID: "c", Abandoned: true}},
		}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Get() = %+v, want %+v", got, want)
	}
	if !repo.from.Equal(c.StartsAt) || !repo.to.Equal(c.EndsAt) {
		t.Fatalf("activities asked for [%s, %s), want the window [%s, %s)", repo.from, repo.to, c.StartsAt, c.EndsAt)
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
	// Long after the end, and the pseudo of c is gone.
	svc := NewService(&fakeChallenges{c: c}, pseudos, repo, clock.NewFake(c.EndsAt.AddDate(0, 1, 0)), 24*time.Hour)

	got, err := svc.Get(t.Context(), "actor", "c1")
	if err != nil {
		t.Fatal(err)
	}
	want := Leaderboard{ComputedAt: frozenAt, Categories: []CategoryRanking{
		{Type: CategoryCumulativeTime, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 9000}, Pseudo: pseudo("Ann")},
			{Entry: scoring.Entry{UserID: "c", Abandoned: true}},
		}},
		{Type: CategoryCumulativeDistance, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 28000}, Pseudo: pseudo("Ann")},
			{Entry: scoring.Entry{UserID: "c", Abandoned: true}},
		}},
		{Type: CategoryRunElimination, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 22000, Progress: &scoring.Progress{}}, Pseudo: pseudo("Ann")},
			{Entry: scoring.Entry{UserID: "b", Rank: 2, Value: 5000, Progress: &scoring.Progress{Status: scoring.StatusEliminated, EliminatedWeek: 2}}, Pseudo: pseudo("Bob")},
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

func TestGetComputesWhatIsNotFrozenYet(t *testing.T) {
	c := twoWeeks(t)
	tests := []struct {
		name      string
		now       time.Time
		snapshots map[string]json.RawMessage
	}{
		{name: "over, the worker has not run", now: c.EndsAt},
		{name: "over, a category is missing", now: c.EndsAt, snapshots: map[string]json.RawMessage{CategoryCumulativeTime: json.RawMessage(`{"entries":[]}`)}},
		{name: "still running", now: c.EndsAt.Add(-time.Second), snapshots: map[string]json.RawMessage{CategoryCumulativeTime: json.RawMessage(`{"entries":[]}`)}},
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
