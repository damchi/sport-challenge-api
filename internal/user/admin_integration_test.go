//go:build integration

package user

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
)

// TestAdminAccountChangesFromTheDatabase: an admin changes the names of an
// account in the transaction of the change, blocks it (its next login
// answers account_blocked), unblocks it (served again), then deletes it;
// its identity goes at Auth0 once CompleteDeletion runs. A deleted account
// is not found.
func TestAdminAccountChangesFromTheDatabase(t *testing.T) {
	repo, pool := newRepository(t)
	gdb := repo.db
	now := time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC)
	identities := &fakeIdentities{}
	s := NewService(repo, &fakeDeletionLock{}, nil, identities, clock.NewFake(now), discard)
	admin := auth.Actor{ID: newUser(t, repo, "email|admin").ID, Admin: true}
	bob := namedUser(t, repo, "email|bob", "bob_1")
	namedUser(t, repo, "email|other", "taken")
	inTx := func(fn func(ctx context.Context) error) error { return db.InTx(t.Context(), gdb, fn) }

	var change AccountChange
	if err := inTx(func(ctx context.Context) (err error) {
		change, err = s.AdminUpdateProfile(ctx, admin, bob.ID, ProfileChanges{FirstName: ptr("Robert"), LastName: ptr("Martin")})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if change.Old["first_name"] != nil || change.New["first_name"] != "Robert" || change.New["last_name"] != "Martin" || len(change.Old) != 2 {
		t.Fatalf("change %+v, want the two names set", change)
	}
	got, err := repo.GetByID(t.Context(), bob.ID)
	if err != nil || *got.FirstName != "Robert" || *got.LastName != "Martin" || *got.Pseudo != "bob_1" {
		t.Fatalf("profile %+v, %v, want the names changed, the pseudo kept", got, err)
	}

	// A pseudo taken, regardless of case: 409, nothing written.
	err = inTx(func(ctx context.Context) error {
		_, err := s.AdminUpdateProfile(ctx, admin, bob.ID, ProfileChanges{Pseudo: ptr("TAKEN"), FirstName: ptr("Bobby")})
		return err
	})
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Status != 409 || appErr.Params[apperr.FieldsParam] != "pseudo" {
		t.Fatalf("error = %v, want 409 on the pseudo", err)
	}
	if got, _ := repo.GetByID(t.Context(), bob.ID); *got.FirstName != "Robert" {
		t.Fatalf("first name %q, want nothing written", *got.FirstName)
	}

	if err := inTx(func(ctx context.Context) error { _, err := s.AdminBlock(ctx, admin, bob.ID); return err }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetOrCreateBySubject(t.Context(), "email|bob", ""); !errors.Is(err, apperr.New(apperr.CodeAccountBlocked, nil)) {
		t.Fatalf("login of a blocked account: %v, want account_blocked", err)
	}
	var blockedAt *time.Time
	if err := pool.QueryRow(t.Context(), "SELECT blocked_at FROM users WHERE id = $1", bob.ID).Scan(&blockedAt); err != nil || blockedAt == nil || !blockedAt.Equal(now) {
		t.Fatalf("blocked at %v, %v, want the clock's %v", blockedAt, err, now)
	}
	// Never their own account.
	err = inTx(func(ctx context.Context) error { _, err := s.AdminBlock(ctx, admin, admin.ID); return err })
	if !errors.Is(err, apperr.New(apperr.CodeForbidden, nil)) {
		t.Fatalf("self block: %v, want forbidden", err)
	}

	if err := inTx(func(ctx context.Context) error { _, err := s.AdminUnblock(ctx, admin, bob.ID); return err }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetOrCreateBySubject(t.Context(), "email|bob", ""); err != nil {
		t.Fatalf("login once unblocked: %v", err)
	}

	var deletion Deletion
	if err := inTx(func(ctx context.Context) (err error) { deletion, err = s.AdminDelete(ctx, admin, bob.ID); return err }); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, "SELECT count(*) FROM users WHERE id = $1 AND anonymized_at = $2 AND first_name IS NULL", bob.ID, now); n != 1 {
		t.Fatal("account not anonymized at the clock's now")
	}
	if identities.deleted != "" || count(t, pool, "SELECT count(*) FROM pending_auth0_deletions WHERE user_id = $1", bob.ID) != 1 {
		t.Fatal("want the deletion at Auth0 pending, not done before CompleteDeletion")
	}
	s.CompleteDeletion(t.Context(), deletion)
	if identities.deleted != "email|bob" || count(t, pool, "SELECT count(*) FROM pending_auth0_deletions WHERE user_id = $1", bob.ID) != 0 {
		t.Fatalf("deleted at Auth0 %q, want the subject of bob and nothing pending", identities.deleted)
	}

	// Deleted: every change answers user_not_found.
	for name, change := range map[string]func(ctx context.Context) error{
		"update": func(ctx context.Context) error {
			_, err := s.AdminUpdateProfile(ctx, admin, bob.ID, ProfileChanges{})
			return err
		},
		"block":   func(ctx context.Context) error { _, err := s.AdminBlock(ctx, admin, bob.ID); return err },
		"unblock": func(ctx context.Context) error { _, err := s.AdminUnblock(ctx, admin, bob.ID); return err },
		"delete":  func(ctx context.Context) error { _, err := s.AdminDelete(ctx, admin, bob.ID); return err },
	} {
		if err := inTx(change); !errors.Is(err, apperr.New(apperr.CodeUserNotFound, nil)) {
			t.Fatalf("%s of a deleted account: %v, want user_not_found", name, err)
		}
	}
}
