//go:build integration

package user

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
)

// namedUser creates the user of subject with a pseudo.
func namedUser(t *testing.T, repo *Repository, subject, pseudo string) User {
	t.Helper()
	u := newUser(t, repo, subject)
	if _, err := repo.UpdateProfile(t.Context(), u.ID, ProfileChanges{Pseudo: ptr(pseudo)}); err != nil {
		t.Fatal(err)
	}
	return u
}

func noAbandon(context.Context) error { return nil }

func TestDeleteAnonymizes(t *testing.T) {
	repo, pool := newRepository(t)
	u := namedUser(t, repo, "email|alice", "alice_1")
	if _, err := pool.Exec(t.Context(), "UPDATE users SET avatar_key = 'avatars/alice.png', first_name = 'Alice', last_name = 'Martin', email = 'alice@example.com', language = 'fr' WHERE id = $1", u.ID); err != nil {
		t.Fatal(err)
	}
	other := namedUser(t, repo, "email|bob", "bob_1")

	subject, err := repo.Delete(t.Context(), u.ID, noAbandon)
	if err != nil || subject != "email|alice" {
		t.Fatalf("Delete() = %q, %v, want the former subject", subject, err)
	}
	var authSubject string
	var pseudo, firstName, lastName, email, avatar, language *string
	var anonymizedAt *time.Time
	if err := pool.QueryRow(t.Context(), "SELECT auth_subject, pseudo, first_name, last_name, email, avatar_key, language, anonymized_at FROM users WHERE id = $1", u.ID).
		Scan(&authSubject, &pseudo, &firstName, &lastName, &email, &avatar, &language, &anonymizedAt); err != nil {
		t.Fatal(err)
	}
	if authSubject != "deleted|"+u.ID || pseudo != nil || firstName != nil || lastName != nil || email != nil || avatar != nil || language != nil || anonymizedAt == nil {
		t.Fatalf("row %q, pseudo %v, names %v %v, email set %t, avatar %v, anonymized %v: want the placeholder subject, nothing personal left, anonymized",
			authSubject, pseudo, firstName, lastName, email != nil, avatar, anonymizedAt)
	}
	// Only the fingerprint of the subject is kept, never the subject itself.
	sum := sha256.Sum256([]byte("email|alice"))
	if n := count(t, pool, "SELECT count(*) FROM deleted_subjects WHERE fingerprint = $1", sum[:]); n != 1 {
		t.Fatalf("%d fingerprints, want 1", n)
	}
	if n := count(t, pool, "SELECT count(*) FROM users WHERE auth_subject = 'email|alice'"); n != 0 {
		t.Fatalf("%d rows still hold the subject, want 0", n)
	}
	// The other user is untouched; the pseudo is free again.
	if _, err := repo.GetByID(t.Context(), other.ID); err != nil {
		t.Fatalf("other user: %v", err)
	}
	if _, err := repo.Delete(t.Context(), u.ID, noAbandon); !errors.Is(err, errUserNotFound) {
		t.Fatalf("second Delete() error = %v, want errUserNotFound", err)
	}
}

func TestDeletedSubjectNeverLogsInAgain(t *testing.T) {
	repo, pool := newRepository(t)
	svc := NewService(repo, nil, nil, nil, discard)
	u := namedUser(t, repo, "email|carol", "carol_1")
	if _, err := repo.Delete(t.Context(), u.ID, noAbandon); err != nil {
		t.Fatal(err)
	}

	// The access token is still valid: the same subject logs in again.
	_, err := svc.GetOrCreateBySubject(t.Context(), "email|carol", "")
	if !errors.Is(err, apperr.New(apperr.CodeAccountDeleted, nil)) {
		t.Fatalf("error = %v, want account_deleted", err)
	}
	if n := count(t, pool, "SELECT count(*) FROM users"); n != 1 {
		t.Fatalf("%d users, want only the anonymized one: no account created", n)
	}
	// Another subject still gets its account.
	if _, err := svc.GetOrCreateBySubject(t.Context(), "email|dave", ""); err != nil {
		t.Fatalf("new subject: %v", err)
	}
}

func TestDeleteRollsBackWhenAbandonFails(t *testing.T) {
	repo, pool := newRepository(t)
	u := namedUser(t, repo, "email|erin", "erin_1")
	failure := errors.New("abandon failed")

	_, err := repo.Delete(t.Context(), u.ID, func(ctx context.Context) error {
		// The abandons run in the deletion's transaction: they see the anonymization.
		var anonymized bool
		if err := db.Conn(ctx, repo.db).Raw("SELECT anonymized_at IS NOT NULL FROM users WHERE id = ?", u.ID).Scan(&anonymized).Error; err != nil {
			return err
		}
		if !anonymized {
			t.Error("the abandons do not see the anonymization")
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("error = %v, want the failure", err)
	}
	if got, err := repo.GetByID(t.Context(), u.ID); err != nil || got.Pseudo == nil || *got.Pseudo != "erin_1" {
		t.Fatalf("user %+v, %v, want it untouched", got, err)
	}
	if n := count(t, pool, "SELECT count(*) FROM deleted_subjects"); n != 0 {
		t.Fatalf("%d fingerprints, want none", n)
	}
}

func TestPatchRacingTheDeletion(t *testing.T) {
	repo, pool := newRepository(t)
	u := namedUser(t, repo, "email|frank", "frank_1")

	// The deletion holds the user row while the PATCH arrives.
	locked, release := make(chan struct{}), make(chan struct{})
	deleted := make(chan error, 1)
	go func() {
		_, err := repo.Delete(t.Context(), u.ID, func(context.Context) error {
			close(locked)
			<-release
			return nil
		})
		deleted <- err
	}()
	select {
	case <-locked:
	case err := <-deleted:
		t.Fatalf("deletion ended before holding the lock: %v", err)
	}
	patched := make(chan error, 1)
	go func() {
		_, err := repo.UpdateProfile(t.Context(), u.ID, ProfileChanges{Pseudo: ptr("revived")})
		patched <- err
	}()
	// Give the PATCH the time to reach the lock, then let the deletion commit.
	time.Sleep(100 * time.Millisecond)
	close(release)
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
	if err := <-patched; !errors.Is(err, errUserNotFound) {
		t.Fatalf("PATCH error = %v, want errUserNotFound", err)
	}
	var pseudo *string
	if err := pool.QueryRow(t.Context(), "SELECT pseudo FROM users WHERE id = $1", u.ID).Scan(&pseudo); err != nil {
		t.Fatal(err)
	}
	if pseudo != nil {
		t.Fatalf("pseudo %q written on the anonymized account, want none", *pseudo)
	}
}

func TestAddPendingAuth0Deletion(t *testing.T) {
	repo, pool := newRepository(t)
	for range 2 {
		if err := repo.AddPendingAuth0Deletion(t.Context(), "email|gina"); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, pool, "SELECT count(*) FROM pending_auth0_deletions WHERE auth_subject = 'email|gina'"); n != 1 {
		t.Fatalf("%d pending rows, want 1", n)
	}
}

func count(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPendingAuth0DeletionsReplay(t *testing.T) {
	repo, pool := newRepository(t)
	ctx := t.Context()
	for _, subject := range []string{"email|first", "email|second"} {
		if err := repo.AddPendingAuth0Deletion(ctx, subject); err != nil {
			t.Fatal(err)
		}
	}
	got, err := repo.PendingAuth0Deletions(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("PendingAuth0Deletions() = %v, %v, want 2 subjects", got, err)
	}

	identities := &flakyIdentities{failing: map[string]bool{"email|first": true}}
	svc := NewService(repo, nil, nil, identities, discard)
	if res, err := svc.RetryAuth0Deletions(ctx); err == nil || res != (RetryResult{Deleted: 1, Failed: 1}) {
		t.Fatalf("first run = %+v, %v, want 1 deleted, 1 failed and an error", res, err)
	}
	if n := count(t, pool, "SELECT count(*) FROM pending_auth0_deletions WHERE auth_subject = 'email|first'"); n != 1 {
		t.Fatalf("%d rows for the failed one, want it still pending", n)
	}
	if n := count(t, pool, "SELECT count(*) FROM pending_auth0_deletions"); n != 1 {
		t.Fatalf("%d pending rows, want 1", n)
	}

	identities.failing = nil
	if res, err := svc.RetryAuth0Deletions(ctx); err != nil || res != (RetryResult{Deleted: 1}) {
		t.Fatalf("second run = %+v, %v, want the last one done", res, err)
	}
	// Removing a record already removed is not an error (run at the same time).
	if err := repo.RemovePendingAuth0Deletion(ctx, "email|first"); err != nil {
		t.Fatalf("RemovePendingAuth0Deletion() = %v, want nil", err)
	}
	if res, err := svc.RetryAuth0Deletions(ctx); err != nil || res != (RetryResult{}) {
		t.Fatalf("third run = %+v, %v, want nothing", res, err)
	}
}
