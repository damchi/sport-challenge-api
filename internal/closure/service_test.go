package closure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// fakeRepo keeps the closures and the eliminations in memory, as the
// database does: closing a week twice records it once.
type fakeRepo struct {
	challenges []Challenge
	closed     map[string][]int
	recorded   map[string][]scoring.Eliminated // by challenge, in the order recorded
	closedAt   map[string]time.Time            // by challenge and week
	failOn     string                          // challenge where reading the closed weeks fails
	loads      int                             // calls to OpenChallenges
	ended      []EndedChallenge
	snapshots  map[string][]Snapshot // by challenge
	snapshotAt time.Time
	// last is the closure of the last week given with the snapshots.
	last LastWeek
	// awaitErr is the error of AwaitActivityWrites; awaits counts its calls.
	awaitErr error
	awaits   int
	// deletedSince is how many SaveSnapshots fail as if an account ranked
	// had been deleted since the ranking.
	deletedSince int
	// steps logs, in order, the week locks taken, the transactions run and
	// the results cleared; lockErr is the error of LockWeeks.
	steps   []string
	lockErr error
}

func (f *fakeRepo) LockWeeks(context.Context) error {
	f.steps = append(f.steps, "lock")
	return f.lockErr
}

func (f *fakeRepo) WithWeeksShared(ctx context.Context, fn func(ctx context.Context) error) error {
	f.steps = append(f.steps, "shared")
	return fn(ctx)
}

// Challenge reads a challenge of ended, else one of challenges with its
// elimination category alone.
func (f *fakeRepo) Challenge(_ context.Context, id string) (EndedChallenge, bool, error) {
	for _, c := range f.ended {
		if c.ID == id {
			return c, true, nil
		}
	}
	for _, c := range f.challenges {
		if c.ID == id {
			return EndedChallenge{
				ID: c.ID, Timezone: c.Timezone, StartsAt: c.StartsAt, EndsAt: c.EndsAt,
				Categories: []Category{{ID: c.CategoryID, Type: categoryElimination, Sport: c.Sport, StepMeters: c.StepMeters}},
			}, true, nil
		}
	}
	return EndedChallenge{}, false, nil
}

func (f *fakeRepo) ClearResults(_ context.Context, id string) error {
	f.steps = append(f.steps, "clear "+id)
	delete(f.closed, id)
	delete(f.recorded, id)
	delete(f.snapshots, id)
	return nil
}

func (f *fakeRepo) AwaitActivityWrites(context.Context) error {
	f.awaits++
	return f.awaitErr
}

func newFakeRepo(cs ...Challenge) *fakeRepo {
	return &fakeRepo{challenges: cs, closed: map[string][]int{}, recorded: map[string][]scoring.Eliminated{}, closedAt: map[string]time.Time{}, snapshots: map[string][]Snapshot{}}
}

func (f *fakeRepo) ClosableChallenges(context.Context, time.Time) ([]Challenge, error) {
	f.loads++
	return f.challenges, nil
}

func (f *fakeRepo) ClosedWeeks(_ context.Context, id string) ([]int, error) {
	if id == f.failOn {
		return nil, errors.New("database down")
	}
	return f.closed[id], nil
}

func (f *fakeRepo) CloseWeek(_ context.Context, c Challenge, week int, at time.Time, eliminated []scoring.Eliminated) (bool, error) {
	for _, n := range f.closed[c.ID] {
		if n == week {
			return false, nil
		}
	}
	f.closed[c.ID] = append(f.closed[c.ID], week)
	f.recorded[c.ID] = append(f.recorded[c.ID], eliminated...)
	f.closedAt[fmt.Sprintf("%s/%d", c.ID, week)] = at
	return true, nil
}

// EndedChallenges and SaveSnapshots keep the snapshots in memory, as the
// database does: a challenge with snapshots is no longer listed.
func (f *fakeRepo) EndedChallenges(_ context.Context, now time.Time) ([]EndedChallenge, error) {
	f.loads++
	var out []EndedChallenge
	for _, c := range f.ended {
		if _, done := f.snapshots[c.ID]; !done && !now.Before(c.EndsAt) {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeRepo) SaveSnapshots(_ context.Context, id string, at time.Time, snapshots []Snapshot, last LastWeek) (bool, error) {
	if id == f.failOn {
		return false, errors.New("database down")
	}
	if f.deletedSince > 0 {
		f.deletedSince--
		return false, errRankedAccountDeleted
	}
	if _, done := f.snapshots[id]; done {
		return false, nil
	}
	f.snapshots[id] = snapshots
	f.snapshotAt = at
	f.last = last
	return true, nil
}

type fakeData struct {
	participants []scoring.Participant
	activities   []scoring.Activity
	loads        int
	// weekly is the sport the activities were asked to sum per week, and
	// minSession the minimum session of the points groups asked for, and
	// elevation whether the elevation gains were.
	weekly     string
	minSession int
	elevation  bool
}

func (f *fakeData) Participants(context.Context, string) ([]scoring.Participant, error) {
	f.loads++
	return f.participants, nil
}

func (f *fakeData) Activities(_ context.Context, _ string, _ scoring.Window, weeklySport string, minSessionSeconds int, elevation bool) ([]scoring.Activity, error) {
	f.loads++
	f.weekly, f.minSession, f.elevation = weeklySport, minSessionSeconds, elevation
	return f.activities, nil
}

func discard() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

func TestCloseWeeks(t *testing.T) {
	// 4 weeks across the time change of March 8 (week 2 lasts 167 h).
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 2, 0, 0, 0, 0, loc)
	c := Challenge{
		ID: "c1", Timezone: "America/Toronto", StartsAt: start.UTC(), EndsAt: start.AddDate(0, 0, 28).UTC(),
		CategoryID: "cat3", Sport: "run", StepMeters: 2000,
	}
	w := scoring.Window{Start: c.StartsAt, End: c.EndsAt, Location: loc}
	weekStart := func(n int) time.Time { s, _ := w.WeekBounds(n); return s }
	weekEnd := func(n int) time.Time { _, e := w.WeekBounds(n); return e }
	// A week is closed once it locks, 7 am on the day after it ends.
	locked := w.WeekLocksAt
	runs := func(user string, distances ...int) []scoring.Activity {
		var as []scoring.Activity
		for i, d := range distances {
			if d > 0 {
				m := d
				as = append(as, scoring.Activity{
					UserID: user, SportCode: "run", StartedAt: weekStart(i + 1).Add(time.Hour), DurationSeconds: 1800, DistanceMeters: &m,
				})
			}
		}
		return as
	}
	participants := []scoring.Participant{{UserID: "a"}, {UserID: "b"}, {UserID: "c"}, {UserID: "quitter", Abandoned: true}}
	var activities []scoring.Activity
	// a: eliminated in week 2 (6999 < 5000 + 2000); b: survives; c: eliminated in week 3.
	activities = append(activities, runs("a", 5000, 6999, 9000)...)
	activities = append(activities, runs("b", 5000, 7000, 9000)...)
	activities = append(activities, runs("c", 5000, 7000, 8999)...)

	run := func(t *testing.T, repo *fakeRepo, data *fakeData, at time.Time) (Result, error) {
		t.Helper()
		return NewService(repo, data, clock.NewFake(at), discard()).CloseWeeks(t.Context())
	}
	wantWeek2 := scoring.Eliminated{UserID: "a", Week: 2, DistanceMeters: 6999, MinimumMeters: 7000}
	wantWeek3 := scoring.Eliminated{UserID: "c", Week: 3, DistanceMeters: 8999, MinimumMeters: 9000}

	t.Run("the lock of a week closes it", func(t *testing.T) {
		repo, data := newFakeRepo(c), &fakeData{participants: participants, activities: activities}
		got, err := run(t, repo, data, locked(2))
		if err != nil {
			t.Fatal(err)
		}
		if want := (Result{Challenges: 1, Weeks: 2, Eliminations: 1}); got != want {
			t.Fatalf("result = %+v, want %+v", got, want)
		}
		if !reflect.DeepEqual(repo.closed["c1"], []int{1, 2}) {
			t.Fatalf("closed weeks = %v, want [1 2]", repo.closed["c1"])
		}
		if !reflect.DeepEqual(repo.recorded["c1"], []scoring.Eliminated{wantWeek2}) {
			t.Fatalf("eliminations = %+v, want %+v", repo.recorded["c1"], wantWeek2)
		}
		if at := repo.closedAt["c1/2"]; !at.Equal(locked(2)) {
			t.Fatalf("closed at %v, want the instant of the clock", at)
		}
		if data.weekly != "run" {
			t.Fatalf("weekly sport = %q, want run, the sport of the elimination category", data.weekly)
		}
	})

	t.Run("the exact end of a week does not close it", func(t *testing.T) {
		repo, data := newFakeRepo(c), &fakeData{participants: participants, activities: activities}
		got, err := run(t, repo, data, weekEnd(2))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(repo.closed["c1"], []int{1}) || len(repo.recorded["c1"]) != 0 || got.Eliminations != 0 {
			t.Fatalf("result = %+v, closed = %v, eliminations = %+v, want week 1 only", got, repo.closed["c1"], repo.recorded["c1"])
		}
	})

	t.Run("nothing closes the nanosecond before the lock", func(t *testing.T) {
		repo, data := newFakeRepo(c), &fakeData{participants: participants, activities: activities}
		got, err := run(t, repo, data, locked(1).Add(-time.Nanosecond))
		if err != nil || got != (Result{}) || len(repo.closed["c1"]) != 0 {
			t.Fatalf("result = %+v, err = %v, closed = %v, want nothing", got, err, repo.closed["c1"])
		}
		if data.loads != 0 {
			t.Fatalf("%d reads of participants and activities, want none when there is nothing to close", data.loads)
		}
	})

	t.Run("running again changes nothing", func(t *testing.T) {
		repo, data := newFakeRepo(c), &fakeData{participants: participants, activities: activities}
		if _, err := run(t, repo, data, locked(3)); err != nil {
			t.Fatal(err)
		}
		closed, recorded := append([]int(nil), repo.closed["c1"]...), append([]scoring.Eliminated(nil), repo.recorded["c1"]...)
		data.loads = 0
		got, err := run(t, repo, data, locked(3).Add(time.Minute))
		if err != nil || got != (Result{}) {
			t.Fatalf("second run = %+v, err = %v, want nothing done", got, err)
		}
		if !reflect.DeepEqual(repo.closed["c1"], closed) || !reflect.DeepEqual(repo.recorded["c1"], recorded) {
			t.Fatalf("second run changed the records: %v %+v", repo.closed["c1"], repo.recorded["c1"])
		}
		if data.loads != 0 {
			t.Fatalf("second run read %d times, want 0", data.loads)
		}
	})

	t.Run("an hour late or on time gives the same eliminations", func(t *testing.T) {
		onTime, late := newFakeRepo(c), newFakeRepo(c)
		if _, err := run(t, onTime, &fakeData{participants: participants, activities: activities}, locked(3)); err != nil {
			t.Fatal(err)
		}
		if _, err := run(t, late, &fakeData{participants: participants, activities: activities}, locked(3).Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(onTime.recorded, late.recorded) || !reflect.DeepEqual(onTime.closed, late.closed) {
			t.Fatalf("on time %v %+v, late %v %+v", onTime.closed, onTime.recorded, late.closed, late.recorded)
		}
		if want := []scoring.Eliminated{wantWeek2, wantWeek3}; !reflect.DeepEqual(late.recorded["c1"], want) {
			t.Fatalf("eliminations = %+v, want %+v", late.recorded["c1"], want)
		}
	})

	t.Run("one run per week gives what a single late run gives", func(t *testing.T) {
		stepwise, once := newFakeRepo(c), newFakeRepo(c)
		data := &fakeData{participants: participants, activities: activities}
		for n := 1; n <= 3; n++ {
			if _, err := run(t, stepwise, data, locked(n)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := run(t, once, data, locked(3)); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(stepwise.recorded, once.recorded) || !reflect.DeepEqual(stepwise.closed["c1"], once.closed["c1"]) {
			t.Fatalf("stepwise %v %+v, once %v %+v", stepwise.closed, stepwise.recorded, once.closed, once.recorded)
		}
	})

	t.Run("a week already closed is not closed again", func(t *testing.T) {
		repo := newFakeRepo(c)
		repo.closed["c1"] = []int{1}
		got, err := run(t, repo, &fakeData{participants: participants, activities: activities}, locked(2))
		if err != nil || got.Weeks != 1 {
			t.Fatalf("result = %+v, err = %v, want only week 2", got, err)
		}
	})

	t.Run("the recorded eliminations are the ones of the scorer", func(t *testing.T) {
		repo, data := newFakeRepo(c), &fakeData{participants: participants, activities: activities}
		now := locked(3)
		if _, err := run(t, repo, data, now); err != nil {
			t.Fatal(err)
		}
		recorded := map[string]int{}
		for _, e := range repo.recorded["c1"] {
			recorded[e.UserID] = e.Week
		}
		scored := map[string]int{}
		sw := w
		sw.Now = now
		for _, entry := range (scoring.Elimination{Sport: "run", StepMeters: 2000}).Score(sw, participants, activities) {
			if entry.Progress != nil && entry.Progress.EliminatedWeek > 0 {
				scored[entry.UserID] = entry.Progress.EliminatedWeek
			}
		}
		if !reflect.DeepEqual(recorded, scored) {
			t.Fatalf("recorded %v, the scorer eliminates %v", recorded, scored)
		}
		if _, ok := recorded["quitter"]; ok {
			t.Fatal("an abandoned participant was recorded as eliminated")
		}
	})

	t.Run("the end of the challenge is left to its finalization", func(t *testing.T) {
		repo := newFakeRepo(c)
		repo.closed["c1"] = []int{1, 2, 3}
		got, err := run(t, repo, &fakeData{participants: participants, activities: activities}, c.EndsAt.Add(-time.Nanosecond))
		if err != nil || got != (Result{}) {
			t.Fatalf("result = %+v, err = %v, want nothing", got, err)
		}
	})

	t.Run("a worker down until after the end catches up every week but the last", func(t *testing.T) {
		repo, data := newFakeRepo(c), &fakeData{participants: participants, activities: activities}
		got, err := run(t, repo, data, c.EndsAt.AddDate(0, 0, 30))
		if err != nil {
			t.Fatal(err)
		}
		if want := (Result{Challenges: 1, Weeks: 3, Eliminations: 2}); got != want {
			t.Fatalf("result = %+v, want %+v", got, want)
		}
		if !reflect.DeepEqual(repo.closed["c1"], []int{1, 2, 3}) {
			t.Fatalf("closed weeks = %v, want [1 2 3]: week 4 is for the finalization", repo.closed["c1"])
		}
		if want := []scoring.Eliminated{wantWeek2, wantWeek3}; !reflect.DeepEqual(repo.recorded["c1"], want) {
			t.Fatalf("eliminations = %+v, want %+v", repo.recorded["c1"], want)
		}
	})

	t.Run("a challenge in error does not stop the others", func(t *testing.T) {
		other := c
		other.ID = "c2"
		repo := newFakeRepo(c, other)
		repo.failOn = "c1"
		got, err := run(t, repo, &fakeData{participants: participants, activities: activities}, locked(2))
		if err == nil {
			t.Fatal("error = nil, want the failure of c1")
		}
		if got.Weeks != 2 || !reflect.DeepEqual(repo.closed["c2"], []int{1, 2}) || len(repo.closed["c1"]) != 0 {
			t.Fatalf("result = %+v, closed = %v", got, repo.closed)
		}
	})

	t.Run("an unknown time zone is an error", func(t *testing.T) {
		broken := c
		broken.Timezone = "Mars/Olympus"
		if _, err := run(t, newFakeRepo(broken), &fakeData{}, locked(2)); err == nil {
			t.Fatal("error = nil, want the unknown time zone")
		}
	})
}

func TestFinalizeChallenges(t *testing.T) {
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 2, 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 28).UTC()
	// The last week locks at 7 am on the day after the end: the results are
	// frozen then.
	lock := time.Date(2026, 3, 30, 7, 0, 0, 0, loc).UTC()
	c := EndedChallenge{
		ID: "c1", Timezone: "America/Toronto", StartsAt: start.UTC(), EndsAt: end,
		Categories: []Category{
			{ID: "cat1", Type: categoryCumulativeTime},
			{ID: "cat2", Type: categoryCumulativeDistance, Factors: map[string]float64{"run": 1}},
			{ID: "cat3", Type: categoryElimination, Sport: "run", StepMeters: 2000},
		},
	}
	run := func(week int, meters int) scoring.Activity {
		m := meters
		return scoring.Activity{
			UserID: "", SportCode: "run", StartedAt: start.AddDate(0, 0, 7*(week-1)).Add(time.Hour).UTC(), DurationSeconds: 1800, DistanceMeters: &m,
		}
	}
	runs := func(user string, distances ...int) []scoring.Activity {
		var as []scoring.Activity
		for i, d := range distances {
			a := run(i+1, d)
			a.UserID = user
			as = append(as, a)
		}
		return as
	}
	participants := []scoring.Participant{{UserID: "a"}, {UserID: "b"}, {UserID: "quitter", Abandoned: true}}
	// a misses the minimum of the last week (9000 + 2000 > 10000): eliminated
	// in week 4, the week only the finalization judges; b keeps up.
	var activities []scoring.Activity
	activities = append(activities, runs("a", 5000, 7000, 9000, 10000)...)
	activities = append(activities, runs("b", 5000, 7000, 9000, 11000)...)

	finalize := func(t *testing.T, repo *fakeRepo, at time.Time) (FinalizeResult, error) {
		t.Helper()
		return NewService(repo, &fakeData{participants: participants, activities: activities}, clock.NewFake(at), discard()).FinalizeChallenges(t.Context())
	}
	payloadOf := func(t *testing.T, repo *fakeRepo, category string) payload {
		t.Helper()
		for _, s := range repo.snapshots["c1"] {
			if s.CategoryID == category {
				var p payload
				if err := json.Unmarshal(s.Payload, &p); err != nil {
					t.Fatal(err)
				}
				return p
			}
		}
		t.Fatalf("no snapshot for %s", category)
		return payload{}
	}

	t.Run("the lock of the last week freezes the ranking of every category", func(t *testing.T) {
		repo := newFakeRepo()
		repo.ended = []EndedChallenge{c}
		got, err := finalize(t, repo, lock)
		if err != nil || got != (FinalizeResult{Challenges: 1}) {
			t.Fatalf("result = %+v, err = %v, want 1 challenge", got, err)
		}
		if !repo.snapshotAt.Equal(lock) {
			t.Fatalf("snapshot at %v, want the instant of the clock", repo.snapshotAt)
		}
		// The last week is closed with the results (SC-API-089): only its
		// eliminations, in the elimination category.
		wantLast := LastWeek{Number: 4, CategoryID: "cat3", Eliminated: []scoring.Eliminated{{UserID: "a", Week: 4, DistanceMeters: 10000, MinimumMeters: 11000}}}
		if !reflect.DeepEqual(repo.last, wantLast) {
			t.Fatalf("last week = %+v, want %+v", repo.last, wantLast)
		}
		for i, cat := range c.Categories {
			if s := repo.snapshots["c1"][i]; s.CategoryID != cat.ID || s.Type != cat.Type {
				t.Fatalf("snapshot %d = %s %s, want %s %s", i, s.CategoryID, s.Type, cat.ID, cat.Type)
			}
		}
		want := payload{Entries: []payloadEntry{
			{UserID: "b", Rank: 1, Value: 32000},
			{UserID: "a", Rank: 2, Value: 31000, EliminatedWeek: 4},
			{UserID: "quitter", Abandoned: true},
		}}
		if got := payloadOf(t, repo, "cat3"); !reflect.DeepEqual(got, want) {
			t.Fatalf("elimination ranking = %+v, want %+v", got, want)
		}
		if got := payloadOf(t, repo, "cat1"); len(got.Entries) != 3 || got.Entries[0].Rank != 1 {
			t.Fatalf("time ranking = %+v, want 3 entries from rank 1", got)
		}
	})

	t.Run("a last week without elimination is closed all the same", func(t *testing.T) {
		repo := newFakeRepo()
		repo.ended = []EndedChallenge{c}
		keepUp := append(runs("a", 5000, 7000, 9000, 11000), runs("b", 5000, 7000, 9000, 11000)...)
		data := &fakeData{participants: participants, activities: keepUp}
		svc := NewService(repo, data, clock.NewFake(lock), discard())
		if _, err := svc.FinalizeChallenges(t.Context()); err != nil {
			t.Fatal(err)
		}
		if data.weekly != "run" {
			t.Fatalf("weekly sport = %q, want run, the sport of the elimination category", data.weekly)
		}
		if want := (LastWeek{Number: 4, CategoryID: "cat3"}); !reflect.DeepEqual(repo.last, want) {
			t.Fatalf("last week = %+v, want %+v", repo.last, want)
		}
	})

	t.Run("the points category is frozen from its groups, without provisional points", func(t *testing.T) {
		repo := newFakeRepo()
		withPoints := c
		withPoints.Categories = append(slices.Clone(c.Categories), Category{
			ID: "cat4", Type: categoryPoints, Criteria: []string{scoring.CriterionLongestDistance}, Podium: 5,
			Factors: map[string]float64{"run": 1}, MinSessionSeconds: 1200,
		})
		repo.ended = []EndedChallenge{withPoints}
		groups := make([]scoring.Activity, len(activities))
		for i, a := range activities {
			a.LongestSeconds, a.LongestMeters, a.Sessions = a.DurationSeconds, a.DistanceMeters, 1
			groups[i] = a
		}
		data := &fakeData{participants: participants, activities: groups}
		if _, err := NewService(repo, data, clock.NewFake(lock), discard()).FinalizeChallenges(t.Context()); err != nil {
			t.Fatal(err)
		}
		if data.weekly != "run" || data.minSession != 1200 {
			t.Fatalf("activities asked with %q per week and a %d s minimum, want run and 1200", data.weekly, data.minSession)
		}
		// Equal in weeks 1 to 3 (10 each), b ahead in week 4 (10 against 8).
		want := payload{Entries: []payloadEntry{
			{UserID: "b", Rank: 1, Value: 40},
			{UserID: "a", Rank: 2, Value: 38},
			{UserID: "quitter", Abandoned: true},
		}}
		if got := withoutWeeks(t, payloadOf(t, repo, "cat4"), 4); !reflect.DeepEqual(got, want) {
			t.Fatalf("points ranking = %+v, want %+v", got, want)
		}
		for _, snap := range repo.snapshots["c1"] {
			if strings.Contains(string(snap.Payload), "provisional") {
				t.Fatalf("snapshot %s carries provisional points: %s", snap.CategoryID, snap.Payload)
			}
		}
	})

	t.Run("the weekly elevation criterion is frozen from the elevation gains of each week", func(t *testing.T) {
		repo := newFakeRepo()
		withPoints := c
		withPoints.Categories = append(slices.Clone(c.Categories), Category{
			ID: "cat4", Type: categoryPoints, Criteria: []string{scoring.CriterionWeeklyElevation}, Podium: 5, MinSessionSeconds: 1200,
		})
		repo.ended = []EndedChallenge{withPoints}
		// a climbs more in weeks 1 and 2, b in weeks 3 and 4.
		gains := map[string][]int{"a": {500, 400, 100, 0}, "b": {200, 300, 600, 700}}
		groups := make([]scoring.Activity, len(activities))
		weeks := scoring.Window{Start: c.StartsAt, End: c.EndsAt, Location: loc}
		for i, a := range activities {
			g := gains[a.UserID][weeks.WeekOf(a.StartedAt)-1]
			a.ElevationMeters = &g
			groups[i] = a
		}
		data := &fakeData{participants: participants, activities: groups}
		if _, err := NewService(repo, data, clock.NewFake(lock), discard()).FinalizeChallenges(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !data.elevation {
			t.Fatal("activities asked without the elevation gains")
		}
		// b 8 + 8 + 10 + 10; a 10 + 10 + 8, and nothing in week 4 (0 m does
		// not score). All acquired.
		want := payload{Entries: []payloadEntry{
			{UserID: "b", Rank: 1, Value: 36}, {UserID: "a", Rank: 2, Value: 28}, {UserID: "quitter", Abandoned: true},
		}}
		if got := withoutWeeks(t, payloadOf(t, repo, "cat4"), 4); !reflect.DeepEqual(got, want) {
			t.Fatalf("points ranking = %+v, want %+v", got, want)
		}
	})

	t.Run("the cumulative elevation category is frozen from the elevation gains", func(t *testing.T) {
		repo := newFakeRepo()
		withElevation := c
		withElevation.Categories = append(slices.Clone(c.Categories), Category{ID: "cat5", Type: categoryElevation})
		repo.ended = []EndedChallenge{withElevation}
		gain := func(m int) *int { return &m }
		climbs := slices.Clone(activities)
		climbs = append(climbs,
			scoring.Activity{UserID: "a", SportCode: "hike", StartedAt: c.StartsAt, DurationSeconds: 60, DistanceMeters: gain(100), ElevationMeters: gain(800)},
			scoring.Activity{UserID: "b", SportCode: "ride", StartedAt: c.StartsAt, DurationSeconds: 60, DistanceMeters: gain(100), ElevationMeters: gain(800)},
			scoring.Activity{UserID: "quitter", SportCode: "ride", StartedAt: c.StartsAt, DurationSeconds: 60, DistanceMeters: gain(100), ElevationMeters: gain(5000)},
		)
		data := &fakeData{participants: participants, activities: climbs}
		if _, err := NewService(repo, data, clock.NewFake(lock), discard()).FinalizeChallenges(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !data.elevation {
			t.Fatal("activities asked without the elevation gains")
		}
		want := payload{Entries: []payloadEntry{
			{UserID: "a", Rank: 1, Value: 800},
			{UserID: "b", Rank: 1, Value: 800},
			{UserID: "quitter", Abandoned: true},
		}}
		if got := payloadOf(t, repo, "cat5"); !reflect.DeepEqual(got, want) {
			t.Fatalf("elevation ranking = %+v, want %+v", got, want)
		}
	})

	t.Run("an account deleted since the ranking has the challenge ranked again", func(t *testing.T) {
		repo := newFakeRepo()
		repo.ended = []EndedChallenge{c}
		repo.deletedSince = 1
		data := &fakeData{participants: participants, activities: activities}
		got, err := NewService(repo, data, clock.NewFake(lock), discard()).FinalizeChallenges(t.Context())
		if err != nil || got != (FinalizeResult{Challenges: 1}) || len(repo.snapshots["c1"]) != len(c.Categories) {
			t.Fatalf("result = %+v, err = %v, want 1 challenge frozen", got, err)
		}
		// Participants and activities read twice: ranked again.
		if data.loads != 4 {
			t.Fatalf("%d reads, want 4: the challenge ranked twice", data.loads)
		}
	})

	t.Run("an account deleted at each attempt is left to the next run", func(t *testing.T) {
		repo := newFakeRepo()
		repo.ended = []EndedChallenge{c}
		repo.deletedSince = freezeAttempts
		got, err := finalize(t, repo, lock)
		if !errors.Is(err, errRankedAccountDeleted) || got != (FinalizeResult{}) || len(repo.snapshots) != 0 {
			t.Fatalf("result = %+v, err = %v, want nothing frozen and the error", got, err)
		}
	})

	for name, at := range map[string]time.Time{
		"nothing is frozen the nanosecond before the end":         end.Add(-time.Nanosecond),
		"nothing is frozen at the end, before the lock":           end,
		"nothing is frozen the nanosecond before the lock (7 am)": lock.Add(-time.Nanosecond),
	} {
		t.Run(name, func(t *testing.T) {
			repo := newFakeRepo()
			repo.ended = []EndedChallenge{c}
			got, err := finalize(t, repo, at)
			if err != nil || got != (FinalizeResult{}) || len(repo.snapshots) != 0 {
				t.Fatalf("result = %+v, err = %v, snapshots = %v, want nothing", got, err, repo.snapshots)
			}
		})
	}

	t.Run("running again, even later, changes nothing", func(t *testing.T) {
		repo := newFakeRepo()
		repo.ended = []EndedChallenge{c}
		if _, err := finalize(t, repo, lock); err != nil {
			t.Fatal(err)
		}
		before := repo.snapshots["c1"]
		got, err := finalize(t, repo, lock.Add(time.Hour))
		if err != nil || got != (FinalizeResult{}) {
			t.Fatalf("second run = %+v, err = %v, want nothing done", got, err)
		}
		if !reflect.DeepEqual(repo.snapshots["c1"], before) || !repo.snapshotAt.Equal(lock) {
			t.Fatalf("second run changed the snapshots")
		}
	})

	t.Run("a run at the same time that lost records nothing", func(t *testing.T) {
		repo := newFakeRepo()
		repo.ended = []EndedChallenge{c}
		// The listing is stale: the challenge was finalized since it was read.
		repo.snapshots["c1"] = []Snapshot{{CategoryID: "cat1"}}
		svc := NewService(staleRepo{repo}, &fakeData{participants: participants, activities: activities}, clock.NewFake(lock), discard())
		got, err := svc.FinalizeChallenges(t.Context())
		if err != nil || got != (FinalizeResult{}) {
			t.Fatalf("result = %+v, err = %v, want nothing done", got, err)
		}
	})

	t.Run("an hour late gives the same ranking", func(t *testing.T) {
		onTime, late := newFakeRepo(), newFakeRepo()
		onTime.ended, late.ended = []EndedChallenge{c}, []EndedChallenge{c}
		if _, err := finalize(t, onTime, lock); err != nil {
			t.Fatal(err)
		}
		if _, err := finalize(t, late, lock.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(onTime.snapshots, late.snapshots) {
			t.Fatalf("on time %+v, late %+v", onTime.snapshots, late.snapshots)
		}
	})

	t.Run("a challenge in error does not stop the others", func(t *testing.T) {
		other := c
		other.ID = "c2"
		repo := newFakeRepo()
		repo.ended = []EndedChallenge{c, other}
		repo.failOn = "c1"
		got, err := finalize(t, repo, lock)
		if err == nil || got != (FinalizeResult{Challenges: 1}) {
			t.Fatalf("result = %+v, err = %v, want 1 challenge and an error", got, err)
		}
		if _, ok := repo.snapshots["c2"]; !ok {
			t.Fatal("c2 was not finalized")
		}
	})

	t.Run("an unknown category type is an error", func(t *testing.T) {
		broken := c
		broken.Categories = []Category{{ID: "cat9", Type: "fastest"}}
		repo := newFakeRepo()
		repo.ended = []EndedChallenge{broken}
		if _, err := finalize(t, repo, lock); err == nil || len(repo.snapshots) != 0 {
			t.Fatalf("err = %v, snapshots = %v, want an error and nothing recorded", err, repo.snapshots)
		}
	})
}

// staleRepo lists the challenges without looking at the snapshots, as a run
// does that read the listing just before another one finalized.
type staleRepo struct{ *fakeRepo }

func (s staleRepo) EndedChallenges(context.Context, time.Time) ([]EndedChallenge, error) {
	return s.ended, nil
}

func TestEliminationSport(t *testing.T) {
	with := []Category{{Type: categoryCumulativeTime}, {Type: categoryElimination, Sport: "run"}}
	if got := eliminationSport(with); got != "run" {
		t.Fatalf("with an elimination category: %q, want run", got)
	}
	if got := eliminationSport(with[:1]); got != "" {
		t.Fatalf("without an elimination category: %q, want empty", got)
	}
}

func TestHasElevation(t *testing.T) {
	with := []Category{{Type: categoryCumulativeTime}, {Type: categoryElevation}}
	if !hasElevation(with) {
		t.Fatal("with a cumulative elevation category: false, want true")
	}
	if hasElevation(with[:1]) {
		t.Fatal("without a cumulative elevation category: true, want false")
	}
	pointsWith := func(criteria ...string) []Category {
		return []Category{{Type: categoryCumulativeTime}, {Type: categoryPoints, Criteria: criteria, Podium: 5}}
	}
	if !hasElevation(pointsWith(scoring.CriterionSessionCount, scoring.CriterionWeeklyElevation)) {
		t.Fatal("with the weekly elevation criterion: false, want true")
	}
	if hasElevation(pointsWith(scoring.CriterionSessionCount)) {
		t.Fatal("with points without the weekly elevation: true, want false")
	}
}

func TestRunsAwaitTheActivityWrites(t *testing.T) {
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 2, 0, 0, 0, 0, loc)
	c := Challenge{
		ID: "c1", Timezone: "America/Toronto", StartsAt: start.UTC(), EndsAt: start.AddDate(0, 0, 28).UTC(),
		CategoryID: "cat3", Sport: "run", StepMeters: 2000,
	}
	ended := EndedChallenge{ID: "c1", Timezone: c.Timezone, StartsAt: c.StartsAt, EndsAt: c.EndsAt, Categories: []Category{{ID: "cat1", Type: categoryCumulativeTime}}}
	after := clock.NewFake(start.AddDate(0, 0, 40))

	tests := []struct {
		name string
		run  func(svc *Service) error
	}{
		{"close-weeks", func(svc *Service) error { _, err := svc.CloseWeeks(t.Context()); return err }},
		{"finalize-challenges", func(svc *Service) error { _, err := svc.FinalizeChallenges(t.Context()); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name+" waits once", func(t *testing.T) {
			repo := newFakeRepo(c)
			repo.ended = []EndedChallenge{ended}
			if err := tt.run(NewService(repo, &fakeData{}, after, discard())); err != nil {
				t.Fatal(err)
			}
			if repo.awaits != 1 {
				t.Fatalf("%d waits, want 1", repo.awaits)
			}
		})
		t.Run(tt.name+" judges nothing when the wait fails", func(t *testing.T) {
			repo := newFakeRepo(c)
			repo.ended = []EndedChallenge{ended}
			repo.awaitErr = errors.New("database down")
			data := &fakeData{}
			err := tt.run(NewService(repo, data, after, discard()))
			if err == nil || repo.loads != 0 || data.loads != 0 || len(repo.closed) != 0 || len(repo.snapshots) != 0 {
				t.Fatalf("err = %v, %d listings, %d reads, closed %v, snapshots %v, want an error and nothing read",
					err, repo.loads, data.loads, repo.closed, repo.snapshots)
			}
		})
	}
}

// withoutWeeks checks the weeks frozen in each entry of the points ranking
// p, of a challenge of weeks weeks: there for everyone, every week for a
// participant ranked, summing to the value; then returns p without them.
func withoutWeeks(t *testing.T, p payload, weeks int) payload {
	t.Helper()
	out := payload{Entries: make([]payloadEntry, len(p.Entries))}
	for i, e := range p.Entries {
		if e.Weeks == nil {
			t.Fatalf("%s: no weeks frozen", e.UserID)
		}
		sum := 0
		for _, w := range *e.Weeks {
			sum += w.Points
		}
		if !e.Abandoned && (len(*e.Weeks) != weeks || sum != e.Value) {
			t.Fatalf("%s: weeks %+v, want %d weeks summing to %d", e.UserID, *e.Weeks, weeks, e.Value)
		}
		e.Weeks = nil
		out.Entries[i] = e
	}
	return out
}
