//go:build integration

package main

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/activity"
	"github.com/damchi/sport-challenge-api/internal/challenge"
	"github.com/damchi/sport-challenge-api/internal/leaderboard"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
	"github.com/damchi/sport-challenge-api/internal/sport"
	"github.com/damchi/sport-challenge-api/internal/user"
)

// TestEverythingWorksWithoutAPseudo: the pseudo is optional (SC-API-118). A
// user with a first and a last name but no pseudo creates a challenge,
// another one removes theirs and joins it, both record an activity, and both
// appear in the participants and the leaderboard without a pseudo.
func TestEverythingWorksWithoutAPseudo(t *testing.T) {
	pool := testdb.New(t)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	gdb, err := db.NewGorm(pool, logger)
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(recordNow)
	users := user.NewService(user.NewRepository(gdb), nil, nil, nil, nil, logger)
	sports := sport.NewService(sport.NewRepository(gdb))
	challenges := challenge.NewService(challenge.NewRepository(gdb), challengeProfiles{users}, sports, challengeStandings{}, clk)
	activities := activity.NewService(activity.NewRepository(gdb), activityProfiles{users}, activityCatalogue{sports}, participations{challenges}, activityScopes{challenges}, clk)
	leaderboards := leaderboard.NewService(leaderboardChallenges{challenges}, leaderboardProfiles{users}, leaderboard.NewRepository(gdb), clk, 24*time.Hour)

	insertUser := func(subject, pseudo, first, last *string) string {
		var id string
		if err := pool.QueryRow(t.Context(), "INSERT INTO users (auth_subject, pseudo, first_name, last_name) VALUES ($1, $2, $3, $4) RETURNING id",
			subject, pseudo, first, last).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	str := func(s string) *string { return &s }
	creator := insertUser(str("email|creator"), nil, str("Ana"), str("Lee"))
	joiner := insertUser(str("email|joiner"), str("bo_ng"), str("Bo"), str("Ng"))

	// The joiner removes their pseudo: the profile stays complete.
	me, err := users.UpdateProfile(t.Context(), joiner, user.ProfileChanges{RemovePseudo: true})
	if err != nil || me.Pseudo != nil || !me.ProfileComplete() {
		t.Fatalf("UpdateProfile() = %+v, %v, want no pseudo and a complete profile", me, err)
	}

	// recordNow is Thursday 2026-06-11 in Toronto: the challenge starts today.
	c, err := challenges.Create(t.Context(), creator, challenge.Draft{
		Title: "No pseudo", Visibility: "public", Timezone: "America/Toronto",
		StartDate: "2026-06-11", Weeks: 2,
	})
	if err != nil {
		t.Fatalf("Create() without a pseudo: %v", err)
	}
	if _, err := challenges.Join(t.Context(), joiner, c.ID); err != nil {
		t.Fatalf("Join() without a pseudo: %v", err)
	}
	for _, id := range []string{creator, joiner} {
		d := 5000
		if _, err := activities.Record(t.Context(), id, nil, activity.Draft{SportCode: "run", StartedAt: toronto(t, 2026, 6, 11, 7), DurationSeconds: 1500, DistanceMeters: &d}); err != nil {
			t.Fatalf("Record() without a pseudo: %v", err)
		}
	}

	firstNames := map[string]string{creator: "Ana", joiner: "Bo"}
	page, err := challenges.Participants(t.Context(), creator, c.ID, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("participants = %+v, want the creator and the joiner", page.Items)
	}
	for _, p := range page.Items {
		if p.Name.Pseudo != nil || p.Name.FirstName == nil || *p.Name.FirstName != firstNames[p.UserID] {
			t.Fatalf("participant %+v, want no pseudo and first name %q", p, firstNames[p.UserID])
		}
	}

	board, err := leaderboards.Get(t.Context(), joiner, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, category := range board.Categories {
		if len(category.Entries) != 2 {
			t.Fatalf("%s entries = %+v, want both users", category.Type, category.Entries)
		}
		for _, e := range category.Entries {
			if e.Rank != 1 || e.Name.Pseudo != nil || e.Name.FirstName == nil || *e.Name.FirstName != firstNames[e.UserID] {
				t.Fatalf("%s entry %+v, want ranked 1 (tie) without a pseudo, first name %q", category.Type, e, firstNames[e.UserID])
			}
		}
	}
}
