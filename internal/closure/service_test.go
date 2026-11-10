package closure

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
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
}

func newFakeRepo(cs ...Challenge) *fakeRepo {
	return &fakeRepo{challenges: cs, closed: map[string][]int{}, recorded: map[string][]scoring.Eliminated{}, closedAt: map[string]time.Time{}}
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

type fakeData struct {
	participants []scoring.Participant
	activities   []scoring.Activity
	loads        int
}

func (f *fakeData) Participants(context.Context, string) ([]scoring.Participant, error) {
	f.loads++
	return f.participants, nil
}

func (f *fakeData) Activities(context.Context, string, time.Time, time.Time) ([]scoring.Activity, error) {
	f.loads++
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
	wantWeek2 := scoring.Eliminated{UserID: "a", Week: 2, RunMeters: 6999, MinimumMeters: 7000}
	wantWeek3 := scoring.Eliminated{UserID: "c", Week: 3, RunMeters: 8999, MinimumMeters: 9000}

	t.Run("the exact end of a week closes it", func(t *testing.T) {
		repo, data := newFakeRepo(c), &fakeData{participants: participants, activities: activities}
		got, err := run(t, repo, data, weekEnd(2))
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
		if at := repo.closedAt["c1/2"]; !at.Equal(weekEnd(2)) {
			t.Fatalf("closed at %v, want the instant of the clock", at)
		}
	})

	t.Run("nothing closes the nanosecond before", func(t *testing.T) {
		repo, data := newFakeRepo(c), &fakeData{participants: participants, activities: activities}
		got, err := run(t, repo, data, weekEnd(1).Add(-time.Nanosecond))
		if err != nil || got != (Result{}) || len(repo.closed["c1"]) != 0 {
			t.Fatalf("result = %+v, err = %v, closed = %v, want nothing", got, err, repo.closed["c1"])
		}
		if data.loads != 0 {
			t.Fatalf("%d reads of participants and activities, want none when there is nothing to close", data.loads)
		}
	})

	t.Run("running again changes nothing", func(t *testing.T) {
		repo, data := newFakeRepo(c), &fakeData{participants: participants, activities: activities}
		if _, err := run(t, repo, data, weekEnd(3)); err != nil {
			t.Fatal(err)
		}
		closed, recorded := append([]int(nil), repo.closed["c1"]...), append([]scoring.Eliminated(nil), repo.recorded["c1"]...)
		data.loads = 0
		got, err := run(t, repo, data, weekEnd(3).Add(time.Minute))
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
		if _, err := run(t, onTime, &fakeData{participants: participants, activities: activities}, weekEnd(3)); err != nil {
			t.Fatal(err)
		}
		if _, err := run(t, late, &fakeData{participants: participants, activities: activities}, weekEnd(3).Add(time.Hour)); err != nil {
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
			if _, err := run(t, stepwise, data, weekEnd(n)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := run(t, once, data, weekEnd(3)); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(stepwise.recorded, once.recorded) || !reflect.DeepEqual(stepwise.closed["c1"], once.closed["c1"]) {
			t.Fatalf("stepwise %v %+v, once %v %+v", stepwise.closed, stepwise.recorded, once.closed, once.recorded)
		}
	})

	t.Run("a week already closed is not closed again", func(t *testing.T) {
		repo := newFakeRepo(c)
		repo.closed["c1"] = []int{1}
		got, err := run(t, repo, &fakeData{participants: participants, activities: activities}, weekEnd(2))
		if err != nil || got.Weeks != 1 {
			t.Fatalf("result = %+v, err = %v, want only week 2", got, err)
		}
	})

	t.Run("the recorded eliminations are the ones of the scorer", func(t *testing.T) {
		repo, data := newFakeRepo(c), &fakeData{participants: participants, activities: activities}
		now := weekEnd(3)
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
		got, err := run(t, repo, &fakeData{participants: participants, activities: activities}, weekEnd(2))
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
		if _, err := run(t, newFakeRepo(broken), &fakeData{}, weekEnd(2)); err == nil {
			t.Fatal("error = nil, want the unknown time zone")
		}
	})
}
