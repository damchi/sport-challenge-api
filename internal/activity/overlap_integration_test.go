//go:build integration

package activity

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
)

// runFor is a run of the fixture's user starting at startedAt, lasting minutes.
func runFor(startedAt time.Time, minutes int) Draft {
	d := run(startedAt)
	d.DurationSeconds = minutes * 60
	return d
}

// TestRecordRefusesAnOverlap follows the rule of README §2.5 (SC-API-084):
// two activities of a user never overlap, [start, end) each; one after the
// other (a triathlon) is fine, a deleted activity no longer counts.
func TestRecordRefusesAnOverlap(t *testing.T) {
	f := setupRecord(t)
	svc := f.service()
	at := func(h, m int) time.Time { return toronto(t, 2026, 6, 10, h).Add(time.Duration(m) * time.Minute) }
	first, err := svc.Record(t.Context(), f.user, nil, runFor(at(7, 0), 25)) // [07:00, 07:25)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		draft Draft
		want  apperr.Code // empty: recorded
	}{
		{name: "starts inside", draft: runFor(at(7, 10), 30), want: apperr.CodeActivityOverlap},
		{name: "ends inside", draft: runFor(at(6, 50), 15), want: apperr.CodeActivityOverlap},
		{name: "holds it", draft: runFor(at(6, 50), 60), want: apperr.CodeActivityOverlap},
		{name: "same interval", draft: runFor(at(7, 0), 25), want: apperr.CodeActivityOverlap},
		{name: "starts when it ends: the next one", draft: runFor(at(7, 25), 20)},
		{name: "ends when it starts: the one before", draft: runFor(at(6, 35), 25)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.Record(t.Context(), f.user, nil, tt.draft)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Record() = %v, want recorded", err)
				}
				return
			}
			wantCode(t, err, tt.want)
		})
	}
	if err := svc.Delete(t.Context(), f.user, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Record(t.Context(), f.user, nil, runFor(at(7, 5), 10)); err != nil {
		t.Fatalf("over a deleted activity: %v, want recorded", err)
	}
}

// TestUpdateRefusesAnOverlap: an activity replaced over its own interval is
// fine; over another activity of the user, refused.
func TestUpdateRefusesAnOverlap(t *testing.T) {
	f := setupRecord(t)
	svc := f.service()
	at := func(h, m int) time.Time { return toronto(t, 2026, 6, 10, h).Add(time.Duration(m) * time.Minute) }
	a, err := svc.Record(t.Context(), f.user, nil, runFor(at(7, 0), 25))
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.Record(t.Context(), f.user, nil, runFor(at(8, 0), 25))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Update(t.Context(), f.user, a.ID, runFor(at(7, 5), 30)); err != nil {
		t.Fatalf("over its own interval: %v, want replaced", err)
	}
	_, err = svc.Update(t.Context(), f.user, b.ID, runFor(at(7, 20), 30))
	wantCode(t, err, apperr.CodeActivityOverlap)
	// Right after a (which now ends at 07:35): the next one, not an overlap.
	if _, err := svc.Update(t.Context(), f.user, b.ID, runFor(at(7, 35), 25)); err != nil {
		t.Fatalf("right after another: %v, want replaced", err)
	}
}

// TestRecordReplayIsNotAnOverlap: a request sent again with its idempotency
// key answers the activity recorded first, never activity_overlap, though its
// interval is that activity's own.
func TestRecordReplayIsNotAnOverlap(t *testing.T) {
	f := setupRecord(t)
	svc := f.service()
	key := "replay-key"
	draft := runFor(toronto(t, 2026, 6, 10, 7), 25)
	first, err := svc.Record(t.Context(), f.user, &key, draft)
	if err != nil {
		t.Fatal(err)
	}
	again, err := svc.Record(t.Context(), f.user, &key, draft)
	if err != nil || again.Created || again.ID != first.ID {
		t.Fatalf("replay = %+v, %v, want the first activity", again, err)
	}
}

// TestRecordOverlapConcurrently: two writes of the same interval at once,
// without idempotency key. Each one, inside its transaction, waits up to
// 300 ms for the other to reach the same point before its overlap check:
// without the lock of the user's activities both would get there and both be
// stored. With it, the second waits for the first to commit, then is refused.
func TestRecordOverlapConcurrently(t *testing.T) {
	f := setupRecord(t)
	repo := NewRepository(f.gdb)
	a := Activity{UserID: f.user, SportCode: "run", StartedAt: toronto(t, 2026, 6, 10, 7), DurationSeconds: 1500, DistanceMeters: ptr(5000), Source: SourceManual}
	arrived := make(chan struct{}, 2)
	decide := func(context.Context) error {
		arrived <- struct{}{}
		deadline := time.Now().Add(300 * time.Millisecond)
		for len(arrived) < 2 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		return nil
	}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Go(func() { _, _, errs[i] = repo.Record(t.Context(), a, decide) })
	}
	wg.Wait()
	var recorded, refused int
	for _, err := range errs {
		switch {
		case err == nil:
			recorded++
		case errors.Is(err, errActivityOverlap):
			refused++
		default:
			t.Fatalf("Record() = %v, want recorded or errActivityOverlap", err)
		}
	}
	if recorded != 1 || refused != 1 || f.count() != 1 {
		t.Fatalf("%d recorded, %d refused, %d stored, want one of each and one stored", recorded, refused, f.count())
	}
}

// TestUpdateKeepingItsIntervalIsNotChecked: an activity that already overlaps
// another (recorded before the rule) keeps its interval when only its
// distance changes: no new overlap, the update goes through. Moving it is
// checked; so is the end, never in the future.
func TestUpdateKeepingItsIntervalIsNotChecked(t *testing.T) {
	f := setupRecord(t)
	svc := f.service()
	start := toronto(t, 2026, 6, 10, 7)
	a, err := svc.Record(t.Context(), f.user, nil, runFor(start, 25))
	if err != nil {
		t.Fatal(err)
	}
	// An older overlap, written as the data could be before this rule.
	var b string
	if err := f.pool.QueryRow(t.Context(), `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters, source)
		VALUES ($1, 'run', $2, 1500, 5000, 'manual') RETURNING id`, f.user, start.Add(10*time.Minute)).Scan(&b); err != nil {
		t.Fatal(err)
	}
	same := runFor(start.Add(10*time.Minute), 25)
	*same.DistanceMeters = 6000
	if _, err := svc.Update(t.Context(), f.user, b, same); err != nil {
		t.Fatalf("same interval, new distance: %v, want replaced", err)
	}
	_, err = svc.Update(t.Context(), f.user, b, runFor(start.Add(5*time.Minute), 25))
	wantCode(t, err, apperr.CodeActivityOverlap)
	_, err = svc.Update(t.Context(), f.user, a.ID, runFor(recordNow, 60))
	wantCode(t, err, apperr.CodeValidationFailed)
}
