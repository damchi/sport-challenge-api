//go:build integration

package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damchi/sport-challenge-api/internal/activity"
	"github.com/damchi/sport-challenge-api/internal/admin"
	"github.com/damchi/sport-challenge-api/internal/challenge"
	"github.com/damchi/sport-challenge-api/internal/closure"
	"github.com/damchi/sport-challenge-api/internal/invitation"
	"github.com/damchi/sport-challenge-api/internal/leaderboard"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
	"github.com/damchi/sport-challenge-api/internal/sport"
	"github.com/damchi/sport-challenge-api/internal/user"
)

// pausedRecompute runs before, if set, then the recompute of a challenge.
type pausedRecompute struct {
	inner  admin.Recomputer
	before func()
}

func (p *pausedRecompute) Recompute(ctx context.Context, id string) error {
	if p.before != nil {
		p.before()
	}
	return p.inner.Recompute(ctx, id)
}

// awaitLockWaiter waits until a transaction of the database of the test
// waits for a lock on a row (pg_stat_activity: packages run in parallel
// on other databases).
func awaitLockWaiter(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
			t.Error(err)
			return
		}
		if waiting > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Error("nobody waits for a lock")
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestAdminMemberChangeAndAccountDeletionInParallel: an admin change of a
// member and the deletion of the same account at the same moment never
// wait for each other in a cycle (PostgreSQL would abort one, 40P01), in
// a finished challenge whose recompute locks the users of its
// participants: the change locks the account first, as the deletion does.
// Whoever locks it first goes first; the other waits, then runs or finds
// the account deleted.
func TestAdminMemberChangeAndAccountDeletionInParallel(t *testing.T) {
	pool := testdb.New(t)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	gdb, err := db.NewGorm(pool, logger)
	if err != nil {
		t.Fatal(err)
	}
	// Two weeks from Monday 2026-05-04, over and frozen at recordNow.
	start := toronto(t, 2026, 5, 4, 0)
	clk := clock.NewFake(recordNow)
	var (
		challenges  *challenge.Service
		activities  *activity.Service
		invitations *invitation.Service
		// beforeErasure, if set, runs in the transaction of a deletion,
		// once the account is locked.
		beforeErasure func()
	)
	erasers := []user.Eraser{
		eraseUserFunc(func(context.Context, string) error {
			if beforeErasure != nil {
				beforeErasure()
			}
			return nil
		}),
		eraseUserFunc(func(ctx context.Context, userID string) error { return invitations.EraseUser(ctx, userID) }),
		eraseUserFunc(func(ctx context.Context, userID string) error { return activities.EraseUser(ctx, userID) }),
		eraseUserFunc(func(ctx context.Context, userID string) error { return challenges.EraseUser(ctx, userID) }),
	}
	users := user.NewService(user.NewRepository(gdb), abandonAllFunc(func(ctx context.Context, userID string) error {
		return challenges.AbandonAll(ctx, userID)
	}), erasers, deletedAtAuth0{}, clk, logger)
	sports := sport.NewService(sport.NewRepository(gdb))
	challenges = challenge.NewService(challenge.NewRepository(gdb), challengeProfiles{users}, sports, challengeStandings{}, clk)
	activities = activity.NewService(activity.NewRepository(gdb), activityProfiles{users}, activityCatalogue{sports}, participations{challenges}, activityScopes{challenges}, clk)
	invitations = invitation.NewService(invitationChallenges{challenges}, invitation.NewRepository(gdb), clk)
	leaderboards := leaderboard.NewService(leaderboardChallenges{challenges}, leaderboardProfiles{users}, leaderboard.NewRepository(gdb), clk, 24*time.Hour)
	adminRepo := admin.NewRepository(gdb)
	recompute := &pausedRecompute{inner: closure.NewService(closure.NewRepository(gdb), leaderboard.NewRepository(gdb), clk, logger)}
	members := admin.NewMemberCorrections(admin.NewCorrections(adminRepo, admin.NewJournal(adminRepo, clk), recompute, leaderboards), challenges)

	insertUser := func(subject string) string {
		var id string
		if err := pool.QueryRow(t.Context(), "INSERT INTO users (auth_subject, first_name, last_name) VALUES ($1, 'Test', 'User') RETURNING id", subject).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	alice, first, second := insertUser("alice"), insertUser("first"), insertUser("second")
	var id string
	if err := pool.QueryRow(t.Context(), `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by)
		VALUES ('Spring', 'public', 'America/Toronto', $1, $2, $3) RETURNING id`, start, start.AddDate(0, 0, 14), alice).Scan(&id); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO categories (challenge_id, type, position, params) VALUES ($1, 'cumulative_time', 1, '{}'),
		($1, 'cumulative_distance', 2, '{"factors": {"run": 1}}'), ($1, 'elimination', 3, '{"sport": "run", "step_meters": 2000}')`, id)
	exec(`INSERT INTO challenge_creators (challenge_id, user_id) VALUES ($1, $2)`, id, alice)
	for _, u := range []string{alice, first, second} {
		exec(`INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3)`, id, u, start)
		exec(`INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters) VALUES ($1, 'run', $2, 1800, 5000)`,
			u, start.Add(30*time.Hour))
	}
	adminActor := auth.Actor{ID: alice, Admin: true}

	// run runs the change and the deletion, each in its goroutine, and
	// returns their errors once both are over.
	type outcome struct{ change, deletion error }
	run := func(t *testing.T, change, deletion func() error) outcome {
		t.Helper()
		changed, deleted := make(chan error, 1), make(chan error, 1)
		go func() { changed <- change() }()
		go func() { deleted <- deletion() }()
		var out outcome
		timeout := time.After(30 * time.Second)
		for range 2 {
			select {
			case out.change = <-changed:
			case out.deletion = <-deleted:
			case <-timeout:
				t.Fatal("the change and the deletion are stuck")
			}
		}
		for _, err := range []error{out.change, out.deletion} {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "40P01" {
				t.Fatalf("deadlock: %v", err)
			}
		}
		return out
	}
	deleted := func(t *testing.T, userID string) bool {
		t.Helper()
		var anonymized bool
		if err := pool.QueryRow(t.Context(), `SELECT anonymized_at IS NOT NULL FROM users WHERE id = $1`, userID).Scan(&anonymized); err != nil {
			t.Fatal(err)
		}
		return anonymized
	}

	t.Run("the deletion locks the account first: the change finds it deleted", func(t *testing.T) {
		locked := make(chan struct{})
		// The deletion holds the account until the change waits for it.
		beforeErasure = func() {
			close(locked)
			awaitLockWaiter(t, pool)
		}
		t.Cleanup(func() { beforeErasure = nil })
		out := run(t, func() error {
			<-locked
			_, err := members.ExcludeParticipant(context.Background(), adminActor, id, first, "Tricherie", false)
			return err
		}, func() error {
			return users.Delete(context.Background(), first)
		})
		if out.deletion != nil || !errors.Is(out.change, apperr.New(apperr.CodeNotFound, nil)) || !deleted(t, first) {
			t.Fatalf("change %v, deletion %v: want the account deleted, the change refused (not_found)", out.change, out.deletion)
		}
	})

	t.Run("the change locks the account first: the deletion waits for it", func(t *testing.T) {
		locked := make(chan struct{})
		// The change holds the account until the deletion waits for it.
		recompute.before = func() {
			close(locked)
			awaitLockWaiter(t, pool)
		}
		t.Cleanup(func() { recompute.before = nil })
		out := run(t, func() error {
			_, err := members.ExcludeParticipant(context.Background(), adminActor, id, second, "Tricherie", false)
			return err
		}, func() error {
			<-locked
			return users.Delete(context.Background(), second)
		})
		if out.change != nil || out.deletion != nil || !deleted(t, second) {
			t.Fatalf("change %v, deletion %v: want both done", out.change, out.deletion)
		}
		var rows int
		if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM participants WHERE user_id = $1`, second).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != 0 {
			t.Fatalf("%d participations of the deleted account left, want none", rows)
		}
	})
}
