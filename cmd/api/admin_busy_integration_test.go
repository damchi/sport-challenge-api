//go:build integration

package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/admin"
	"github.com/damchi/sport-challenge-api/internal/challenge"
	"github.com/damchi/sport-challenge-api/internal/closure"
	"github.com/damchi/sport-challenge-api/internal/invitation"
	"github.com/damchi/sport-challenge-api/internal/leaderboard"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/buildinfo"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
	"github.com/damchi/sport-challenge-api/internal/sport"
	"github.com/damchi/sport-challenge-api/internal/user"
)

// adminVerifier accepts any token, with the admin permission.
type adminVerifier struct{}

func (adminVerifier) Verify(context.Context, string) (auth.Claims, error) {
	return auth.Claims{Subject: "email|123", Admin: true}, nil
}

// TestAdminChangeAnswersBusyBeforeTheRequestDeadline: through the router,
// under the deadline of every request, an admin change that waits for the
// week lock answers 503 busy, nothing written, instead of the 500 of a
// request cut by its deadline. It waits for the lock as long as in
// production (10 s).
func TestAdminChangeAnswersBusyBeforeTheRequestDeadline(t *testing.T) {
	pool := testdb.New(t)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	gdb, err := db.NewGorm(pool, logger)
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(recordNow)
	users := user.NewService(user.NewRepository(gdb), nil, nil, deletedAtAuth0{}, clk, logger)
	sports := sport.NewService(sport.NewRepository(gdb))
	challenges := challenge.NewService(challenge.NewRepository(gdb), challengeProfiles{users}, sports, challengeStandings{}, clk)
	invitations := invitation.NewService(invitationChallenges{challenges}, invitation.NewRepository(gdb), clk)
	leaderboards := leaderboard.NewService(leaderboardChallenges{challenges}, leaderboardProfiles{users}, leaderboard.NewRepository(gdb), clk, 24*time.Hour)
	adminRepo := admin.NewRepository(gdb)
	recompute := closure.NewService(closure.NewRepository(gdb), leaderboard.NewRepository(gdb), clk, logger)
	corrections := admin.NewCorrections(adminRepo, admin.NewJournal(adminRepo, clk), recompute, leaderboards)

	var alice, id string
	if err := pool.QueryRow(t.Context(), "INSERT INTO users (auth_subject, first_name, last_name) VALUES ('alice', 'Test', 'User') RETURNING id").Scan(&alice); err != nil {
		t.Fatal(err)
	}
	start := toronto(t, 2026, 5, 4, 0)
	if err := pool.QueryRow(t.Context(), `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by)
		VALUES ('Spring', 'public', 'America/Toronto', $1, $2, $3) RETURNING id`, start, start.AddDate(0, 0, 14), alice).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO categories (challenge_id, type, position, params) VALUES ($1, 'cumulative_time', 1, '{}')`, id); err != nil {
		t.Fatal(err)
	}

	handlers := testHandlers()
	handlers.adminHandler = admin.NewHandler(admin.NewService(adminRepo, clk, buildinfo.Get()),
		admin.NewChallengeCorrections(corrections, challenges, invitations),
		admin.NewMemberCorrections(corrections, challenges),
		admin.NewActivityCorrections(corrections, nil),
		admin.NewAccountCorrections(corrections, nil))
	opts := routerOptions{allowedOrigins: []string{webOrigin}, ipLimiter: testLimiter(1000), userLimiter: testLimiter(1000)}
	r := newTestRouterWith(t, opts, handlers, adminVerifier{}, func(context.Context, auth.Claims, string) (string, error) {
		return alice, nil
	})

	// An activity write in progress holds the week lock shared.
	writer := gdb.Begin()
	t.Cleanup(func() { writer.Rollback() })
	if err := db.ShareWeekLock(writer); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/challenges/"+id, strings.NewReader(`{"title":"Printemps","reason":"Titre mal saisi"}`))
	req.Header.Set("Authorization", "Bearer some.access.token")
	req.Header.Set("Content-Type", "application/json")
	began := time.Now()
	status, code := serve(t, r, req)
	if status != http.StatusServiceUnavailable || code != "busy" {
		t.Fatalf("got %d %q, want 503 busy", status, code)
	}
	if waited := time.Since(began); waited >= requestTimeout {
		t.Fatalf("answered after %v, want before the deadline of the request (%v)", waited, requestTimeout)
	}
	var title string
	if err := pool.QueryRow(t.Context(), `SELECT title FROM challenges WHERE id = $1`, id).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Spring" {
		t.Fatalf("title %q after a lock timeout, want Spring", title)
	}
}
