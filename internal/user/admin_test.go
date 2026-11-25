package user

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

var (
	anAdmin    = auth.Actor{ID: "admin-1", Admin: true}
	notAnAdmin = auth.Actor{ID: "user-9"}
	adminNow   = time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC)
)

// wantCode fails unless err is the application error of code and status.
func wantCode(t *testing.T, err error, code apperr.Code, status int) {
	t.Helper()
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != code || appErr.Status != status {
		t.Fatalf("error = %v, want %d %s", err, status, code)
	}
}

func TestAdminRefusal(t *testing.T) {
	if err := adminRefusal(anAdmin); err != nil {
		t.Fatalf("adminRefusal(admin) = %v, want nil", err)
	}
	// Not an admin: the route answers as if it did not exist.
	wantCode(t, adminRefusal(notAnAdmin), apperr.CodeNotFound, http.StatusNotFound)
}

func TestBlockRefusal(t *testing.T) {
	tests := []struct {
		name     string
		targetID string
		wantErr  bool
	}{
		{name: "another account", targetID: "user-2"},
		{name: "their own account: forbidden", targetID: anAdmin.ID, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := blockRefusal(anAdmin, tt.targetID)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("blockRefusal() = %v, want nil", err)
				}
				return
			}
			wantCode(t, err, apperr.CodeForbidden, http.StatusForbidden)
		})
	}
}

func TestServiceAdminUpdateProfile(t *testing.T) {
	before := User{ID: "u-1", Pseudo: ptr("marie"), FirstName: ptr("Marie"), LastName: ptr("Curie"), Language: ptr("fr")}
	tests := []struct {
		name        string
		actor       auth.Actor
		changes     ProfileChanges
		after       User
		lockErr     error
		repoErr     error
		wantCode    apperr.Code
		wantStatus  int
		wantFields  string
		wantWritten bool
		want        AccountChange
	}{
		{
			name: "names changed, pseudo removed", actor: anAdmin,
			changes: ProfileChanges{RemovePseudo: true, FirstName: ptr("Maria"), LastName: ptr("Curie")},
			after:   User{ID: "u-1", FirstName: ptr("Maria"), LastName: ptr("Curie")}, wantWritten: true,
			// The last name did not change: it is not journaled.
			want: AccountChange{
				Old: map[string]any{"pseudo": "marie", "first_name": "Marie"},
				New: map[string]any{"pseudo": nil, "first_name": "Maria"},
			},
		},
		{name: "nothing changes", actor: anAdmin, after: before, wantWritten: true, want: noChange()},
		{name: "not an admin", actor: notAnAdmin, changes: ProfileChanges{FirstName: ptr("Maria")}, wantCode: apperr.CodeNotFound, wantStatus: http.StatusNotFound},
		{
			name: "invalid names, the rules of PATCH /me", actor: anAdmin, changes: ProfileChanges{FirstName: ptr("M4rie"), Pseudo: ptr("é")},
			wantCode: apperr.CodeValidationFailed, wantStatus: http.StatusBadRequest, wantFields: "first_name,pseudo",
		},
		{
			name: "pseudo taken", actor: anAdmin, changes: ProfileChanges{Pseudo: ptr("Pierre")}, repoErr: fmt.Errorf("update user: %w", errPseudoTaken),
			wantCode: apperr.CodeValidationFailed, wantStatus: http.StatusConflict, wantFields: "pseudo", wantWritten: true,
		},
		{name: "unknown or deleted account", actor: anAdmin, changes: ProfileChanges{FirstName: ptr("Maria")}, lockErr: fmt.Errorf("lock user: %w", errUserNotFound), wantCode: apperr.CodeUserNotFound, wantStatus: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var written *ProfileChanges
			changes := ProfileChanges{Pseudo: ptr("not-called")}
			after := tt.after
			repo := fakeRepository{user: before, updated: &after, lockErr: tt.lockErr, err: tt.repoErr, changes: &changes}
			// A language in the request is the user's: never written by an admin.
			input := tt.changes
			input.Language = ptr("en")

			got, err := NewService(repo, nil, nil, nil, clock.NewFake(adminNow), discard).AdminUpdateProfile(t.Context(), tt.actor, "u-1", input)

			if changes.Pseudo == nil || *changes.Pseudo != "not-called" {
				written = &changes
			}
			if (written != nil) != tt.wantWritten {
				t.Fatalf("repository written = %v, want %v", written != nil, tt.wantWritten)
			}
			if written != nil && written.Language != nil {
				t.Fatalf("language written %q, want it left to the user", *written.Language)
			}
			if tt.wantCode != "" {
				wantCode(t, err, tt.wantCode, tt.wantStatus)
				var appErr *apperr.Error
				if errors.As(err, &appErr); appErr.Params[apperr.FieldsParam] != tt.wantFields {
					t.Fatalf("fields %q, want %q", appErr.Params[apperr.FieldsParam], tt.wantFields)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("AdminUpdateProfile() = %+v, %v, want %+v", got, err, tt.want)
			}
		})
	}
}

func TestServiceAdminBlockAndUnblock(t *testing.T) {
	blockedAt := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		unblock    bool
		actor      auth.Actor
		target     string
		blockedAt  *time.Time
		lockErr    error
		wantCode   apperr.Code
		wantStatus int
		wantWrites []*time.Time
		want       AccountChange
	}{
		{
			name: "block", actor: anAdmin, target: "u-1", wantWrites: []*time.Time{&adminNow},
			want: AccountChange{Old: map[string]any{"blocked_at": nil}, New: map[string]any{"blocked_at": adminNow}},
		},
		// Blocked since the first time: nothing is written.
		{name: "block a blocked account", actor: anAdmin, target: "u-1", blockedAt: &blockedAt, want: noChange()},
		{name: "block their own account", actor: anAdmin, target: anAdmin.ID, wantCode: apperr.CodeForbidden, wantStatus: http.StatusForbidden},
		{name: "block, not an admin", actor: notAnAdmin, target: "u-1", wantCode: apperr.CodeNotFound, wantStatus: http.StatusNotFound},
		{name: "block an unknown account", actor: anAdmin, target: "u-1", lockErr: fmt.Errorf("lock user: %w", errUserNotFound), wantCode: apperr.CodeUserNotFound, wantStatus: http.StatusNotFound},
		{
			name: "unblock", unblock: true, actor: anAdmin, target: "u-1", blockedAt: &blockedAt, wantWrites: []*time.Time{nil},
			want: AccountChange{Old: map[string]any{"blocked_at": blockedAt}, New: map[string]any{"blocked_at": nil}},
		},
		{name: "unblock an account not blocked", unblock: true, actor: anAdmin, target: "u-1", want: noChange()},
		{name: "unblock, not an admin", unblock: true, actor: notAnAdmin, target: "u-1", blockedAt: &blockedAt, wantCode: apperr.CodeNotFound, wantStatus: http.StatusNotFound},
		{name: "unblock an unknown account", unblock: true, actor: anAdmin, target: "u-1", lockErr: fmt.Errorf("lock user: %w", errUserNotFound), wantCode: apperr.CodeUserNotFound, wantStatus: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var writes []*time.Time
			repo := fakeRepository{user: User{ID: tt.target, BlockedAt: tt.blockedAt}, lockErr: tt.lockErr, blocks: &writes}
			s := NewService(repo, nil, nil, nil, clock.NewFake(adminNow), discard)
			change := s.AdminBlock
			if tt.unblock {
				change = s.AdminUnblock
			}
			got, err := change(t.Context(), tt.actor, tt.target)
			if tt.wantCode != "" {
				wantCode(t, err, tt.wantCode, tt.wantStatus)
				if len(writes) > 0 {
					t.Fatalf("written %v, want nothing", writes)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("change = %+v, %v, want %+v", got, err, tt.want)
			}
			if !slices.EqualFunc(writes, tt.wantWrites, func(a, b *time.Time) bool { return (a == nil) == (b == nil) && (a == nil || a.Equal(*b)) }) {
				t.Fatalf("blocked_at written %v, want %v", writes, tt.wantWrites)
			}
		})
	}
}

func TestServiceAdminDelete(t *testing.T) {
	lastCreator := apperr.New(apperr.CodeLastCreator, nil)
	tests := []struct {
		name       string
		actor      auth.Actor
		deleteErr  error
		lockErr    error
		wantCode   apperr.Code
		wantStatus int
	}{
		{name: "deleted", actor: anAdmin},
		{name: "not an admin", actor: notAnAdmin, wantCode: apperr.CodeNotFound, wantStatus: http.StatusNotFound},
		{name: "unknown or deleted account", actor: anAdmin, deleteErr: fmt.Errorf("delete user: %w", errUserNotFound), wantCode: apperr.CodeUserNotFound, wantStatus: http.StatusNotFound},
		// The refusal of DELETE /me: the account is the last creator of a
		// challenge not finished.
		{name: "last creator", actor: anAdmin, lockErr: lastCreator, wantCode: apperr.CodeLastCreator, wantStatus: http.StatusUnprocessableEntity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var removed []string
			repo := fakeRepository{
				user: User{ID: "u-1", AuthSubject: "email|1"}, deleted: new(string), deletedAt: new(time.Time),
				deleteErr: tt.deleteErr, removed: &removed,
			}
			deletionLock := &fakeDeletionLock{err: tt.lockErr}
			var erased []string
			erasers := []Eraser{fakeEraser{name: "journal", deletionLock: deletionLock, calls: &erased}}
			identities := &fakeIdentities{}
			s := NewService(repo, deletionLock, erasers, identities, clock.NewFake(adminNow), discard)

			d, err := s.AdminDelete(t.Context(), tt.actor, "u-1")
			if tt.wantCode != "" {
				wantCode(t, err, tt.wantCode, tt.wantStatus)
				return
			}
			if err != nil || !d.At.Equal(adminNow) {
				t.Fatalf("AdminDelete() = %+v, %v, want the deletion at %v", d, err, adminNow)
			}
			// The path of DELETE /me: anonymized at the clock's now, locked,
			// then the erasers.
			if *repo.deleted != "u-1" || !repo.deletedAt.Equal(adminNow) || deletionLock.locked != "u-1" || !slices.Equal(erased, []string{"journal"}) {
				t.Fatalf("anonymized %q at %v, locked %q, erased %v", *repo.deleted, *repo.deletedAt, deletionLock.locked, erased)
			}
			// Nothing at Auth0 before the commit: CompleteDeletion does it.
			if identities.deleted != "" {
				t.Fatalf("deleted at Auth0 before the commit")
			}
			s.CompleteDeletion(t.Context(), d)
			if identities.deleted != "email|1" || !slices.Equal(removed, []string{"email|1"}) {
				t.Fatalf("deleted at Auth0 %q, removed %v, want the subject of the account", identities.deleted, removed)
			}
		})
	}
}

func TestServiceGetOrCreateBySubjectRefusesABlockedAccount(t *testing.T) {
	blockedAt := adminNow
	repo := fakeRepository{user: User{ID: "u-1", BlockedAt: &blockedAt}}
	_, err := NewService(repo, nil, nil, nil, nil, discard).GetOrCreateBySubject(t.Context(), "email|1", "")
	wantCode(t, err, apperr.CodeAccountBlocked, http.StatusForbidden)
}
