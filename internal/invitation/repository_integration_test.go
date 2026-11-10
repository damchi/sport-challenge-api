//go:build integration

package invitation

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
)

// setup gives a repository, its pool, a creator and their private challenge.
func setup(t *testing.T) (*Repository, *pgxpool.Pool, string, string) {
	t.Helper()
	pool := testdb.New(t)
	gdb, err := db.NewGorm(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	creator := insertUser(t, pool, "email|creator")
	var challenge string
	if err := pool.QueryRow(t.Context(), `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by)
		VALUES ('Spring', 'private', 'America/Toronto', '2026-03-02 05:00Z', '2026-03-23 04:00Z', $1) RETURNING id`, creator).Scan(&challenge); err != nil {
		t.Fatal(err)
	}
	return NewRepository(gdb), pool, creator, challenge
}

func insertUser(t *testing.T, pool *pgxpool.Pool, subject string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(t.Context(), "INSERT INTO users (auth_subject) VALUES ($1) RETURNING id", subject).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func hashOf(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

func TestRepositoryCreate(t *testing.T) {
	repo, pool, creator, challenge := setup(t)
	inv, err := repo.Create(t.Context(), Invitation{ChallengeID: challenge, InvitedBy: creator, TokenHash: hashOf("t1"), ExpiresAt: deadline, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if inv.ID == "" {
		t.Fatal("no id returned")
	}
	var stored []byte
	var expiresAt, createdAt time.Time
	var revoked bool
	if err := pool.QueryRow(t.Context(), "SELECT token_hash, expires_at, created_at, revoked_at IS NOT NULL FROM invitations WHERE id = $1 AND challenge_id = $2 AND invited_by = $3", inv.ID, challenge, creator).
		Scan(&stored, &expiresAt, &createdAt, &revoked); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, hashOf("t1")) || !expiresAt.Equal(deadline) || !createdAt.Equal(now) || revoked {
		t.Fatalf("stored %x, expiring %s, created %s, revoked %v", stored, expiresAt, createdAt, revoked)
	}
}

// TestRepositoryLink links a user to a valid invitation only: unknown,
// expired from the exact expiry, revoked, or to a deleted challenge, it is
// invalid and nothing is linked.
func TestRepositoryLink(t *testing.T) {
	repo, pool, creator, challenge := setup(t)
	guest := insertUser(t, pool, "email|guest")
	for token, revokedAt := range map[string]*time.Time{"valid": nil, "revoked": &now} {
		inv, err := repo.Create(t.Context(), Invitation{ChallengeID: challenge, InvitedBy: creator, TokenHash: hashOf(token), ExpiresAt: deadline, CreatedAt: now.Add(-time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if revokedAt != nil {
			if _, err := pool.Exec(t.Context(), "UPDATE invitations SET revoked_at = $1 WHERE id = $2", *revokedAt, inv.ID); err != nil {
				t.Fatal(err)
			}
		}
	}

	invalid := map[string]struct {
		token string
		at    time.Time
	}{
		"unknown":                {token: "nope", at: now},
		"revoked":                {token: "revoked", at: now},
		"at the exact expiry":    {token: "valid", at: deadline},
		"after the expiry":       {token: "valid", at: deadline.Add(time.Hour)},
		"the hash of a hash":     {token: string(hashOf("valid")), at: now},
		"hash of an empty token": {token: "", at: now},
	}
	for name, tc := range invalid {
		if _, err := repo.Link(t.Context(), hashOf(tc.token), guest, tc.at); !errors.Is(err, errInvalid) {
			t.Fatalf("%s: error = %v, want errInvalid", name, err)
		}
	}
	if n := countLinks(t, pool, guest); n != 0 {
		t.Fatalf("%d links after invalid tokens, want none", n)
	}

	for range 2 {
		got, err := repo.Link(t.Context(), hashOf("valid"), guest, deadline.Add(-time.Second))
		if err != nil || got != challenge {
			t.Fatalf("Link() = %q, %v, want %s", got, err, challenge)
		}
	}
	if n := countLinks(t, pool, guest); n != 1 {
		t.Fatalf("%d links after linking twice, want one", n)
	}

	// A deleted challenge invalidates its invitations.
	if _, err := pool.Exec(t.Context(), "UPDATE challenges SET deleted_at = $1 WHERE id = $2", now, challenge); err != nil {
		t.Fatal(err)
	}
	other := insertUser(t, pool, "email|other")
	if _, err := repo.Link(t.Context(), hashOf("valid"), other, now); !errors.Is(err, errInvalid) {
		t.Fatalf("deleted challenge: error = %v, want errInvalid", err)
	}
}

func countLinks(t *testing.T, pool *pgxpool.Pool, user string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM invitation_recipients WHERE user_id = $1", user).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
