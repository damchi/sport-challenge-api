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

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damchi/sport-challenge-api/internal/activity"
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

// TestAdminChangesRunOnASingleConnection: through the router, on a pool of
// one connection, the admin changes that read the sports catalogue under
// the week lock go through. Every read of a correction runs in its
// transaction: one that took a second connection would wait for the one
// the transaction holds, as on an instance whose other connections wait
// for the week lock.
func TestAdminChangesRunOnASingleConnection(t *testing.T) {
	setup := testdb.New(t)
	cfg := setup.Config()
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	gdb, err := db.NewGorm(pool, logger)
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(recordNow)
	var challenges *challenge.Service
	users := user.NewService(user.NewRepository(gdb), lockForDeletionFunc(func(ctx context.Context, userID string) error {
		return challenges.LockForDeletion(ctx, userID)
	}), nil, deletedAtAuth0{}, clk, logger)
	sports := sport.NewService(sport.NewRepository(gdb))
	challenges = challenge.NewService(challenge.NewRepository(gdb), challengeProfiles{users}, sports, challengeStandings{}, clk)
	activities := activity.NewService(activity.NewRepository(gdb), activityProfiles{users}, activityCatalogue{sports}, participations{challenges}, activityScopes{challenges}, clk)
	invitations := invitation.NewService(invitationChallenges{challenges}, invitation.NewRepository(gdb), clk)
	leaderboards := leaderboard.NewService(leaderboardChallenges{challenges}, leaderboardProfiles{users}, leaderboard.NewRepository(gdb), clk, 24*time.Hour)
	adminRepo := admin.NewRepository(gdb)
	recompute := closure.NewService(closure.NewRepository(gdb), leaderboard.NewRepository(gdb), clk, logger)
	corrections := admin.NewCorrections(adminRepo, admin.NewJournal(adminRepo, clk), recompute, leaderboards)

	var alice, bob, id string
	if err := setup.QueryRow(t.Context(), "INSERT INTO users (auth_subject, first_name, last_name, pseudo) VALUES ('alice', 'Test', 'User', 'alice') RETURNING id").Scan(&alice); err != nil {
		t.Fatal(err)
	}
	if err := setup.QueryRow(t.Context(), "INSERT INTO users (auth_subject, first_name, last_name, pseudo) VALUES ('bob', 'Test', 'User', 'bob') RETURNING id").Scan(&bob); err != nil {
		t.Fatal(err)
	}
	start := toronto(t, 2026, 6, 1, 0)
	if err := setup.QueryRow(t.Context(), `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by)
		VALUES ('Spring', 'public', 'America/Toronto', $1, $2, $3) RETURNING id`, start, start.AddDate(0, 0, 14), alice).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.Exec(t.Context(), `INSERT INTO categories (challenge_id, type, position, params) VALUES ($1, 'cumulative_time', 1, '{}'),
		($1, 'cumulative_distance', 2, '{"factors": {"run": 1}}')`, id); err != nil {
		t.Fatal(err)
	}
	// Alice created it: an ongoing challenge always keeps a creator.
	if _, err := setup.Exec(t.Context(), `INSERT INTO challenge_creators (challenge_id, user_id) VALUES ($1, $2)`, id, alice); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.Exec(t.Context(), `INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3), ($1, $4, $3)`, id, bob, start, alice); err != nil {
		t.Fatal(err)
	}

	handlers := testHandlers()
	handlers.adminHandler = admin.NewHandler(admin.NewService(adminRepo, nil, clk, buildinfo.Get()),
		admin.NewChallengeCorrections(corrections, challenges, invitations),
		admin.NewMemberCorrections(corrections, challenges),
		admin.NewActivityCorrections(corrections, activities),
		admin.NewAccountCorrections(corrections, users))
	opts := routerOptions{allowedOrigins: []string{webOrigin}, ipLimiter: testLimiter(1000), userLimiter: testLimiter(1000)}
	r := newTestRouterWith(t, opts, handlers, adminVerifier{}, func(context.Context, auth.Claims, string) (string, error) {
		return alice, nil
	})

	for _, tc := range []struct {
		name, method, path, body string
		want                     int
	}{
		{
			"challenge settings", http.MethodPatch, "/api/v1/admin/challenges/" + id,
			`{"distance_factors":{"ride":0.4},"reason":"Coefficient mal saisi"}`, http.StatusOK,
		},
		{
			"activity of an account", http.MethodPost, "/api/v1/admin/users/" + bob + "/activities",
			`{"sport_code":"run","started_at":"2026-06-02T14:00:00Z","duration_seconds":1800,"distance_meters":5000,"reason":"Oubli de saisie"}`, http.StatusOK,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Waiting for a second connection would last until the
			// deadline of the request: a shorter one fails sooner.
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			req := httptest.NewRequestWithContext(ctx, tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer some.access.token")
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("got %d %s, want %d", rec.Code, rec.Body, tc.want)
			}
		})
	}
}
