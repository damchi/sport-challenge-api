//go:build integration

package closure

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/damchi/sport-challenge-api/internal/leaderboard"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// recomputeFixture is a 4-week challenge across the time change of March 8
// with every kind of category the worker freezes, and participants who
// survive, are eliminated in a week the worker closes, in the last week, and
// who is eliminated in week 2 then abandons once it is locked.
type recomputeFixture struct {
	seed
	gdb       *gorm.DB
	id        string
	a, b, c   string
	quitter   string
	locked    func(n int) time.Time
	inWeek    func(n int) time.Time
	runOfBWk2 string
}

func newRecomputeFixture(t *testing.T) recomputeFixture {
	t.Helper()
	pool := testdb.New(t)
	gdb, err := db.NewGorm(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	f := recomputeFixture{seed: seed{t: t, pool: pool}, gdb: gdb}
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 2, 0, 0, 0, 0, loc)
	f.locked = func(n int) time.Time { return time.Date(2026, 3, 2+7*n, 7, 0, 0, 0, loc).UTC() }
	f.inWeek = func(n int) time.Time { return start.AddDate(0, 0, 7*(n-1)).Add(10 * time.Hour) }
	f.a, f.b, f.c = f.user("a"), f.user("b"), f.user("c")
	quitter := f.user("quitter")
	f.quitter = quitter
	f.scan(&f.id, `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by)
		VALUES ('Spring', 'public', 'America/Toronto', $1, $2, $3) RETURNING id`, start, start.AddDate(0, 0, 28), f.a)
	f.exec(`INSERT INTO categories (challenge_id, type, position, params) VALUES
		($1, 'cumulative_time', 1, '{}'),
		($1, 'cumulative_distance', 2, '{"factors": {"run": 1, "ride": 0.3}}'),
		($1, 'elimination', 3, '{"sport": "run", "step_meters": 2000}'),
		($1, 'points', 4, '{"criteria": ["longest_duration", "session_count"], "podium": 5, "min_session_seconds": 1200}')`, f.id)
	f.exec(`INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $5), ($1, $3, $5), ($1, $4, $5)`, f.id, f.a, f.b, f.c, start)
	// The quitter leaves once week 2 is locked.
	f.exec(`INSERT INTO participants (challenge_id, user_id, status, joined_at, abandoned_at) VALUES ($1, $2, 'abandoned', $3, $4)`,
		f.id, quitter, start, f.locked(2).Add(time.Hour))
	// a keeps up; b misses week 3 (8000 < 7000 + 2000); c misses the last
	// week (10000 < 9000 + 2000), judged by the finalization.
	for week, meters := range []int{5000, 7000, 9000, 11000} {
		f.run(f.a, f.inWeek(week+1), meters)
	}
	for week, meters := range []int{5000, 7000, 8000} {
		f.run(f.b, f.inWeek(week+1), meters)
	}
	for week, meters := range []int{5000, 7000, 9000, 10000} {
		f.run(f.c, f.inWeek(week+1), meters)
	}
	f.run(quitter, f.inWeek(1), 6000)
	f.exec(`INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters) VALUES ($1, 'ride', $2, 5400, 30000)`, f.b, f.inWeek(2).Add(time.Hour))
	f.scan(&f.runOfBWk2, `SELECT id FROM activities WHERE user_id = $1 AND sport_code = 'run' AND started_at = $2`, f.b, f.inWeek(2))
	return f
}

func (f recomputeFixture) service(at time.Time) *Service {
	return f.serviceOn(leaderboard.NewRepository(f.gdb), at)
}

func (f recomputeFixture) serviceOn(data Activities, at time.Time) *Service {
	return NewService(NewRepository(f.gdb), data, clock.NewFake(at), slog.New(slog.NewJSONHandler(io.Discard, nil)))
}

// worker runs close-weeks at the lock of each week up to last, then
// finalize-challenges, as the scheduler would.
func (f recomputeFixture) worker(last int) {
	f.t.Helper()
	for n := 1; n <= last; n++ {
		if _, err := f.service(f.locked(n)).CloseWeeks(f.t.Context()); err != nil {
			f.t.Fatal(err)
		}
		if _, err := f.service(f.locked(n)).FinalizeChallenges(f.t.Context()); err != nil {
			f.t.Fatal(err)
		}
	}
}

// recompute runs change (when given) then Recompute at at in a transaction
// of its own, which the week lock opens, as an admin change does; fail, when
// given, is returned after them, which rolls everything back.
func (f recomputeFixture) recompute(at time.Time, change func(tx *gorm.DB) error, fail ...error) error {
	return db.InTx(f.t.Context(), f.gdb, func(ctx context.Context) error {
		tx := db.Conn(ctx, f.gdb)
		if err := db.LockWeeks(tx); err != nil {
			return err
		}
		if change != nil {
			if err := change(tx); err != nil {
				return err
			}
		}
		if err := f.service(at).Recompute(ctx, f.id); err != nil {
			return err
		}
		return errors.Join(fail...)
	})
}

// judged is what the worker recorded for the challenge.
type judged struct {
	Weeks        []int
	Eliminations []recordedElimination
	Payloads     map[string]payload
}

func (f recomputeFixture) judged() judged {
	f.t.Helper()
	out := judged{Weeks: f.closedWeeks(f.id), Eliminations: f.eliminations(f.id), Payloads: map[string]payload{}}
	for _, s := range f.snapshots(f.id) {
		out.Payloads[s.Type] = s.Payload
	}
	return out
}

// tamper puts in place results the activities do not give, as left by a
// judgement before an admin change.
func (f recomputeFixture) tamper() {
	f.t.Helper()
	f.exec(`DELETE FROM elimination WHERE challenge_id = $1`, f.id)
	f.exec(`INSERT INTO elimination (challenge_id, category_id, user_id, week_number, distance_meters, required_meters)
		SELECT challenge_id, id, $2, 2, 0, 7000 FROM categories WHERE challenge_id = $1 AND type = 'elimination'`, f.id, f.a)
	f.exec(`UPDATE result_snapshot SET payload = '{"entries": []}' WHERE challenge_id = $1`, f.id)
}

// TestRecomputeGivesTheResultOfTheWorker: on the same activities, Recompute
// records the closures, eliminations and frozen results of close-weeks and
// finalize-challenges, whatever the results it finds.
func TestRecomputeGivesTheResultOfTheWorker(t *testing.T) {
	for _, tt := range []struct {
		name string
		last int // the last week locked
	}{
		{name: "a challenge in progress", last: 3},
		{name: "a challenge over, its results frozen", last: 4},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newRecomputeFixture(t)
			f.worker(tt.last)
			want := f.judged()
			// The quitter (week 2, before leaving), b (week 3), then c (week 4).
			if tt.last == 4 && (len(want.Payloads) != 4 || len(want.Eliminations) != 3) {
				t.Fatalf("worker judged %+v, want 4 snapshots and 3 eliminations", want)
			}
			if tt.last == 3 && (len(want.Payloads) != 0 || len(want.Eliminations) != 2) {
				t.Fatalf("worker judged %+v, want no snapshot, the quitter and b eliminated", want)
			}
			if q := want.Eliminations[0]; q.User != f.quitter || q.Week != 2 {
				t.Fatalf("eliminations %+v, want the quitter eliminated in week 2 first", want.Eliminations)
			}
			f.tamper()
			if err := f.recompute(f.locked(tt.last).Add(time.Hour), nil); err != nil {
				t.Fatal(err)
			}
			if got := f.judged(); !reflect.DeepEqual(got, want) {
				t.Fatalf("recompute judged\n%+v\nwant the judgement of the worker\n%+v", got, want)
			}
		})
	}
}

// TestRecomputeAfterAChange: the change of an activity in a locked week,
// made in the transaction of the recompute, is judged by it; a change rolled
// back leaves the former judgement.
func TestRecomputeAfterAChange(t *testing.T) {
	f := newRecomputeFixture(t)
	f.worker(4)
	before := f.judged()
	shorten := func(tx *gorm.DB) error {
		// b runs 6999 m in week 2 instead of 7000: eliminated in week 2.
		return tx.Exec(`UPDATE activities SET distance_meters = 6999 WHERE id = ?`, f.runOfBWk2).Error
	}

	t.Run("rolled back", func(t *testing.T) {
		boom := errors.New("the change failed")
		err := f.recompute(f.locked(4).Add(time.Hour), shorten, boom)
		if !errors.Is(err, boom) {
			t.Fatalf("got %v, want %v", err, boom)
		}
		if got := f.judged(); !reflect.DeepEqual(got, before) {
			t.Fatalf("judged %+v, want the former judgement", got)
		}
	})

	t.Run("committed", func(t *testing.T) {
		if err := f.recompute(f.locked(4).Add(time.Hour), shorten); err != nil {
			t.Fatal(err)
		}
		got := f.judged()
		if !containsElimination(got.Eliminations, recordedElimination{User: f.b, Week: 2, Run: 6999, Min: 7000}) || len(got.Eliminations) != 3 {
			t.Fatalf("eliminations = %+v, want b eliminated in week 2, the quitter and c as before", got.Eliminations)
		}
		if !containsElimination(got.Eliminations, before.Eliminations[0]) {
			t.Fatalf("eliminations = %+v, want the quitter still eliminated in week 2, judged before leaving", got.Eliminations)
		}
		if reflect.DeepEqual(got.Payloads, before.Payloads) {
			t.Fatal("frozen results unchanged, want them frozen again with the change")
		}
	})
}

// TestRecomputeOfAChallengeInProgressAgain: a challenge the change makes in
// progress again (a week longer) loses its frozen results, and the worker
// freezes them at its new end only.
func TestRecomputeOfAChallengeInProgressAgain(t *testing.T) {
	f := newRecomputeFixture(t)
	f.worker(4)
	err := f.recompute(f.locked(4).Add(time.Hour), func(tx *gorm.DB) error {
		return tx.Exec(`UPDATE challenges SET ends_at = ends_at + interval '7 days' WHERE id = ?`, f.id).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	got := f.judged()
	if len(got.Payloads) != 0 || !reflect.DeepEqual(got.Weeks, []int{1, 2, 3, 4}) {
		t.Fatalf("judged %+v, want no frozen results and weeks 1 to 4 closed", got)
	}
	// The new last week (5) locks a week later.
	if _, err := f.service(f.locked(4).Add(2 * time.Hour)).FinalizeChallenges(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := f.judged(); len(got.Payloads) != 0 {
		t.Fatalf("frozen before the new end: %+v", got.Payloads)
	}
	if _, err := f.service(f.locked(5)).FinalizeChallenges(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := f.judged(); len(got.Payloads) != 4 {
		t.Fatalf("snapshots %+v, want them frozen at the new end", got.Payloads)
	}
}

func TestRecomputeOutsideATransaction(t *testing.T) {
	f := newRecomputeFixture(t)
	f.worker(2)
	before := f.judged()
	if err := f.service(f.locked(2)).Recompute(t.Context(), f.id); err == nil {
		t.Fatal("recomputed outside a transaction, want an error")
	}
	if got := f.judged(); !reflect.DeepEqual(got, before) {
		t.Fatalf("judged %+v, want nothing changed", got)
	}
}

func TestRecomputeOfADeletedChallenge(t *testing.T) {
	f := newRecomputeFixture(t)
	f.exec(`UPDATE challenges SET deleted_at = $2 WHERE id = $1`, f.id, f.locked(1))
	if err := f.recompute(f.locked(2), nil); err == nil {
		t.Fatal("recomputed a deleted challenge, want challenge_not_found")
	}
}

// pausedData blocks the first read of the activities until release is
// closed, signalling paused once it is there: the run then holds the week
// lock shared, in the transaction of its challenge.
type pausedData struct {
	data    Activities
	paused  chan struct{}
	release chan struct{}
	first   bool
}

func (p *pausedData) Activities(ctx context.Context, challengeID string, w scoring.Window, weeklySport string, minSessionSeconds int, elevation bool) ([]scoring.Activity, error) {
	if !p.first {
		p.first = true
		close(p.paused)
		<-p.release
	}
	return p.data.Activities(ctx, challengeID, w, weeklySport, minSessionSeconds, elevation)
}

func (p *pausedData) Participants(ctx context.Context, challengeID string) ([]scoring.Participant, error) {
	return p.data.Participants(ctx, challengeID)
}

// TestRecomputeAndTheWorkerDoNotInterleave: a run of the worker on a
// challenge and a recompute of it, at the same time, take turns: whichever
// holds the week lock first ends before the other starts, so the last one
// judges what the first one committed.
func TestRecomputeAndTheWorkerDoNotInterleave(t *testing.T) {
	shorten := func(f recomputeFixture) func(tx *gorm.DB) error {
		return func(tx *gorm.DB) error {
			return tx.Exec(`UPDATE activities SET distance_meters = 6999 WHERE id = ?`, f.runOfBWk2).Error
		}
	}
	wantB := func(f recomputeFixture) recordedElimination {
		return recordedElimination{User: f.b, Week: 2, Run: 6999, Min: 7000}
	}

	t.Run("a run that starts during a recompute waits for it", func(t *testing.T) {
		f := newRecomputeFixture(t)
		f.worker(1)
		changed, commit, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() {
			done <- f.recompute(f.locked(2), func(tx *gorm.DB) error {
				if err := shorten(f)(tx); err != nil {
					return err
				}
				close(changed)
				<-commit
				return nil
			})
		}()
		<-changed
		ran := make(chan error, 1)
		go func() {
			_, err := f.service(f.locked(2)).CloseWeeks(t.Context())
			ran <- err
		}()
		f.awaitWaiter(t)
		close(commit)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if err := <-ran; err != nil {
			t.Fatal(err)
		}
		// The run found week 2 closed by the recompute, with the change.
		if got := f.eliminations(f.id); len(got) != 2 || !containsElimination(got, wantB(f)) {
			t.Fatalf("eliminations = %+v, want %+v and the quitter", got, wantB(f))
		}
	})

	t.Run("a recompute that starts during a run waits for it", func(t *testing.T) {
		f := newRecomputeFixture(t)
		f.worker(1)
		data := &pausedData{data: leaderboard.NewRepository(f.gdb), paused: make(chan struct{}), release: make(chan struct{})}
		ran := make(chan error, 1)
		go func() {
			// Judges week 2 from the activities before the change.
			_, err := f.serviceOn(data, f.locked(2)).CloseWeeks(t.Context())
			ran <- err
		}()
		<-data.paused
		done := make(chan error, 1)
		go func() { done <- f.recompute(f.locked(2), shorten(f)) }()
		f.awaitWaiter(t)
		select {
		case err := <-done:
			t.Fatalf("recompute ended during the run: %v", err)
		default:
		}
		close(data.release)
		if err := <-ran; err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		// The recompute came after the run: week 2 judged again with the change.
		if got := f.eliminations(f.id); len(got) != 2 || !containsElimination(got, wantB(f)) {
			t.Fatalf("eliminations = %+v, want %+v and the quitter", got, wantB(f))
		}
	})
}

func containsElimination(es []recordedElimination, want recordedElimination) bool {
	for _, e := range es {
		if e == want {
			return true
		}
	}
	return false
}
