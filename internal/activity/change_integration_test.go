//go:build integration

package activity

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

// stored is a row of activities as Update and Delete leave it.
type stored struct {
	sportCode string
	startedAt time.Time
	distance  *int
	source    string
	key       *string
	deletedAt *time.Time
}

func (f fixture) stored(id string) stored {
	f.t.Helper()
	var s stored
	err := f.pool.QueryRow(f.t.Context(), "SELECT sport_code, started_at, distance_meters, source, idempotency_key, deleted_at FROM activities WHERE id = $1", id).
		Scan(&s.sportCode, &s.startedAt, &s.distance, &s.source, &s.key, &s.deletedAt)
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

// recorded records a run of the user in week 2 of challengeOf, with a key.
func (f fixture) recorded(svc *Service) Recorded {
	f.t.Helper()
	key := "first-key"
	rec, err := svc.Record(f.t.Context(), f.user, &key, run(toronto(f.t, 2026, 6, 10, 7)))
	if err != nil {
		f.t.Fatal(err)
	}
	return rec
}

func wantCode(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("error = %v, want %s", err, code)
	}
}

func TestUpdateStoresTheActivity(t *testing.T) {
	f := setupRecord(t)
	svc := f.service(challengeOf(t, "c1"))
	rec := f.recorded(svc)

	moved := toronto(t, 2026, 6, 11, 6)
	got, err := svc.Update(t.Context(), f.user, rec.ID, Draft{SportCode: "yoga", StartedAt: moved, DurationSeconds: 3600})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != rec.ID || got.Source != SourceManual || got.IdempotencyKey == nil || len(got.Contributions) != 1 {
		t.Fatalf("Update() = %+v, want the activity, manual, its key, counting in c1", got)
	}
	s := f.stored(rec.ID)
	if s.sportCode != "yoga" || !s.startedAt.Equal(moved) || s.distance != nil || s.source != SourceManual || s.key == nil || *s.key != "first-key" || s.deletedAt != nil {
		t.Fatalf("stored %+v, want yoga at the new start, no distance, the source and the key kept", s)
	}
}

func TestDeleteMarksTheActivity(t *testing.T) {
	f := setupRecord(t)
	svc := f.service(challengeOf(t, "c1"))
	rec := f.recorded(svc)

	if err := svc.Delete(t.Context(), f.user, rec.ID); err != nil {
		t.Fatal(err)
	}
	if s := f.stored(rec.ID); s.deletedAt == nil || !s.deletedAt.Equal(recordNow) {
		t.Fatalf("deleted_at %v, want the clock's now", s.deletedAt)
	}
	// Its key stays taken: a creation sent again records nothing.
	key := "first-key"
	_, err := svc.Record(t.Context(), f.user, &key, run(toronto(t, 2026, 6, 10, 7)))
	wantCode(t, err, apperr.CodeActivityNotFound)
	if f.count() != 1 {
		t.Fatalf("%d rows, want 1", f.count())
	}

	// Deleted: gone for every change.
	wantCode(t, svc.Delete(t.Context(), f.user, rec.ID), apperr.CodeActivityNotFound)
	_, err = svc.Update(t.Context(), f.user, rec.ID, run(toronto(t, 2026, 6, 10, 9)))
	wantCode(t, err, apperr.CodeActivityNotFound)
}

func TestChangeActivityOfAnotherUser(t *testing.T) {
	f := setupRecord(t)
	svc := f.service(challengeOf(t, "c1"))
	rec := f.recorded(svc)
	var other string
	if err := f.pool.QueryRow(t.Context(), "INSERT INTO users (auth_subject, pseudo) VALUES ('email|other', 'other') RETURNING id").Scan(&other); err != nil {
		t.Fatal(err)
	}

	_, err := svc.Update(t.Context(), other, rec.ID, run(toronto(t, 2026, 6, 10, 9)))
	wantCode(t, err, apperr.CodeActivityNotFound)
	wantCode(t, svc.Delete(t.Context(), other, rec.ID), apperr.CodeActivityNotFound)
	if s := f.stored(rec.ID); !s.startedAt.Equal(rec.StartedAt) || s.deletedAt != nil {
		t.Fatalf("stored %+v, want it unchanged", s)
	}
}

func TestChangeActivityWeekLocked(t *testing.T) {
	f := setupRecord(t)
	svc := f.service(challengeOf(t, "c1"))
	rec := f.recorded(svc)

	// Into week 1, ended: refused in the transaction, nothing written.
	_, err := svc.Update(t.Context(), f.user, rec.ID, run(toronto(t, 2026, 6, 3, 7)))
	wantCode(t, err, apperr.CodeWeekLocked)
	if s := f.stored(rec.ID); !s.startedAt.Equal(rec.StartedAt) {
		t.Fatalf("started_at %s, want it unchanged", s.startedAt)
	}

	// Week 2 ends on Monday 2026-06-15 and locks at 7 am that day: from then
	// on, nothing changes.
	at := func(now time.Time) *Service {
		return NewService(NewRepository(f.gdb), fakeProfiles{complete: true}, catalogue, staticParticipations{challengeOf(t, "c1")}, nil, clock.NewFake(now))
	}
	locked := at(toronto(t, 2026, 6, 15, 7))
	_, err = locked.Update(t.Context(), f.user, rec.ID, run(toronto(t, 2026, 6, 10, 9)))
	wantCode(t, err, apperr.CodeWeekLocked)
	wantCode(t, locked.Delete(t.Context(), f.user, rec.ID), apperr.CodeWeekLocked)
	if s := f.stored(rec.ID); s.deletedAt != nil || !s.startedAt.Equal(rec.StartedAt) {
		t.Fatalf("stored %+v, want it unchanged", s)
	}

	// Until 7 am, after the end of the week, it can still be changed (into a
	// run across the end of the week) and deleted.
	open := at(toronto(t, 2026, 6, 15, 7).Add(-time.Minute))
	across := toronto(t, 2026, 6, 15, 0).Add(-15 * time.Minute)
	if _, err := open.Update(t.Context(), f.user, rec.ID, run(across)); err != nil {
		t.Fatalf("update before 7 am: %v", err)
	}
	if s := f.stored(rec.ID); !s.startedAt.Equal(across) {
		t.Fatalf("started_at %s, want %s", s.startedAt, across)
	}
	if err := open.Delete(t.Context(), f.user, rec.ID); err != nil {
		t.Fatalf("delete before 7 am: %v", err)
	}
	if s := f.stored(rec.ID); s.deletedAt == nil {
		t.Fatalf("stored %+v, want it deleted", s)
	}
}

func TestChangeActivityAccountDeleted(t *testing.T) {
	f := setupRecord(t)
	svc := f.service()
	rec := f.recorded(svc)
	if _, err := f.pool.Exec(t.Context(), "UPDATE users SET anonymized_at = now() WHERE id = $1", f.user); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Update(t.Context(), f.user, rec.ID, run(toronto(t, 2026, 6, 10, 9)))
	wantCode(t, err, apperr.CodeAccountDeleted)
	wantCode(t, svc.Delete(t.Context(), f.user, rec.ID), apperr.CodeAccountDeleted)
}

// TestUpdateReplacesTheRepetitions: a CrossFit session entered before the
// user joins a burpees challenge gets its burpees by a change; once no
// challenge offers them any more (left, changed by an admin), a change that
// sends them back keeps them, one that adds wall balls is refused, one that
// leaves them out removes them. A locked week, or the activity of someone
// else, changes nothing.
func TestUpdateReplacesTheRepetitions(t *testing.T) {
	f := setupRecord(t)
	burpees := challengeOf(t, "c1")
	burpees.Reference, burpees.Exercise = "crossfit", "burpee"
	session := func(day int, reps ...Repetitions) Draft {
		return Draft{SportCode: "crossfit", StartedAt: toronto(t, 2026, 6, day, 7), DurationSeconds: 1800, Repetitions: reps}
	}
	rec, err := f.service().Record(t.Context(), f.user, nil, session(10))
	if err != nil {
		t.Fatal(err)
	}
	id, thirty := rec.ID, []Repetitions{{"burpee", 30}}
	reps := func() []Repetitions {
		t.Helper()
		page, err := f.service().Mine(t.Context(), f.user, Filter{}, 10, nil)
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("list %+v, %v, want the session", page, err)
		}
		return page.Items[0].Repetitions
	}
	if got, err := f.service(burpees).Update(t.Context(), f.user, id, session(10, thirty...)); err != nil || !slices.Equal(got.Repetitions, thirty) || !slices.Equal(reps(), thirty) {
		t.Fatalf("joined: %+v, %v, listed %+v, want %+v", got.Repetitions, err, reps(), thirty)
	}

	_, err = f.service(burpees).Update(t.Context(), f.user, id, session(3, thirty...))
	wantCode(t, err, apperr.CodeWeekLocked)
	var other string
	if err := f.pool.QueryRow(t.Context(), "INSERT INTO users (auth_subject, pseudo) VALUES ('email|other', 'other') RETURNING id").Scan(&other); err != nil {
		t.Fatal(err)
	}
	_, err = f.service(burpees).Update(t.Context(), other, id, session(10))
	wantCode(t, err, apperr.CodeActivityNotFound)

	if got, err := f.service().Update(t.Context(), f.user, id, session(11, thirty...)); err != nil || !slices.Equal(got.Repetitions, thirty) {
		t.Fatalf("offered no more, sent back: %+v, %v, want them kept", got.Repetitions, err)
	}
	_, err = f.service().Update(t.Context(), f.user, id, session(11, Repetitions{"burpee", 30}, Repetitions{"wall_ball", 5}))
	wantCode(t, err, apperr.CodeUnknownExercise)
	if !slices.Equal(reps(), thirty) {
		t.Fatalf("after the refusals: %+v, want %+v", reps(), thirty)
	}
	if got, err := f.service().Update(t.Context(), f.user, id, session(11)); err != nil || got.Repetitions != nil || reps() != nil {
		t.Fatalf("left out: %+v, %v, listed %+v, want none", got.Repetitions, err, reps())
	}
}
