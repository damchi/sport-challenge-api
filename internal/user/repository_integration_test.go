//go:build integration

package user

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
)

func newRepository(t *testing.T) (*Repository, *pgxpool.Pool) {
	t.Helper()
	pool := testdb.New(t)
	gdb, err := db.NewGorm(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return NewRepository(gdb), pool
}

func countUsers(t *testing.T, pool *pgxpool.Pool, subject string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM users WHERE auth_subject = $1", subject).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestGetOrCreateBySubjectCreatesAnEmptyProfile(t *testing.T) {
	repo, pool := newRepository(t)

	u, err := repo.GetOrCreateBySubject(t.Context(), "email|new")
	if err != nil {
		t.Fatal(err)
	}
	if u.ID == "" || u.AuthSubject != "email|new" || u.CreatedAt.IsZero() || u.UpdatedAt.IsZero() {
		t.Fatalf("user = %+v, want an id, the subject and the database timestamps", u)
	}
	if u.Pseudo != nil || u.Language != nil || u.AvatarKey != nil || u.AnonymizedAt != nil {
		t.Fatalf("user = %+v, want an empty profile", u)
	}
	if n := countUsers(t, pool, "email|new"); n != 1 {
		t.Fatalf("%d rows, want 1", n)
	}
}

func TestGetOrCreateBySubjectReturnsTheExistingUser(t *testing.T) {
	repo, pool := newRepository(t)

	first, err := repo.GetOrCreateBySubject(t.Context(), "email|again")
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.GetOrCreateBySubject(t.Context(), "email|again")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("second call returned %s, want %s", second.ID, first.ID)
	}
	if n := countUsers(t, pool, "email|again"); n != 1 {
		t.Fatalf("%d rows, want 1", n)
	}
}

func TestGetOrCreateBySubjectIsSafeUnderConcurrency(t *testing.T) {
	repo, pool := newRepository(t)
	const calls = 50

	ids := make([]string, calls)
	errs := make([]error, calls)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range calls {
		wg.Go(func() {
			<-start
			u, err := repo.GetOrCreateBySubject(t.Context(), "email|concurrent")
			ids[i], errs[i] = u.ID, err
		})
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if ids[i] != ids[0] {
			t.Fatalf("call %d returned %s, want %s for every call", i, ids[i], ids[0])
		}
	}
	if n := countUsers(t, pool, "email|concurrent"); n != 1 {
		t.Fatalf("%d users created by %d concurrent calls, want 1", n, calls)
	}
}

func TestServiceRefusesAnAnonymizedAccount(t *testing.T) {
	repo, pool := newRepository(t)
	if _, err := pool.Exec(t.Context(),
		"INSERT INTO users (auth_subject, anonymized_at) VALUES ('email|gone', now())"); err != nil {
		t.Fatal(err)
	}

	_, err := NewService(repo).GetOrCreateBySubject(t.Context(), "email|gone")
	if !errors.Is(err, apperr.New(apperr.CodeAccountDeleted, nil)) {
		t.Fatalf("error = %v, want account_deleted", err)
	}
	if n := countUsers(t, pool, "email|gone"); n != 1 {
		t.Fatalf("%d rows, want the anonymized one only", n)
	}
}
