package leaderboard

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

type fakeChallenges struct {
	c   Challenge
	err error
	// actor and asked record the actor and the challenge of the last call.
	actor auth.Actor
	asked string
}

func (f *fakeChallenges) Challenge(_ context.Context, actor auth.Actor, id string) (Challenge, error) {
	f.actor, f.asked = actor, id
	return f.c, f.err
}

type fakeRepo struct {
	participants []scoring.Participant
	activities   []scoring.Activity
	// snapshots and frozenAt are what Snapshots gives; it is not counted in
	// calls, which count what a ranking is computed from.
	snapshots map[string]json.RawMessage
	frozenAt  time.Time
	// snapshotsErr is what Snapshots and SnapshotsOf fail with.
	snapshotsErr error
	// snapshotsOf is what SnapshotsOf gives, by challenge ID; snapshotsOfAsked
	// the IDs it was last asked for, snapshotsOfCalls how many times.
	snapshotsOf      map[string]map[string]json.RawMessage
	snapshotsOfAsked []string
	snapshotsOfCalls int
	// deletedCalls counts the calls of DeletedAccounts.
	deletedCalls int
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
	// participantsOf and activitiesOf replace participants and activities
	// for the challenges they hold; scopes are the last ones asked for.
	participantsOf map[string][]scoring.Participant
	activitiesOf   map[string][]scoring.Activity
	scopes         []Scope
}

func (f *fakeRepo) ParticipantsOf(_ context.Context, ids []string) (map[string][]scoring.Participant, error) {
	f.calls++
	out := make(map[string][]scoring.Participant, len(ids))
	for _, id := range ids {
		out[id] = f.participants
		if p, ok := f.participantsOf[id]; ok {
			out[id] = p
		}
	}
	return out, f.err
}

func (f *fakeRepo) ActivitiesOf(_ context.Context, scopes []Scope) (map[string][]scoring.Activity, error) {
	f.calls++
	f.scopes = scopes
	out := make(map[string][]scoring.Activity, len(scopes))
	for _, sc := range scopes {
		f.from, f.to, f.weekly = sc.Window.Start, sc.Window.End, sc.WeeklySport
		out[sc.ChallengeID] = f.activities
		if a, ok := f.activitiesOf[sc.ChallengeID]; ok {
			out[sc.ChallengeID] = a
		}
	}
	return out, f.err
}

func (f *fakeRepo) Snapshots(context.Context, string) (map[string]json.RawMessage, time.Time, error) {
	return f.snapshots, f.frozenAt, f.snapshotsErr
}

func (f *fakeRepo) SnapshotsOf(_ context.Context, ids []string) (map[string]map[string]json.RawMessage, error) {
	f.snapshotsOfCalls++
	f.snapshotsOfAsked = ids
	out := make(map[string]map[string]json.RawMessage)
	for _, id := range ids {
		if p, ok := f.snapshotsOf[id]; ok {
			out[id] = p
		}
	}
	return out, f.snapshotsErr
}

func (f *fakeRepo) DeletedAccounts(context.Context, []string) (map[string]bool, error) {
	f.deletedCalls++
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
			{Type: CategoryElimination, Sport: "run", StepMeters: 2000},
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
	// The last week locks at 7 am on the day after the end.
	now := at(15, 7)
	svc := NewService(&fakeChallenges{c: c}, pseudos, repo, clock.NewFake(now), 24*time.Hour)

	got, err := svc.Get(t.Context(), auth.Actor{ID: "actor"}, "c1")
	if err != nil {
		t.Fatal(err)
	}
	// c abandoned, and the fake gives it no name: the live path lists it.
	want := Leaderboard{ComputedAt: now, Categories: []CategoryRanking{
		{Type: CategoryCumulativeTime, RankedCount: 2, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 9000}, Name: annName},
			{Entry: scoring.Entry{UserID: "b", Rank: 2, Value: 3600}, Name: bobName},
			{Entry: scoring.Entry{UserID: "c", Abandoned: true}},
		}},
		{Type: CategoryCumulativeDistance, RankedCount: 2, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 28000}, Name: annName},
			{Entry: scoring.Entry{UserID: "b", Rank: 2, Value: 5000}, Name: bobName},
			{Entry: scoring.Entry{UserID: "c", Abandoned: true}},
		}},
		{Type: CategoryElimination, RankedCount: 2, Entries: []Ranked{
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

// TestTimeOnlyChallenge: a challenge with the cumulative time alone is ranked,
// frozen and placed in that category only, and nothing is summed per week.
func TestTimeOnlyChallenge(t *testing.T) {
	c := threeWeeks(t)
	c.Categories = c.Categories[:1]
	loc := toronto(t)
	at := func(day, hour int) time.Time { return time.Date(2026, 6, day, hour, 0, 0, 0, loc).UTC() }
	activities := []scoring.Activity{
		{UserID: "a", SportCode: "ride", StartedAt: at(2, 7), DurationSeconds: 3600, DistanceMeters: meters(30000)},
		{UserID: "b", SportCode: "yoga", StartedAt: at(3, 7), DurationSeconds: 1800},
	}
	participants := []scoring.Participant{{UserID: "a"}, {UserID: "b"}}

	t.Run("live", func(t *testing.T) {
		repo := &fakeRepo{participants: participants, activities: activities}
		now := at(4, 12)
		svc := NewService(&fakeChallenges{c: c}, pseudos, repo, clock.NewFake(now), 24*time.Hour)
		got, err := svc.Get(t.Context(), auth.Actor{ID: "actor"}, "c1")
		if err != nil {
			t.Fatal(err)
		}
		want := Leaderboard{ComputedAt: now, Categories: []CategoryRanking{{Type: CategoryCumulativeTime, RankedCount: 2, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 3600}, Name: annName},
			{Entry: scoring.Entry{UserID: "b", Rank: 2, Value: 1800}, Name: bobName},
		}}}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Get() = %+v, want %+v", got, want)
		}
		if repo.weekly != "" {
			t.Fatalf("weekly sport = %q, want none without an elimination category", repo.weekly)
		}
	})

	t.Run("frozen", func(t *testing.T) {
		frozenAt := c.EndsAt.Add(time.Hour)
		repo := &fakeRepo{
			snapshots: map[string]json.RawMessage{
				CategoryCumulativeTime: json.RawMessage(`{"entries":[{"user_id":"a","rank":1,"value":3600,"abandoned":false}]}`),
			},
			frozenAt: frozenAt,
		}
		svc := NewService(&fakeChallenges{c: c}, pseudos, repo, clock.NewFake(c.EndsAt.AddDate(0, 1, 0)), 24*time.Hour)
		got, err := svc.Get(t.Context(), auth.Actor{ID: "actor"}, "c1")
		if err != nil {
			t.Fatal(err)
		}
		want := Leaderboard{ComputedAt: frozenAt, Categories: []CategoryRanking{{Type: CategoryCumulativeTime, RankedCount: 1, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 3600}, Name: annName},
		}}}}
		if !reflect.DeepEqual(got, want) || repo.calls != 0 {
			t.Fatalf("Get() = %+v after %d reads, want %+v from the snapshot alone", got, repo.calls, want)
		}
	})

	t.Run("standings", func(t *testing.T) {
		repo := &fakeRepo{participants: participants, activities: activities}
		svc := NewService(&fakeChallenges{}, pseudos, repo, clock.NewFake(time.Time{}), 24*time.Hour)
		got, err := svc.Standings(t.Context(), "b", []Challenge{c}, at(4, 12))
		if err != nil {
			t.Fatal(err)
		}
		want := Standing{Week: 1, WeekEndsAt: at(8, 0), Categories: []Place{
			{Type: CategoryCumulativeTime, Entry: scoring.Entry{UserID: "b", Rank: 2, Value: 1800}, RankedCount: 2},
		}}
		if st := got["c1"]; !reflect.DeepEqual(st, want) {
			t.Fatalf("Standings() = %+v, want %+v", st, want)
		}
		if len(repo.scopes) != 1 || repo.scopes[0].WeeklySport != "" {
			t.Fatalf("scopes %+v, want one with no weekly sport", repo.scopes)
		}
	})
}

// TestPointsChallenge: the points category is ranked on the points of the
// locked weeks, those of the running week apart; its groups are asked for
// with its minimum session; frozen, it has no provisional points; and the
// actor's place carries them.
func TestPointsChallenge(t *testing.T) {
	c := twoWeeks(t)
	c.Categories = []Category{
		{Type: CategoryCumulativeTime},
		{Type: CategoryPoints, Criteria: []string{scoring.CriterionLongestDuration}, Podium: 5, MinSessionSeconds: 1200},
	}
	loc := toronto(t)
	at := func(day, hour int) time.Time { return time.Date(2026, 6, day, hour, 0, 0, 0, loc).UTC() }
	group := func(user string, week time.Time, seconds, longest int) scoring.Activity {
		return scoring.Activity{UserID: user, SportCode: "run", StartedAt: week, DurationSeconds: seconds, LongestSeconds: longest, Sessions: 1}
	}
	// a has the longest session of week 1, b that of week 2 (running).
	activities := []scoring.Activity{
		group("a", at(1, 0), 3600, 3600), group("b", at(1, 0), 1800, 1800),
		group("a", at(8, 0), 1000, 1000), group("b", at(8, 0), 3000, 3000),
	}
	participants := []scoring.Participant{{UserID: "a"}, {UserID: "b"}}
	now := at(10, 12)
	ten, eight := 10, 8

	t.Run("live", func(t *testing.T) {
		repo := &fakeRepo{participants: participants, activities: activities}
		svc := NewService(&fakeChallenges{c: c}, pseudos, repo, clock.NewFake(now), 24*time.Hour)
		got, err := svc.Get(t.Context(), auth.Actor{ID: "actor"}, "c1")
		if err != nil {
			t.Fatal(err)
		}
		want := CategoryRanking{Type: CategoryPoints, RankedCount: 2, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 10, Provisional: &eight}, Name: annName},
			{Entry: scoring.Entry{UserID: "b", Rank: 2, Value: 8, Provisional: &ten}, Name: bobName},
		}}
		if len(got.Categories) != 2 || !reflect.DeepEqual(got.Categories[1], want) {
			t.Fatalf("Get() = %+v, want the points %+v", got.Categories, want)
		}
		if len(repo.scopes) != 1 || repo.scopes[0].Points == nil || repo.scopes[0].Points.MinSessionSeconds != 1200 || repo.scopes[0].WeeklySport != "" {
			t.Fatalf("scopes %+v, want the points groups at 1200 s and no weekly sport", repo.scopes)
		}
	})

	t.Run("frozen", func(t *testing.T) {
		repo := &fakeRepo{snapshots: map[string]json.RawMessage{
			CategoryCumulativeTime: json.RawMessage(`{"entries":[{"user_id":"a","rank":1,"value":4600,"abandoned":false}]}`),
			CategoryPoints:         json.RawMessage(`{"entries":[{"user_id":"b","rank":1,"value":18,"abandoned":false},{"user_id":"a","rank":2,"value":10,"abandoned":false}]}`),
		}}
		svc := NewService(&fakeChallenges{c: c}, pseudos, repo, clock.NewFake(c.EndsAt.AddDate(0, 1, 0)), 24*time.Hour)
		got, err := svc.Get(t.Context(), auth.Actor{ID: "actor"}, "c1")
		if err != nil {
			t.Fatal(err)
		}
		want := CategoryRanking{Type: CategoryPoints, RankedCount: 2, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "b", Rank: 1, Value: 18}, Name: bobName},
			{Entry: scoring.Entry{UserID: "a", Rank: 2, Value: 10}, Name: annName},
		}}
		if !reflect.DeepEqual(got.Categories[1], want) || repo.calls != 0 {
			t.Fatalf("Get() = %+v after %d reads, want %+v from the snapshot alone", got.Categories, repo.calls, want)
		}
	})

	t.Run("standings", func(t *testing.T) {
		repo := &fakeRepo{participants: participants, activities: activities}
		svc := NewService(&fakeChallenges{}, pseudos, repo, clock.NewFake(time.Time{}), 24*time.Hour)
		got, err := svc.Standings(t.Context(), "b", []Challenge{c}, now)
		if err != nil {
			t.Fatal(err)
		}
		place := got["c1"].Categories[1]
		if want := (Place{Type: CategoryPoints, Entry: scoring.Entry{UserID: "b", Rank: 2, Value: 8, Provisional: &ten}, RankedCount: 2}); !reflect.DeepEqual(place, want) {
			t.Fatalf("points place %+v, want %+v", place, want)
		}
		if len(repo.scopes) != 1 || repo.scopes[0].Points == nil {
			t.Fatalf("scopes %+v, want the points groups", repo.scopes)
		}
	})
}

func TestScopeOf(t *testing.T) {
	c := twoWeeks(t)
	if got := scopeOf("c1", c.Categories, scoring.Window{}); got.ChallengeID != "c1" || got.WeeklySport != "run" || got.Points != nil {
		t.Fatalf("with an elimination category: %+v, want run per week and no points", got)
	}
	c.Categories = []Category{{Type: CategoryCumulativeTime}, {Type: CategoryCumulativeDistance}}
	if got := scopeOf("c1", c.Categories, scoring.Window{}); got.WeeklySport != "" || got.Points != nil {
		t.Fatalf("without an elimination nor a points category: %+v, want neither", got)
	}
	c.Categories = append(c.Categories, Category{Type: CategoryPoints, Criteria: []string{scoring.CriterionSessionCount}, Podium: 5, MinSessionSeconds: 1200})
	if got := scopeOf("c1", c.Categories, scoring.Window{}); got.WeeklySport != "" || got.Points == nil || got.Points.MinSessionSeconds != 1200 {
		t.Fatalf("with a points category: %+v, want its groups at 1200 s", got)
	}
}

func TestGetJudgesWeeksAtTheClockInstant(t *testing.T) {
	c := twoWeeks(t)
	loc := toronto(t)
	// a runs 5 km then 6.999 km: eliminated, once week 2 locks. b runs 1 km
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
	// The last week locks at 7 am on the day after the end.
	lock := time.Date(2026, 6, 15, 7, 0, 0, 0, loc).UTC()
	fake := clock.NewFake(c.EndsAt)
	svc := NewService(&fakeChallenges{c: c}, pseudos, repo, fake, 24*time.Hour)

	rank := func() []Ranked {
		t.Helper()
		got, err := svc.Get(t.Context(), auth.Actor{ID: "actor"}, "c1")
		if err != nil {
			t.Fatal(err)
		}
		return got.Categories[2].Entries
	}
	if got := rank(); got[0].UserID != "a" {
		t.Fatalf("at the end: %+v, want a (not yet judged) first", got)
	}
	fake.Set(lock.Add(-time.Second))
	if got := rank(); got[0].UserID != "a" {
		t.Fatalf("one second before 7 am: %+v, want a (not yet judged) first", got)
	}
	fake.Set(lock)
	if got := rank(); got[0].UserID != "b" {
		t.Fatalf("at 7 am the day after week 2: %+v, want b first, a eliminated", got)
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
			got, err := svc.Get(t.Context(), auth.Actor{ID: "actor"}, "c1")
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
			if _, err := svc.Get(t.Context(), auth.Actor{ID: "actor"}, "c1"); !errors.Is(err, boom) {
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
			if _, err := svc.Get(t.Context(), auth.Actor{ID: "actor"}, "c1"); err == nil {
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
	if _, err := svc.Get(t.Context(), auth.Actor{ID: "actor"}, "c1"); err != nil {
		t.Fatal(err)
	}
	if challenges.actor != (auth.Actor{ID: "actor"}) || challenges.asked != "c1" {
		t.Fatalf("challenge asked as %+v %s, want actor c1", challenges.actor, challenges.asked)
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
			CategoryElimination: json.RawMessage(`{"entries":[{"user_id":"a","rank":1,"value":22000,"abandoned":false},` +
				`{"user_id":"b","rank":2,"value":5000,"abandoned":false,"eliminated_week":2},{"user_id":"c","rank":0,"value":0,"abandoned":true}]}`),
		},
		frozenAt: frozenAt,
	}
	// Long after the end, and c has no name: it is not deleted, still listed.
	svc := NewService(&fakeChallenges{c: c}, pseudos, repo, clock.NewFake(c.EndsAt.AddDate(0, 1, 0)), 24*time.Hour)

	got, err := svc.Get(t.Context(), auth.Actor{ID: "actor"}, "c1")
	if err != nil {
		t.Fatal(err)
	}
	want := Leaderboard{ComputedAt: frozenAt, Categories: []CategoryRanking{
		{Type: CategoryCumulativeTime, RankedCount: 1, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 9000}, Name: annName},
			{Entry: scoring.Entry{UserID: "c", Abandoned: true}},
		}},
		{Type: CategoryCumulativeDistance, RankedCount: 1, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 28000}, Name: annName},
			{Entry: scoring.Entry{UserID: "c", Abandoned: true}},
		}},
		{Type: CategoryElimination, RankedCount: 2, Entries: []Ranked{
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
		CategoryElimination: json.RawMessage(`{"entries":[{"user_id":"a","rank":1,"value":22000},` +
			`{"user_id":"d","rank":2,"value":9000,"eliminated_week":2},{"user_id":"b","rank":3,"value":5000,"eliminated_week":1}]}`),
	}, deleted: map[string]bool{"d": true, "e": true}}
	profiles := &recordingProfiles{fakeProfiles: pseudos}
	svc := NewService(&fakeChallenges{c: c}, profiles, repo, clock.NewFake(c.EndsAt), time.Hour)

	got, err := svc.Get(t.Context(), auth.Actor{ID: "actor"}, "c1")
	if err != nil {
		t.Fatal(err)
	}
	want := []CategoryRanking{
		{Type: CategoryCumulativeTime, RankedCount: 2, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 9000}, Name: annName},
			{Entry: scoring.Entry{UserID: "b", Rank: 1, Value: 9000}, Name: bobName},
		}},
		{Type: CategoryCumulativeDistance, RankedCount: 2, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 28000}, Name: annName},
			{Entry: scoring.Entry{UserID: "b", Rank: 2, Value: 5000}, Name: bobName},
		}},
		{Type: CategoryElimination, RankedCount: 2, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 22000, Progress: &scoring.Progress{}}, Name: annName},
			{Entry: scoring.Entry{UserID: "b", Rank: 2, Value: 5000, Progress: &scoring.Progress{Status: scoring.StatusEliminated, EliminatedWeek: 1}}, Name: bobName},
		}},
	}
	if !reflect.DeepEqual(got.Categories, want) {
		t.Fatalf("Get() = %+v, want %+v", got.Categories, want)
	}
	if want := []string{"a", "b"}; !reflect.DeepEqual(profiles.asked, want) {
		t.Fatalf("names asked for %v, want %v: never a deleted account", profiles.asked, want)
	}
}

// TestGetFrozenAfterTheErasure: once the deletion erased the entries of an
// account from the frozen results (README §2.2), the others move up as when
// they were only left out: the ranks frozen with it are given again.
func TestGetFrozenAfterTheErasure(t *testing.T) {
	c := twoWeeks(t)
	// The snapshots of TestGetFrozenLeavesOutDeletedAccounts, d erased.
	repo := &fakeRepo{snapshots: map[string]json.RawMessage{
		CategoryCumulativeTime:     json.RawMessage(`{"entries":[{"user_id":"a","rank":2,"value":9000},{"user_id":"b","rank":2,"value":9000}]}`),
		CategoryCumulativeDistance: json.RawMessage(`{"entries":[{"user_id":"a","rank":1,"value":28000},{"user_id":"b","rank":3,"value":5000}]}`),
		CategoryElimination:        json.RawMessage(`{"entries":[{"user_id":"a","rank":1,"value":22000},{"user_id":"b","rank":3,"value":5000,"eliminated_week":1}]}`),
	}}
	svc := NewService(&fakeChallenges{c: c}, pseudos, repo, clock.NewFake(c.EndsAt), time.Hour)

	got, err := svc.Get(t.Context(), auth.Actor{ID: "actor"}, "c1")
	if err != nil {
		t.Fatal(err)
	}
	want := []CategoryRanking{
		{Type: CategoryCumulativeTime, RankedCount: 2, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 9000}, Name: annName},
			{Entry: scoring.Entry{UserID: "b", Rank: 1, Value: 9000}, Name: bobName},
		}},
		{Type: CategoryCumulativeDistance, RankedCount: 2, Entries: []Ranked{
			{Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 28000}, Name: annName},
			{Entry: scoring.Entry{UserID: "b", Rank: 2, Value: 5000}, Name: bobName},
		}},
		{Type: CategoryElimination, RankedCount: 2, Entries: []Ranked{
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
			CategoryElimination:        json.RawMessage(`{"entries":[]}`),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{participants: []scoring.Participant{{UserID: "a"}}, snapshots: tt.snapshots}
			svc := NewService(&fakeChallenges{c: c}, pseudos, repo, clock.NewFake(tt.now), 24*time.Hour)
			if _, err := svc.Get(t.Context(), auth.Actor{ID: "actor"}, "c1"); err != nil {
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
			CategoryElimination:        json.RawMessage(`{"entries":[]}`),
		}}},
		{name: "invalid payload: an error, not a computed ranking", repo: &fakeRepo{snapshots: map[string]json.RawMessage{
			CategoryCumulativeTime: bad, CategoryCumulativeDistance: bad, CategoryElimination: bad,
		}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeChallenges{c: c}, pseudos, tt.repo, clock.NewFake(c.EndsAt), time.Hour)
			if _, err := svc.Get(t.Context(), auth.Actor{ID: "actor"}, "c1"); err == nil {
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
		CategoryElimination:        json.RawMessage(`{"entries":[{"user_id":"b","rank":1,"value":1}]}`),
	}}
	profiles := &recordingProfiles{fakeProfiles: pseudos}
	svc := NewService(&fakeChallenges{c: c}, profiles, repo, clock.NewFake(c.EndsAt), time.Hour)
	got, err := svc.Get(t.Context(), auth.Actor{ID: "actor"}, "c1")
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

// threeWeeks is twoWeeks lasting 3 weeks: a week runs after an elimination.
func threeWeeks(t *testing.T) Challenge {
	t.Helper()
	c := twoWeeks(t)
	c.EndsAt = c.StartsAt.AddDate(0, 0, 21)
	return c
}

func TestStandings(t *testing.T) {
	c := threeWeeks(t)
	loc := toronto(t)
	at := func(day, hour int) time.Time { return time.Date(2026, 6, day, hour, 0, 0, 0, loc).UTC() }
	run := func(user string, day, distance int) scoring.Activity {
		return scoring.Activity{UserID: user, SportCode: "run", StartedAt: at(day, 7), DurationSeconds: 3600, DistanceMeters: meters(distance)}
	}
	endOf := func(week int) time.Time { return at(1+7*week, 0) }
	everyone := []scoring.Participant{{UserID: "a"}, {UserID: "b"}, {UserID: "c", Abandoned: true}}
	// a and b run 5 km in week 1: equal everywhere.
	week1 := []scoring.Activity{run("a", 2, 5000), run("b", 2, 5000), run("c", 2, 9000)}
	minimum := func(m int) *int { return &m }
	tests := []struct {
		name         string
		now          time.Time
		participants []scoring.Participant
		activities   []scoring.Activity
		// want is nil when the challenge is left out.
		want *Standing
	}{
		{
			name: "week 1: equal ones share the rank, no minimum nor status",
			now:  at(3, 12), participants: everyone, activities: week1,
			want: &Standing{Week: 1, WeekEndsAt: endOf(1), Categories: []Place{
				{Type: CategoryCumulativeTime, Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 3600}, RankedCount: 2},
				{Type: CategoryCumulativeDistance, Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 5000}, RankedCount: 2},
				{Type: CategoryElimination, Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 5000, Progress: &scoring.Progress{DistanceMeters: 5000, WeekEndsAt: endOf(1)}}, RankedCount: 2},
			}},
		},
		{
			name: "week 2, under the minimum: in progress",
			now:  at(10, 12), participants: everyone, activities: append(week1, run("a", 9, 6000)),
			want: &Standing{Week: 2, WeekEndsAt: endOf(2), Categories: []Place{
				{Type: CategoryCumulativeTime, Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 7200}, RankedCount: 2},
				{Type: CategoryCumulativeDistance, Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 11000}, RankedCount: 2},
				{Type: CategoryElimination, Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 11000, Progress: &scoring.Progress{
					Status: scoring.StatusInProgress, MinimumMeters: minimum(7000), DistanceMeters: 6000, WeekEndsAt: endOf(2),
				}}, RankedCount: 2},
			}},
		},
		{
			name: "week 2, under the minimum 24 h before the end: at risk",
			now:  endOf(2).Add(-24 * time.Hour), participants: everyone, activities: append(week1, run("a", 9, 6000)),
			want: &Standing{Week: 2, WeekEndsAt: endOf(2), Categories: []Place{
				{Type: CategoryCumulativeTime, Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 7200}, RankedCount: 2},
				{Type: CategoryCumulativeDistance, Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 11000}, RankedCount: 2},
				{Type: CategoryElimination, Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 11000, Progress: &scoring.Progress{
					Status: scoring.StatusAtRisk, MinimumMeters: minimum(7000), DistanceMeters: 6000, WeekEndsAt: endOf(2),
				}}, RankedCount: 2},
			}},
		},
		{
			name: "week 2, the minimum run: reached",
			now:  endOf(2).Add(-time.Hour), participants: everyone, activities: append(week1, run("a", 9, 7000)),
			want: &Standing{Week: 2, WeekEndsAt: endOf(2), Categories: []Place{
				{Type: CategoryCumulativeTime, Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 7200}, RankedCount: 2},
				{Type: CategoryCumulativeDistance, Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 12000}, RankedCount: 2},
				{Type: CategoryElimination, Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 12000, Progress: &scoring.Progress{
					Status: scoring.StatusReached, MinimumMeters: minimum(7000), DistanceMeters: 7000, WeekEndsAt: endOf(2),
				}}, RankedCount: 2},
			}},
		},
		{
			name: "week 3, eliminated in week 2: ranked after the survivor",
			now:  at(16, 12), participants: everyone, activities: append(week1, run("a", 9, 6000), run("b", 9, 7000)),
			want: &Standing{Week: 3, WeekEndsAt: endOf(3), Categories: []Place{
				{Type: CategoryCumulativeTime, Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 7200}, RankedCount: 2},
				{Type: CategoryCumulativeDistance, Entry: scoring.Entry{UserID: "a", Rank: 2, Value: 11000}, RankedCount: 2},
				{Type: CategoryElimination, Entry: scoring.Entry{UserID: "a", Rank: 2, Value: 11000, Progress: &scoring.Progress{
					Status: scoring.StatusEliminated, EliminatedWeek: 2,
				}}, RankedCount: 2},
			}},
		},
		{
			name: "two ahead with the same value: third, after the gap",
			now:  at(3, 12), participants: []scoring.Participant{{UserID: "a"}, {UserID: "b"}, {UserID: "d"}},
			activities: []scoring.Activity{run("a", 2, 5000), run("b", 2, 6000), run("d", 2, 6000)},
			want: &Standing{Week: 1, WeekEndsAt: endOf(1), Categories: []Place{
				{Type: CategoryCumulativeTime, Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 3600}, RankedCount: 3},
				{Type: CategoryCumulativeDistance, Entry: scoring.Entry{UserID: "a", Rank: 3, Value: 5000}, RankedCount: 3},
				{Type: CategoryElimination, Entry: scoring.Entry{UserID: "a", Rank: 3, Value: 5000, Progress: &scoring.Progress{DistanceMeters: 5000, WeekEndsAt: endOf(1)}}, RankedCount: 3},
			}},
		},
		{
			name: "at the exact end of week 1: week 2, with its minimum",
			now:  endOf(1), participants: everyone, activities: week1,
			want: &Standing{Week: 2, WeekEndsAt: endOf(2), Categories: []Place{
				{Type: CategoryCumulativeTime, Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 3600}, RankedCount: 2},
				{Type: CategoryCumulativeDistance, Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 5000}, RankedCount: 2},
				{Type: CategoryElimination, Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 5000, Progress: &scoring.Progress{
					Status: scoring.StatusInProgress, MinimumMeters: minimum(7000), DistanceMeters: 0, WeekEndsAt: endOf(2),
				}}, RankedCount: 2},
			}},
		},
		{
			name: "at the exact end of week 2: week 3, not eliminated before 7 am, a minimum that may still rise",
			now:  endOf(2), participants: everyone, activities: append(week1, run("a", 9, 6000), run("b", 9, 7000)),
			want: &Standing{Week: 3, WeekEndsAt: endOf(3), Categories: []Place{
				{Type: CategoryCumulativeTime, Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 7200}, RankedCount: 2},
				{Type: CategoryCumulativeDistance, Entry: scoring.Entry{UserID: "a", Rank: 2, Value: 11000}, RankedCount: 2},
				{Type: CategoryElimination, Entry: scoring.Entry{UserID: "a", Rank: 2, Value: 11000, Progress: &scoring.Progress{
					Status: scoring.StatusInProgress, MinimumMeters: minimum(8000), DistanceMeters: 0, WeekEndsAt: endOf(3),
				}}, RankedCount: 2},
			}},
		},
		{
			name: "at 7 am the day after week 2: eliminated",
			now:  at(15, 7), participants: everyone, activities: append(week1, run("a", 9, 6000), run("b", 9, 7000)),
			want: &Standing{Week: 3, WeekEndsAt: endOf(3), Categories: []Place{
				{Type: CategoryCumulativeTime, Entry: scoring.Entry{UserID: "a", Rank: 1, Value: 7200}, RankedCount: 2},
				{Type: CategoryCumulativeDistance, Entry: scoring.Entry{UserID: "a", Rank: 2, Value: 11000}, RankedCount: 2},
				{Type: CategoryElimination, Entry: scoring.Entry{UserID: "a", Rank: 2, Value: 11000, Progress: &scoring.Progress{
					Status: scoring.StatusEliminated, EliminatedWeek: 2,
				}}, RankedCount: 2},
			}},
		},
		{
			name: "abandoned: left out", now: at(3, 12), activities: week1,
			participants: []scoring.Participant{{UserID: "a", Abandoned: true}, {UserID: "b"}},
		},
		{
			name: "not a participant: left out", now: at(3, 12), activities: week1,
			participants: []scoring.Participant{{UserID: "b"}},
		},
		{name: "before the start: left out", now: c.StartsAt.Add(-time.Second), participants: everyone, activities: week1},
		{name: "at the end: left out", now: c.EndsAt, participants: everyone, activities: week1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{participants: tt.participants, activities: tt.activities}
			// The instant is the caller's, never the service's clock.
			svc := NewService(&fakeChallenges{}, pseudos, repo, clock.NewFake(time.Time{}), 24*time.Hour)
			got, err := svc.Standings(t.Context(), "a", []Challenge{c}, tt.now)
			if err != nil {
				t.Fatal(err)
			}
			st, ok := got["c1"]
			if tt.want == nil {
				if ok {
					t.Fatalf("Standings() = %+v, want c1 left out", st)
				}
				return
			}
			if !ok || !reflect.DeepEqual(st, *tt.want) {
				t.Fatalf("Standings() = %+v, want %+v", st, *tt.want)
			}
		})
	}
}

func TestStandingsReadsEveryChallengeAtOnce(t *testing.T) {
	first := threeWeeks(t)
	second := threeWeeks(t)
	second.ID = "c2"
	gone := threeWeeks(t)
	gone.ID = "c3"
	ended := threeWeeks(t)
	ended.ID, ended.StartsAt, ended.EndsAt = "c4", first.StartsAt.AddDate(0, 0, -21), first.StartsAt
	loc := toronto(t)
	repo := &fakeRepo{
		participants: []scoring.Participant{{UserID: "a"}},
		participantsOf: map[string][]scoring.Participant{
			"c2": {{UserID: "a"}, {UserID: "b"}},
			"c3": {{UserID: "a", Abandoned: true}},
		},
		activitiesOf: map[string][]scoring.Activity{
			"c2": {{UserID: "b", SportCode: "run", StartedAt: time.Date(2026, 6, 2, 7, 0, 0, 0, loc), DurationSeconds: 60, DistanceMeters: meters(1000)}},
		},
	}
	svc := NewService(&fakeChallenges{}, pseudos, repo, clock.NewFake(time.Time{}), 24*time.Hour)

	got, err := svc.Standings(t.Context(), "a", []Challenge{first, second, gone, ended}, time.Date(2026, 6, 3, 12, 0, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	if repo.calls != 2 {
		t.Fatalf("%d reads, want 2 whatever the number of challenges", repo.calls)
	}
	// Neither the abandoned challenge nor the ended one has its activities read.
	if len(repo.scopes) != 2 || repo.scopes[0].ChallengeID != "c1" || repo.scopes[1].ChallengeID != "c2" {
		t.Fatalf("activities read for %+v, want c1 and c2", repo.scopes)
	}
	if len(got) != 2 {
		t.Fatalf("Standings() = %+v, want c1 and c2", got)
	}
	if p := got["c2"].Categories[0]; p.Entry.Rank != 2 || p.RankedCount != 2 {
		t.Fatalf("c2 time ranking = %+v, want rank 2 of 2", p)
	}
	if p := got["c1"].Categories[0]; p.Entry.Rank != 1 || p.RankedCount != 1 {
		t.Fatalf("c1 time ranking = %+v, want rank 1 of 1", p)
	}
}

func TestStandingsFailures(t *testing.T) {
	boom := errors.New("boom")
	c := threeWeeks(t)
	now := c.StartsAt.Add(time.Hour)
	bad := c
	bad.Timezone = "Nowhere/Else"
	tests := []struct {
		name       string
		challenges []Challenge
		repo       *fakeRepo
		wantCalls  int
	}{
		{name: "unknown time zone: nothing is loaded", challenges: []Challenge{bad}, repo: &fakeRepo{}},
		{name: "storage failure", challenges: []Challenge{c}, repo: &fakeRepo{err: boom}, wantCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeChallenges{}, pseudos, tt.repo, clock.NewFake(time.Time{}), time.Hour)
			if _, err := svc.Standings(t.Context(), "a", tt.challenges, now); err == nil {
				t.Fatal("Standings() error = nil, want one")
			}
			if tt.repo.calls != tt.wantCalls {
				t.Fatalf("%d reads, want %d", tt.repo.calls, tt.wantCalls)
			}
		})
	}
}

func TestStandingsWithoutChallengeInItsWeeks(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(&fakeChallenges{}, pseudos, repo, clock.NewFake(time.Time{}), time.Hour)
	got, err := svc.Standings(t.Context(), "a", []Challenge{threeWeeks(t)}, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 || repo.calls != 0 {
		t.Fatalf("Standings() = %+v after %d reads, want nothing read", got, repo.calls)
	}
}

// TestResults: the actor's lines of the frozen results of each challenge,
// read for every challenge at once, as Get serves them: a deleted account is
// left out and the participants after it move up, equal ones still sharing a
// rank; a challenge not frozen, or frozen in part, is left out.
func TestResults(t *testing.T) {
	cat := func(types ...string) []Category {
		out := make([]Category, len(types))
		for i, ty := range types {
			out[i] = Category{Type: ty}
		}
		return out
	}
	repo := &fakeRepo{
		snapshotsOf: map[string]map[string]json.RawMessage{
			"done": {
				CategoryCumulativeTime: json.RawMessage(`{"entries":[{"user_id":"gone","rank":1,"value":9000},` +
					`{"user_id":"b","rank":2,"value":5000},{"user_id":"actor","rank":2,"value":5000},{"user_id":"c","rank":0,"abandoned":true}]}`),
				CategoryElimination: json.RawMessage(`{"entries":[{"user_id":"b","rank":1,"value":22000},` +
					`{"user_id":"actor","rank":2,"value":5000,"eliminated_week":2},{"user_id":"c","rank":0,"abandoned":true}]}`),
			},
			// The elimination snapshot is missing: not all frozen.
			"partial": {
				CategoryCumulativeTime: json.RawMessage(`{"entries":[{"user_id":"actor","rank":1,"value":100}]}`),
			},
		},
		deleted: map[string]bool{"gone": true},
	}
	svc := NewService(&fakeChallenges{}, fakeProfiles{}, repo, clock.NewFake(time.Time{}), 24*time.Hour)
	got, err := svc.Results(t.Context(), "actor", []Challenge{
		{ID: "done", Categories: cat(CategoryCumulativeTime, CategoryElimination)},
		{ID: "partial", Categories: cat(CategoryCumulativeTime, CategoryElimination)},
		{ID: "pending", Categories: cat(CategoryCumulativeTime)},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]Place{"done": {
		{Type: CategoryCumulativeTime, Entry: scoring.Entry{UserID: "actor", Rank: 1, Value: 5000}, RankedCount: 2},
		{Type: CategoryElimination, Entry: scoring.Entry{
			UserID: "actor", Rank: 2, Value: 5000,
			Progress: &scoring.Progress{Status: scoring.StatusEliminated, EliminatedWeek: 2},
		}, RankedCount: 2},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Results() = %+v, want %+v", got, want)
	}
	if repo.snapshotsOfCalls != 1 || repo.deletedCalls != 1 || !reflect.DeepEqual(repo.snapshotsOfAsked, []string{"done", "partial", "pending"}) {
		t.Fatalf("%d snapshot reads of %v and %d account reads, want one of each for every challenge",
			repo.snapshotsOfCalls, repo.snapshotsOfAsked, repo.deletedCalls)
	}
}

// TestResultsReadsNothingItDoesNotNeed: no challenge, no read; nothing
// frozen, no read of the accounts.
func TestResultsReadsNothingItDoesNotNeed(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(&fakeChallenges{}, fakeProfiles{}, repo, clock.NewFake(time.Time{}), 24*time.Hour)
	got, err := svc.Results(t.Context(), "actor", nil)
	if err != nil || len(got) != 0 || repo.snapshotsOfCalls != 0 {
		t.Fatalf("Results(none) = %v, %v after %d reads, want nothing read", got, err, repo.snapshotsOfCalls)
	}
	got, err = svc.Results(t.Context(), "actor", []Challenge{{ID: "pending", Categories: []Category{{Type: CategoryCumulativeTime}}}})
	if err != nil || len(got) != 0 || repo.deletedCalls != 0 {
		t.Fatalf("Results(not frozen) = %v, %v after %d account reads, want none", got, err, repo.deletedCalls)
	}
}

// TestResultsFailures: a failed read, or a snapshot that cannot be decoded,
// fails Results.
func TestResultsFailures(t *testing.T) {
	boom := errors.New("boom")
	cats := []Category{{Type: CategoryCumulativeTime}}
	frozen := map[string]map[string]json.RawMessage{"c1": {CategoryCumulativeTime: json.RawMessage(`{"entries":[]}`)}}
	tests := []struct {
		name string
		repo *fakeRepo
	}{
		{"snapshots", &fakeRepo{snapshotsErr: boom}},
		{"accounts", &fakeRepo{snapshotsOf: frozen, deletedErr: boom}},
		{"payload", &fakeRepo{snapshotsOf: map[string]map[string]json.RawMessage{"c1": {CategoryCumulativeTime: json.RawMessage(`[`)}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeChallenges{}, fakeProfiles{}, tt.repo, clock.NewFake(time.Time{}), 24*time.Hour)
			if _, err := svc.Results(t.Context(), "actor", []Challenge{{ID: "c1", Categories: cats}}); err == nil {
				t.Fatal("Results() succeeded, want an error")
			}
		})
	}
}
