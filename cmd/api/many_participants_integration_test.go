//go:build integration

package main

import (
	"io"
	"log/slog"
	"testing"

	"github.com/damchi/sport-challenge-api/internal/activity"
	"github.com/damchi/sport-challenge-api/internal/challenge"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/sport"
	"github.com/damchi/sport-challenge-api/internal/user"
)

// crowd is more participants than the 65,535 parameters PostgreSQL takes
// in one query.
const crowd = 70_000

// TestAChallengeOfMoreParticipantsThanQueryParameters reads the activity
// feed and the leaderboard of a challenge of crowd participants
// (SC-API-182): the list of every participant goes to the database as one
// parameter, so the size of a challenge has no bound (README §5.5).
func TestAChallengeOfMoreParticipantsThanQueryParameters(t *testing.T) {
	f := newLeaderboardFixture(t)
	start := toronto(t, 2026, 6, 1, 0)
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO users (auth_subject, pseudo, first_name, last_name)
		SELECT 'email|crowd' || g, 'crowd' || g, 'Test', 'User' FROM generate_series(1, $1) AS g`, crowd); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO participants (challenge_id, user_id, joined_at)
		SELECT $1, id, $2 FROM users WHERE pseudo LIKE 'crowd%'`, f.public, start); err != nil {
		t.Fatal(err)
	}
	// The last of the crowd ran too: their name comes from the same query
	// as everyone else's.
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters)
		SELECT id, 'run', $1, 600, 2000 FROM users WHERE pseudo = $2`, toronto(t, 2026, 6, 9, 7), "crowd70000"); err != nil {
		t.Fatal(err)
	}

	board, err := f.svc.Get(t.Context(), auth.Actor{ID: f.runner}, f.public)
	if err != nil {
		t.Fatalf("leaderboard: %v", err)
	}
	cumulative := board.Categories[0]
	if len(cumulative.Entries) != crowd+1 {
		t.Fatalf("leaderboard has %d entries, want %d", len(cumulative.Entries), crowd+1)
	}
	for _, e := range cumulative.Entries {
		if e.Name.Pseudo == nil {
			t.Fatalf("entry %s has no name", e.UserID)
		}
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	gdb, err := db.NewGorm(f.pool, logger)
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(recordNow)
	users := user.NewService(user.NewRepository(gdb), nil, nil, nil, nil, logger)
	sports := sport.NewService(sport.NewRepository(gdb))
	challenges := challenge.NewService(challenge.NewRepository(gdb), challengeProfiles{users}, sports, challengeStandings{}, clk)
	activities := activity.NewService(activity.NewRepository(gdb), activityProfiles{users}, activityCatalogue{sports}, participations{challenges}, activityScopes{challenges}, clk)

	feed, err := activities.OfChallenge(t.Context(), auth.Actor{ID: f.runner}, f.public, 10, nil)
	if err != nil {
		t.Fatalf("activity feed: %v", err)
	}
	// The runner's two runs and the crowd's one, most recent first.
	if len(feed.Items) != 3 || feed.Items[0].Author.Pseudo == nil || *feed.Items[0].Author.Pseudo != "runner" {
		t.Fatalf("activity feed = %+v, want the 3 runs, the runner's of June 10 first", feed.Items)
	}
}
