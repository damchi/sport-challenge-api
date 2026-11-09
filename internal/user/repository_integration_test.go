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

	_, err := NewService(repo, nil, nil, discard).GetOrCreateBySubject(t.Context(), "email|gone")
	if !errors.Is(err, apperr.New(apperr.CodeAccountDeleted, nil)) {
		t.Fatalf("error = %v, want account_deleted", err)
	}
	if n := countUsers(t, pool, "email|gone"); n != 1 {
		t.Fatalf("%d rows, want the anonymized one only", n)
	}
}

func newUser(t *testing.T, repo *Repository, subject string) User {
	t.Helper()
	u, err := repo.GetOrCreateBySubject(t.Context(), subject)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestUpdateProfile(t *testing.T) {
	repo, _ := newRepository(t)
	u := newUser(t, repo, "email|profile")

	got, err := repo.UpdateProfile(t.Context(), u.ID, ProfileChanges{Pseudo: ptr("Marie_92"), Language: ptr("fr")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Pseudo == nil || *got.Pseudo != "Marie_92" || got.Language == nil || *got.Language != "fr" || !got.UpdatedAt.After(u.UpdatedAt) && !got.UpdatedAt.Equal(u.UpdatedAt) {
		t.Fatalf("user = %+v, want pseudo Marie_92 and language fr", got)
	}

	// Only the language changes: the pseudo stays.
	got, err = repo.UpdateProfile(t.Context(), u.ID, ProfileChanges{Language: ptr("en")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Pseudo == nil || *got.Pseudo != "Marie_92" || *got.Language != "en" {
		t.Fatalf("user = %+v, want pseudo kept and language en", got)
	}

	// The user can change the case of their own pseudo.
	if got, err = repo.UpdateProfile(t.Context(), u.ID, ProfileChanges{Pseudo: ptr("marie_92")}); err != nil || *got.Pseudo != "marie_92" {
		t.Fatalf("got %+v, %v, want pseudo marie_92", got, err)
	}

	// Nothing to change: the stored row comes back.
	if got, err = repo.UpdateProfile(t.Context(), u.ID, ProfileChanges{}); err != nil || got.ID != u.ID {
		t.Fatalf("got %+v, %v, want the stored user", got, err)
	}
}

func TestUpdateProfilePseudoIsUniqueRegardlessOfCase(t *testing.T) {
	repo, _ := newRepository(t)
	first := newUser(t, repo, "email|first")
	second := newUser(t, repo, "email|second")
	if _, err := repo.UpdateProfile(t.Context(), first.ID, ProfileChanges{Pseudo: ptr("Marie")}); err != nil {
		t.Fatal(err)
	}

	_, err := repo.UpdateProfile(t.Context(), second.ID, ProfileChanges{Pseudo: ptr("mARIE"), Language: ptr("fr")})
	if !errors.Is(err, errPseudoTaken) {
		t.Fatalf("error = %v, want errPseudoTaken", err)
	}
	got, err := repo.GetByID(t.Context(), second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Pseudo != nil || got.Language != nil {
		t.Fatalf("user = %+v, want nothing written on conflict", got)
	}
}

func TestUpdateProfileConcurrentPseudo(t *testing.T) {
	repo, _ := newRepository(t)
	const users = 10
	ids := make([]string, users)
	for i := range users {
		ids[i] = newUser(t, repo, "email|race-"+string(rune('a'+i))).ID
	}

	errs := make([]error, users)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range users {
		wg.Go(func() {
			<-start
			_, errs[i] = repo.UpdateProfile(t.Context(), ids[i], ProfileChanges{Pseudo: ptr("winner")})
		})
	}
	close(start)
	wg.Wait()

	var won int
	for i, err := range errs {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, errPseudoTaken):
			t.Fatalf("user %d: %v, want nil or errPseudoTaken", i, err)
		}
	}
	if won != 1 {
		t.Fatalf("%d users got the pseudo, want 1", won)
	}
}

func TestUnknownUser(t *testing.T) {
	repo, _ := newRepository(t)
	const missing = "00000000-0000-0000-0000-000000000000"

	if _, err := repo.GetByID(t.Context(), missing); !errors.Is(err, errUserNotFound) {
		t.Fatalf("GetByID error = %v, want errUserNotFound", err)
	}
	if _, err := repo.UpdateProfile(t.Context(), missing, ProfileChanges{Language: ptr("fr")}); !errors.Is(err, errUserNotFound) {
		t.Fatalf("UpdateProfile error = %v, want errUserNotFound", err)
	}
}

func TestUpdateProfileNeverWritesAnAnonymizedAccount(t *testing.T) {
	repo, pool := newRepository(t)
	var id string
	if err := pool.QueryRow(t.Context(),
		"INSERT INTO users (auth_subject, anonymized_at) VALUES ('anonymized|1', now()) RETURNING id").Scan(&id); err != nil {
		t.Fatal(err)
	}

	_, err := repo.UpdateProfile(t.Context(), id, ProfileChanges{Pseudo: ptr("revived"), Language: ptr("fr")})
	if !errors.Is(err, errUserNotFound) {
		t.Fatalf("error = %v, want errUserNotFound", err)
	}
	// Nothing to change ({}): the row is not served either.
	if _, err := repo.UpdateProfile(t.Context(), id, ProfileChanges{}); !errors.Is(err, errUserNotFound) {
		t.Fatalf("empty update error = %v, want errUserNotFound", err)
	}
	if _, err := repo.GetByID(t.Context(), id); !errors.Is(err, errUserNotFound) {
		t.Fatalf("GetByID error = %v, want errUserNotFound", err)
	}
	var pseudo, language *string
	if err := pool.QueryRow(t.Context(), "SELECT pseudo, language FROM users WHERE id = $1", id).Scan(&pseudo, &language); err != nil {
		t.Fatal(err)
	}
	if pseudo != nil || language != nil {
		t.Fatalf("pseudo %v, language %v written on an anonymized account, want nothing", pseudo, language)
	}
}

func TestPseudos(t *testing.T) {
	repo, pool := newRepository(t)
	named := newUser(t, repo, "email|named")
	if _, err := repo.UpdateProfile(t.Context(), named.ID, ProfileChanges{Pseudo: ptr("marie_92")}); err != nil {
		t.Fatal(err)
	}
	unnamed := newUser(t, repo, "email|unnamed")
	// An anonymized row that kept its pseudo: it is still never served.
	var gone string
	if err := pool.QueryRow(t.Context(),
		"INSERT INTO users (auth_subject, pseudo, anonymized_at) VALUES ('anonymized|2', 'ghost', now()) RETURNING id").Scan(&gone); err != nil {
		t.Fatal(err)
	}
	unknown := "00000000-0000-0000-0000-000000000000"

	pseudos, err := repo.Pseudos(t.Context(), []string{named.ID, unnamed.ID, gone, unknown})
	if err != nil {
		t.Fatal(err)
	}
	if len(pseudos) != 1 || pseudos[named.ID] != "marie_92" {
		t.Fatalf("pseudos %v, want only %s: marie_92", pseudos, named.ID)
	}
}
