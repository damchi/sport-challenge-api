//go:build integration

package user

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
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

func noErasure(context.Context) error { return nil }

// deletionTime dates the deletions of the repository tests: not now, so that
// a date written by the database instead would show.
var deletionTime = time.Date(2026, 3, 9, 14, 30, 0, 0, time.UTC)

func TestDeleteAnonymizes(t *testing.T) {
	repo, pool := newRepository(t)
	u := namedUser(t, repo, "email|alice", "alice_1")
	if _, err := pool.Exec(t.Context(), "UPDATE users SET avatar_key = 'avatars/alice.png', first_name = 'Alice', last_name = 'Martin', email = 'alice@example.com', language = 'fr' WHERE id = $1", u.ID); err != nil {
		t.Fatal(err)
	}
	other := namedUser(t, repo, "email|bob", "bob_1")

	subject, err := repo.Delete(t.Context(), u.ID, deletionTime, noErasure)
	if err != nil || subject != "email|alice" {
		t.Fatalf("Delete() = %q, %v, want the former subject", subject, err)
	}
	var authSubject string
	var pseudo, firstName, lastName, email, avatar, language *string
	var anonymizedAt, deletedAt *time.Time
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
	if err := pool.QueryRow(t.Context(), "SELECT deleted_at FROM deleted_subjects WHERE fingerprint = $1", sum[:]).Scan(&deletedAt); err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	// Both dates are the instant given by the service (its clock).
	if !anonymizedAt.Equal(deletionTime) || !deletedAt.Equal(deletionTime) {
		t.Fatalf("anonymized at %v, fingerprint deleted at %v, want both %v", anonymizedAt, deletedAt, deletionTime)
	}
	if n := count(t, pool, "SELECT count(*) FROM users WHERE auth_subject = 'email|alice'"); n != 0 {
		t.Fatalf("%d rows still hold the subject, want 0", n)
	}
	// The deletion at Auth0 is pending from the commit on, linked to the
	// account for the admin.
	if n := count(t, pool, "SELECT count(*) FROM pending_auth0_deletions WHERE auth_subject = 'email|alice' AND user_id = $1", u.ID); n != 1 {
		t.Fatalf("%d pending deletions of the account, want 1", n)
	}
	// The other user is untouched; the pseudo is free again.
	if _, err := repo.GetByID(t.Context(), other.ID); err != nil {
		t.Fatalf("other user: %v", err)
	}
	if _, err := repo.Delete(t.Context(), u.ID, deletionTime, noErasure); !errors.Is(err, errUserNotFound) {
		t.Fatalf("second Delete() error = %v, want errUserNotFound", err)
	}
}

func TestDeletedSubjectNeverLogsInAgain(t *testing.T) {
	repo, pool := newRepository(t)
	svc := NewService(repo, nil, nil, nil, nil, discard)
	u := namedUser(t, repo, "email|carol", "carol_1")
	if _, err := repo.Delete(t.Context(), u.ID, deletionTime, noErasure); err != nil {
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

func TestDeleteRollsBackWhenErasureFails(t *testing.T) {
	repo, pool := newRepository(t)
	u := namedUser(t, repo, "email|erin", "erin_1")
	failure := errors.New("erasure failed")

	_, err := repo.Delete(t.Context(), u.ID, deletionTime, func(ctx context.Context) error {
		// The lock and the erasure run in the deletion's transaction: they see the anonymization.
		var anonymized bool
		if err := db.Conn(ctx, repo.db).Raw("SELECT anonymized_at IS NOT NULL FROM users WHERE id = ?", u.ID).Scan(&anonymized).Error; err != nil {
			return err
		}
		if !anonymized {
			t.Error("the erasure does not see the anonymization")
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
	if n := count(t, pool, "SELECT count(*) FROM pending_auth0_deletions"); n != 0 {
		t.Fatalf("%d pending deletions, want none", n)
	}
}

func TestPatchRacingTheDeletion(t *testing.T) {
	repo, pool := newRepository(t)
	u := namedUser(t, repo, "email|frank", "frank_1")

	// The deletion holds the user row while the PATCH arrives.
	locked, release := make(chan struct{}), make(chan struct{})
	deleted := make(chan error, 1)
	go func() {
		_, err := repo.Delete(t.Context(), u.ID, deletionTime, func(context.Context) error {
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

func TestServiceDeleteRecordsTheAuth0Deletion(t *testing.T) {
	pending := func(t *testing.T, pool *pgxpool.Pool, subject string) int {
		t.Helper()
		return count(t, pool, "SELECT count(*) FROM pending_auth0_deletions WHERE auth_subject = $1", subject)
	}
	t.Run("done at Auth0: no longer pending", func(t *testing.T) {
		repo, pool := newRepository(t)
		u := namedUser(t, repo, "email|gina", "gina_1")
		identities := &flakyIdentities{}
		if err := NewService(repo, lockThen(func() {}), nil, identities, clock.Real{}, discard).Delete(t.Context(), u.ID); err != nil {
			t.Fatal(err)
		}
		if n := pending(t, pool, "email|gina"); n != 0 || !slices.Equal(identities.calls, []string{"email|gina"}) {
			t.Fatalf("%d pending, Auth0 calls %v, want none pending and one call", n, identities.calls)
		}
	})
	t.Run("Auth0 fails: kept for the worker", func(t *testing.T) {
		repo, pool := newRepository(t)
		u := namedUser(t, repo, "email|hugo", "hugo_1")
		identities := &flakyIdentities{failing: map[string]bool{"email|hugo": true}}
		if err := NewService(repo, lockThen(func() {}), nil, identities, clock.Real{}, discard).Delete(t.Context(), u.ID); err != nil {
			t.Fatalf("error = %v, want nil: the account is deleted", err)
		}
		if n := pending(t, pool, "email|hugo"); n != 1 {
			t.Fatalf("%d pending, want 1", n)
		}
	})
	t.Run("stopped between the commit and Auth0: replayed by the worker", func(t *testing.T) {
		repo, pool := newRepository(t)
		u := namedUser(t, repo, "email|iris", "iris_1")
		// The process stops right after the commit: Auth0 is never called.
		if _, err := repo.Delete(t.Context(), u.ID, deletionTime, noErasure); err != nil {
			t.Fatal(err)
		}
		if n := pending(t, pool, "email|iris"); n != 1 {
			t.Fatalf("%d pending, want 1", n)
		}
		identities := &flakyIdentities{}
		res, err := NewService(repo, nil, nil, identities, nil, discard).RetryAuth0Deletions(t.Context())
		if err != nil || res != (RetryResult{Deleted: 1}) || !slices.Equal(identities.calls, []string{"email|iris"}) {
			t.Fatalf("run = %+v, %v, Auth0 calls %v, want the deletion replayed", res, err, identities.calls)
		}
		if n := pending(t, pool, "email|iris"); n != 0 {
			t.Fatalf("%d pending, want 0", n)
		}
	})
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
	if _, err := pool.Exec(ctx, "INSERT INTO pending_auth0_deletions (auth_subject) VALUES ('email|first'), ('email|second')"); err != nil {
		t.Fatal(err)
	}
	got, err := repo.PendingAuth0Deletions(ctx)
	want := []PendingDeletion{{Subject: "email|first"}, {Subject: "email|second"}}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("PendingAuth0Deletions() = %v, %v, want %v", got, err, want)
	}

	identities := &flakyIdentities{failing: map[string]bool{"email|first": true}}
	svc := NewService(repo, nil, nil, identities, nil, discard)
	if res, err := svc.RetryAuth0Deletions(ctx); err == nil || res != (RetryResult{Deleted: 1, Failed: 1}) {
		t.Fatalf("first run = %+v, %v, want 1 deleted, 1 failed and an error", res, err)
	}
	if n := count(t, pool, "SELECT count(*) FROM pending_auth0_deletions WHERE auth_subject = 'email|first' AND attempts = 1 AND last_error_code = 'auth0_status_503' AND NOT blocked"); n != 1 {
		t.Fatalf("%d rows with the failure recorded, want 1", n)
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

func TestPendingAuth0DeletionsBlocked(t *testing.T) {
	repo, pool := newRepository(t)
	ctx := t.Context()
	u, err := repo.GetOrCreateBySubject(ctx, "email|refused")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO pending_auth0_deletions (auth_subject, user_id) VALUES ('email|refused', $1)", u.ID); err != nil {
		t.Fatal(err)
	}
	got, err := repo.PendingAuth0Deletions(ctx)
	if err != nil || !slices.Equal(got, []PendingDeletion{{Subject: "email|refused", UserID: u.ID}}) {
		t.Fatalf("PendingAuth0Deletions() = %v, %v, want the deletion of the account", got, err)
	}

	identities := &flakyIdentities{refused: map[string]bool{"email|refused": true}}
	svc := NewService(repo, nil, nil, identities, nil, discard)
	for run := 1; run <= 2; run++ {
		if res, err := svc.RetryAuth0Deletions(ctx); err != nil || res != (RetryResult{Blocked: 1}) {
			t.Fatalf("run %d = %+v, %v, want 1 blocked and no error", run, res, err)
		}
	}
	if n := count(t, pool, "SELECT count(*) FROM pending_auth0_deletions WHERE auth_subject = 'email|refused' AND attempts = 2 AND last_error_code = 'auth0_status_400' AND blocked"); n != 1 {
		t.Fatalf("%d blocked rows after 2 runs, want 1", n)
	}

	// A transient failure afterwards: no longer blocked.
	identities.refused, identities.failing = nil, map[string]bool{"email|refused": true}
	if _, err := svc.RetryAuth0Deletions(ctx); err == nil {
		t.Fatal("transient failure: want an error")
	}
	if n := count(t, pool, "SELECT count(*) FROM pending_auth0_deletions WHERE attempts = 3 AND last_error_code = 'auth0_status_503' AND NOT blocked"); n != 1 {
		t.Fatalf("%d rows unblocked, want 1", n)
	}
}
