package activity

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

// author is the account whose activities the admin changes.
const author = "6c1d2e3f-4a5b-4c6d-8e7f-001122334455"

var admin = auth.Actor{ID: actor, Admin: true}

// adminService is a service for the admin use cases on the activities of
// author, whose participations are challenges.
func adminService(t *testing.T, repo *fakeRepository, profileComplete bool, challenges ...Participation) *Service {
	t.Helper()
	ps := fakeParticipations{t: t, user: author, challenges: challenges}
	return NewService(repo, fakeProfiles{complete: profileComplete}, catalogue, ps, nil, clock.NewFake(recordNow))
}

func TestAdminRecord(t *testing.T) {
	c1 := challengeOf(t, "c1")
	other := participation(t, [3]int{2026, 4, 27}, 2) // finished on 2026-05-11
	other.ChallengeID = "finished"
	tests := []struct {
		name  string
		draft Draft
		want  []string
	}{
		// Week 1 of c1 locked on 2026-06-08: refused to its author.
		{name: "in a locked week", draft: run(toronto(t, 2026, 6, 3, 7)), want: []string{"c1"}},
		{name: "after the end of a challenge", draft: run(toronto(t, 2026, 5, 2, 7)), want: []string{"finished"}},
		{name: "in an open week", draft: run(toronto(t, 2026, 6, 10, 7)), want: []string{"c1"}},
		{name: "counts nowhere", draft: run(toronto(t, 2026, 3, 2, 7))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{}
			w, err := adminService(t, repo, true, c1, other).AdminRecord(t.Context(), admin, author, nil, tt.draft)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(w.Touched, tt.want) || !slices.Equal(repo.locked, []string{author}) {
				t.Fatalf("touched %v, locked %v; want %v, the author locked", w.Touched, repo.locked, tt.want)
			}
			if repo.recorded != nil {
				t.Fatal("recorded before Write")
			}
			got, err := w.Write(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			r := repo.recorded
			if r == nil || r.UserID != author || r.Source != SourceManual || !r.StartedAt.Equal(tt.draft.StartedAt) || r.IdempotencyKey != nil {
				t.Fatalf("recorded %+v, want the draft for the author, manual", r)
			}
			if got.ActivityID != r.ID || got.Old != nil || got.New["started_at"] != tt.draft.StartedAt.UTC().Format(time.RFC3339Nano) || got.New["distance_meters"] != 5000 {
				t.Fatalf("change = %+v, want the new activity's fields", got)
			}
		})
	}
}

func TestAdminRecordRefused(t *testing.T) {
	long := run(toronto(t, 2026, 6, 10, 7))
	long.DurationSeconds = maxDurationSeconds + 1
	yoga := Draft{SportCode: "yoga", StartedAt: toronto(t, 2026, 6, 10, 7), DurationSeconds: 600, DistanceMeters: new(int)}
	tests := []struct {
		name     string
		actor    auth.Actor
		draft    Draft
		complete bool
		lockErr  error
		code     apperr.Code
		wantErr  error
	}{
		{name: "not an admin", actor: auth.Actor{ID: actor}, draft: run(toronto(t, 2026, 6, 10, 7)), complete: true, code: apperr.CodeNotFound},
		{name: "not an admin, a deleted account: no hint it is deleted", actor: auth.Actor{ID: actor}, draft: run(toronto(t, 2026, 6, 10, 7)), lockErr: errAccountDeleted, code: apperr.CodeNotFound},
		{name: "invalid duration: validation unchanged", actor: admin, draft: long, complete: true, code: apperr.CodeValidationFailed},
		{name: "distance for a sport that takes none", actor: admin, draft: yoga, complete: true, code: apperr.CodeValidationFailed},
		{name: "in the future", actor: admin, draft: run(recordNow.Add(time.Hour)), complete: true, code: apperr.CodeValidationFailed},
		{name: "unknown or deleted account", actor: admin, draft: run(toronto(t, 2026, 6, 10, 7)), complete: true, lockErr: errAccountDeleted, code: apperr.CodeUserNotFound},
		{name: "profile incomplete", actor: admin, draft: run(toronto(t, 2026, 6, 10, 7)), code: apperr.CodeProfileIncomplete},
		{name: "lock failure", actor: admin, draft: run(toronto(t, 2026, 6, 10, 7)), complete: true, lockErr: errBoom, wantErr: errBoom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{lockErr: tt.lockErr}
			_, err := adminService(t, repo, tt.complete, challengeOf(t, "c1")).AdminRecord(t.Context(), tt.actor, author, nil, tt.draft)
			checkError(t, err, tt.code, tt.wantErr)
			if repo.recorded != nil {
				t.Fatal("recorded")
			}
		})
	}
}

func TestAdminRecordOverlap(t *testing.T) {
	repo := &fakeRepository{err: errActivityOverlap}
	w, err := adminService(t, repo, true).AdminRecord(t.Context(), admin, author, nil, run(toronto(t, 2026, 6, 10, 7)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.Write(t.Context())
	checkError(t, err, apperr.CodeActivityOverlap, nil)
}

func TestAdminRecordIdempotencyKey(t *testing.T) {
	key := "k1"
	before := Activity{ID: "a0", UserID: author, SportCode: "run", IdempotencyKey: &key}
	tests := []struct {
		name     string
		repo     *fakeRepository
		recorded bool
		code     apperr.Code
		wantErr  error
	}{
		{name: "new key: recorded with it", repo: &fakeRepository{}, recorded: true},
		{name: "same key, another body: recorded before", repo: &fakeRepository{existing: &before}, wantErr: ErrRecordedBefore},
		{name: "same key, activity deleted since", repo: &fakeRepository{err: errActivityNotFound}, code: apperr.CodeActivityNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := adminService(t, tt.repo, true, challengeOf(t, "c1")).AdminRecord(t.Context(), admin, author, &key, run(toronto(t, 2026, 6, 10, 7)))
			if err != nil {
				t.Fatal(err)
			}
			_, err = w.Write(t.Context())
			if !tt.recorded {
				checkError(t, err, tt.code, tt.wantErr)
				if tt.repo.recorded != nil {
					t.Fatal("recorded")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if r := tt.repo.recorded; r == nil || r.IdempotencyKey == nil || *r.IdempotencyKey != key || r.UserID != author {
				t.Fatalf("recorded %+v, want the key for the author", r)
			}
		})
	}
}

func TestAdminUpdate(t *testing.T) {
	june := challengeOf(t, "june")
	july := participation(t, [3]int{2026, 7, 6}, 2)
	july.ChallengeID = "july"
	// In week 1 of june, locked on 2026-06-08.
	locked := toronto(t, 2026, 6, 3, 7)
	current := Activity{ID: "a1", UserID: author, SportCode: "run", StartedAt: locked, DurationSeconds: 1500, DistanceMeters: ptr(5000), Source: SourceManual}

	tests := []struct {
		name    string
		draft   Draft
		touched []string
		old     map[string]any
		changed map[string]any
	}{
		{
			name: "in a locked week", draft: withDistance(run(locked), 6000), touched: []string{"june"},
			old: map[string]any{"distance_meters": 5000}, changed: map[string]any{"distance_meters": 6000},
		},
		{
			name: "moved out of the challenge: touched before the move", draft: run(toronto(t, 2026, 5, 20, 7)), touched: []string{"june"},
			old:     map[string]any{"started_at": "2026-06-03T11:00:00Z"},
			changed: map[string]any{"started_at": "2026-05-20T11:00:00Z"},
		},
		{name: "nothing changed", draft: run(locked), touched: []string{"june"}, old: map[string]any{}, changed: map[string]any{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{stored: map[string]Activity{"a1": current}}
			w, err := adminService(t, repo, false, june, july).AdminUpdate(t.Context(), admin, "a1", tt.draft)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(w.Touched, tt.touched) || !slices.Equal(repo.locked, []string{author}) {
				t.Fatalf("touched %v, locked %v; want %v, the author locked", w.Touched, repo.locked, tt.touched)
			}
			got, err := w.Write(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if repo.updated == nil || got.ActivityID != "a1" || !reflect.DeepEqual(got.Old, tt.old) || !reflect.DeepEqual(got.New, tt.changed) {
				t.Fatalf("change = %+v (updated %+v), want old %v, new %v", got, repo.updated, tt.old, tt.changed)
			}
		})
	}
}

func TestAdminUpdateMovedBetweenChallenges(t *testing.T) {
	june := challengeOf(t, "june")
	july := participation(t, [3]int{2026, 7, 6}, 2)
	july.ChallengeID = "july"
	// The clock after july: both its weeks locked.
	repo := &fakeRepository{stored: map[string]Activity{"a1": {ID: "a1", UserID: author, SportCode: "run", StartedAt: toronto(t, 2026, 6, 3, 7), DurationSeconds: 1500, DistanceMeters: ptr(5000)}}}
	svc := NewService(repo, fakeProfiles{}, catalogue, fakeParticipations{t: t, user: author, challenges: []Participation{july, june}}, nil, clock.NewFake(toronto(t, 2026, 8, 1, 12)))
	w, err := svc.AdminUpdate(t.Context(), admin, "a1", run(toronto(t, 2026, 7, 8, 7)))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(w.Touched, []string{"july", "june"}) {
		t.Fatalf("touched %v, want both challenges", w.Touched)
	}
}

func TestAdminDelete(t *testing.T) {
	june := challengeOf(t, "june")
	current := Activity{ID: "a1", UserID: author, SportCode: "run", StartedAt: toronto(t, 2026, 6, 3, 7), DurationSeconds: 1500, DistanceMeters: ptr(5000)}
	repo := &fakeRepository{stored: map[string]Activity{"a1": current}}
	w, err := adminService(t, repo, false, june).AdminDelete(t.Context(), admin, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(w.Touched, []string{"june"}) {
		t.Fatalf("touched %v, want june", w.Touched)
	}
	got, err := w.Write(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if repo.deleted != "a1" || !repo.deletedAt.Equal(recordNow) || got.New != nil || got.Old["duration_seconds"] != 1500 {
		t.Fatalf("change = %+v, deleted %q at %v; want a1 deleted now, its old fields", got, repo.deleted, repo.deletedAt)
	}
}

func TestAdminChangeRefused(t *testing.T) {
	current := Activity{ID: "a1", UserID: author, SportCode: "run", StartedAt: toronto(t, 2026, 6, 3, 7), DurationSeconds: 1500, DistanceMeters: ptr(5000)}
	invalid := run(toronto(t, 2026, 6, 3, 7))
	invalid.DurationSeconds = 0
	tests := []struct {
		name    string
		actor   auth.Actor
		id      string
		draft   Draft
		lockErr error
		code    apperr.Code
	}{
		{name: "not an admin", actor: auth.Actor{ID: author}, id: "a1", draft: run(current.StartedAt), code: apperr.CodeNotFound},
		{name: "not an admin, a missing activity: no hint it is missing", actor: auth.Actor{ID: author}, id: "a2", draft: run(current.StartedAt), code: apperr.CodeNotFound},
		{name: "missing or deleted activity", actor: admin, id: "a2", draft: run(current.StartedAt), code: apperr.CodeActivityNotFound},
		{name: "deleted account", actor: admin, id: "a1", draft: run(current.StartedAt), lockErr: errAccountDeleted, code: apperr.CodeActivityNotFound},
		{name: "invalid draft: validation unchanged", actor: admin, id: "a1", draft: invalid, code: apperr.CodeValidationFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{stored: map[string]Activity{"a1": current}, lockErr: tt.lockErr}
			svc := adminService(t, repo, false, challengeOf(t, "c1"))
			_, err := svc.AdminUpdate(t.Context(), tt.actor, tt.id, tt.draft)
			checkError(t, err, tt.code, nil)
			if tt.code == apperr.CodeValidationFailed {
				return
			}
			_, err = svc.AdminDelete(t.Context(), tt.actor, tt.id)
			checkError(t, err, tt.code, nil)
			if repo.updated != nil || repo.deleted != "" {
				t.Fatal("written")
			}
		})
	}
}

var errBoom = errors.New("boom")

func withDistance(d Draft, meters int) Draft {
	d.DistanceMeters = &meters
	return d
}
