//go:build integration

package challenge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
)

// The modules user and sport are read through SQL here: a module never
// imports another module's repository.
type sqlProfiles struct{ pool *pgxpool.Pool }

// ProfileComplete answers as user.Service does: an anonymized account is
// deleted, not incomplete.
func (p sqlProfiles) ProfileComplete(ctx context.Context, id string) (bool, error) {
	var complete, anonymized bool
	err := p.pool.QueryRow(ctx, "SELECT pseudo IS NOT NULL, anonymized_at IS NOT NULL FROM users WHERE id = $1", id).Scan(&complete, &anonymized)
	if err != nil {
		return false, err
	}
	if anonymized {
		return false, apperr.New(apperr.CodeAccountDeleted, nil)
	}
	return complete, nil
}

func (p sqlProfiles) Pseudos(ctx context.Context, ids []string) (map[string]string, error) {
	rows, err := p.pool.Query(ctx, "SELECT id::text, pseudo FROM users WHERE id = ANY($1::uuid[]) AND anonymized_at IS NULL AND pseudo IS NOT NULL", ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pseudos := map[string]string{}
	for rows.Next() {
		var id, pseudo string
		if err := rows.Scan(&id, &pseudo); err != nil {
			return nil, err
		}
		pseudos[id] = pseudo
	}
	return pseudos, rows.Err()
}

type sqlCatalogue struct{ pool *pgxpool.Pool }

func (c sqlCatalogue) SuggestedFactors(ctx context.Context) (map[string]float64, error) {
	rows, err := c.pool.Query(ctx, "SELECT code, suggested_factor::float8 FROM sports WHERE suggested_factor IS NOT NULL")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	factors := map[string]float64{}
	for rows.Next() {
		var code string
		var f float64
		if err := rows.Scan(&code, &f); err != nil {
			return nil, err
		}
		factors[code] = f
	}
	return factors, rows.Err()
}

func setup(t *testing.T, pseudo *string) (*Service, *pgxpool.Pool, string) {
	t.Helper()
	pool := testdb.New(t)
	gdb, err := db.NewGorm(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	var userID string
	if err := pool.QueryRow(t.Context(), "INSERT INTO users (auth_subject, pseudo) VALUES ('email|creator', $1) RETURNING id", pseudo).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	svc := NewService(NewRepository(gdb), sqlProfiles{pool}, sqlCatalogue{pool}, clock.NewFake(now))
	return svc, pool, userID
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCreateStoresEverything(t *testing.T) {
	pseudo := "marie_92"
	svc, pool, userID := setup(t, &pseudo)

	c, err := svc.Create(t.Context(), userID, with(func(d *Draft) {
		d.Title = " Spring run "
		d.DistanceFactors = map[string]float64{"ride": 0.25}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == "" || c.CreatedAt.IsZero() || len(c.Categories) != 3 || c.Categories[0].ID == "" {
		t.Fatalf("challenge %+v, want the ids and timestamps set by the database", c)
	}

	var title, visibility, timezone string
	if err := pool.QueryRow(t.Context(), "SELECT title, visibility, timezone FROM challenges WHERE id = $1 AND created_by = $2", c.ID, userID).Scan(&title, &visibility, &timezone); err != nil {
		t.Fatal(err)
	}
	if title != "Spring run" || visibility != "public" || timezone != "America/Toronto" {
		t.Fatalf("stored %q %s %s, want the trimmed title and the draft", title, visibility, timezone)
	}
	if n := count(t, pool, "SELECT count(*) FROM challenge_creators WHERE challenge_id = $1 AND user_id = $2", c.ID, userID); n != 1 {
		t.Fatalf("%d creator rows, want 1", n)
	}
	if n := count(t, pool, "SELECT count(*) FROM participants WHERE challenge_id = $1 AND user_id = $2 AND status = 'active' AND joined_at = $3", c.ID, userID, now); n != 1 {
		t.Fatalf("%d active participations joined at the clock's now, want 1", n)
	}

	var ride, walk, step float64
	var sport string
	if err := pool.QueryRow(t.Context(), `SELECT
		(SELECT (params->'factors'->>'ride')::float8 FROM categories WHERE challenge_id = $1 AND type = 'cumulative_distance'),
		(SELECT (params->'factors'->>'walk')::float8 FROM categories WHERE challenge_id = $1 AND type = 'cumulative_distance'),
		(SELECT params->>'sport' FROM categories WHERE challenge_id = $1 AND type = 'run_elimination'),
		(SELECT (params->>'step_meters')::float8 FROM categories WHERE challenge_id = $1 AND type = 'run_elimination')`, c.ID).Scan(&ride, &walk, &sport, &step); err != nil {
		t.Fatal(err)
	}
	if ride != 0.25 || walk != 1 || sport != "run" || step != 2000 {
		t.Fatalf("frozen params ride %v walk %v sport %s step %v, want 0.25, the suggested 1, run and 2000", ride, walk, sport, step)
	}
}

func TestCreateStoresAPrivateChallenge(t *testing.T) {
	pseudo := "marie_92"
	svc, pool, userID := setup(t, &pseudo)
	desc := "Line 1\nLine 2"
	c, err := svc.Create(t.Context(), userID, with(func(d *Draft) {
		d.Visibility = VisibilityPrivate
		d.MembersCanInvite = true
		d.Description = &desc
		d.DistanceFactors = map[string]float64{"other": 0.5}
	}))
	if err != nil {
		t.Fatal(err)
	}

	var visibility, description string
	var canInvite bool
	var startsAt, endsAt time.Time
	if err := pool.QueryRow(t.Context(), "SELECT visibility, members_can_invite, description, starts_at, ends_at FROM challenges WHERE id = $1", c.ID).
		Scan(&visibility, &canInvite, &description, &startsAt, &endsAt); err != nil {
		t.Fatal(err)
	}
	if visibility != "private" || !canInvite || description != desc {
		t.Fatalf("stored %s, can invite %v, %q, want the private draft", visibility, canInvite, description)
	}
	// The window read back from timestamptz: local midnights of Toronto.
	if !startsAt.Equal(time.Date(2026, 3, 4, 5, 0, 0, 0, time.UTC)) || !endsAt.Equal(time.Date(2026, 4, 15, 4, 0, 0, 0, time.UTC)) {
		t.Fatalf("stored window [%s, %s), want 6 local weeks from 2026-03-04", startsAt, endsAt)
	}

	// The 3 categories, in order, with their frozen params.
	rows, err := pool.Query(t.Context(), "SELECT type, position, params::text FROM categories WHERE challenge_id = $1 ORDER BY position", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var typ, params string
		var pos int
		if err := rows.Scan(&typ, &pos, &params); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%d %s", pos, typ))
		if typ == CategoryCumulativeTime && params != "{}" {
			t.Errorf("cumulative_time params %s, want {}", params)
		}
	}
	if want := []string{"1 cumulative_time", "2 cumulative_distance", "3 run_elimination"}; !slices.Equal(got, want) {
		t.Fatalf("categories %v, want %v", got, want)
	}

	var run, other float64
	if err := pool.QueryRow(t.Context(), "SELECT (params->'factors'->>'run')::float8, (params->'factors'->>'other')::float8 FROM categories WHERE challenge_id = $1 AND type = 'cumulative_distance'", c.ID).Scan(&run, &other); err != nil {
		t.Fatal(err)
	}
	if run != 1 || other != 0.5 {
		t.Fatalf("frozen run %v, other %v, want 1 and the creator's 0.5 (D9)", run, other)
	}
}

func TestCreateFreezesTheFactors(t *testing.T) {
	pseudo := "marie_92"
	svc, pool, userID := setup(t, &pseudo)
	c, err := svc.Create(t.Context(), userID, valid())
	if err != nil {
		t.Fatal(err)
	}

	// The catalogue changes after the creation: the challenge keeps its copy.
	if _, err := pool.Exec(t.Context(), "UPDATE sports SET suggested_factor = 0.8 WHERE code = 'ride'"); err != nil {
		t.Fatal(err)
	}
	var ride float64
	if err := pool.QueryRow(t.Context(), "SELECT (params->'factors'->>'ride')::float8 FROM categories WHERE challenge_id = $1 AND type = 'cumulative_distance'", c.ID).Scan(&ride); err != nil {
		t.Fatal(err)
	}
	if ride != 0.3 {
		t.Fatalf("ride factor %v after the catalogue change, want the frozen 0.3", ride)
	}
}

func TestCreateRefusalsStoreNothing(t *testing.T) {
	tests := []struct {
		name     string
		pseudo   *string
		draft    Draft
		wantCode apperr.Code
	}{
		{name: "incomplete profile", draft: valid(), wantCode: apperr.CodeProfileIncomplete},
		{name: "factor out of range", pseudo: new("marie_92"), draft: with(func(d *Draft) { d.DistanceFactors = map[string]float64{"ride": 2.5} }), wantCode: apperr.CodeValidationFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, pool, userID := setup(t, tt.pseudo)
			_, err := svc.Create(t.Context(), userID, tt.draft)
			if !errors.Is(err, apperr.New(tt.wantCode, nil)) {
				t.Fatalf("error = %v, want %s", err, tt.wantCode)
			}
			if n := count(t, pool, "SELECT count(*) FROM challenges"); n != 0 {
				t.Fatalf("%d challenges stored, want none", n)
			}
		})
	}
}

// TestCreateIsAtomic makes the last write of the transaction fail (a
// category position the schema refuses): nothing may remain.
func TestCreateIsAtomic(t *testing.T) {
	pool := testdb.New(t)
	gdb, err := db.NewGorm(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	var userID string
	if err := pool.QueryRow(t.Context(), "INSERT INTO users (auth_subject, pseudo) VALUES ('email|atomic', 'atomic') RETURNING id").Scan(&userID); err != nil {
		t.Fatal(err)
	}

	_, err = NewRepository(gdb).Create(t.Context(), Challenge{
		Title: "Atomic", Visibility: VisibilityPublic, Timezone: "UTC",
		StartsAt: now, EndsAt: now.AddDate(0, 0, 14), CreatedBy: userID,
		Categories: []Category{{Type: CategoryCumulativeTime, Position: 4}},
	}, now)
	if err == nil {
		t.Fatal("Create() succeeded with an invalid category")
	}
	for _, table := range []string{"challenges", "challenge_creators", "participants", "categories"} {
		if n := count(t, pool, "SELECT count(*) FROM "+table); n != 0 {
			t.Errorf("%d rows left in %s after the failed transaction, want 0", n, table)
		}
	}
}

func TestMembershipsListsTheChallengesJoined(t *testing.T) {
	pseudo := "marie_92"
	svc, pool, creatorID := setup(t, &pseudo)
	created, err := svc.Create(t.Context(), creatorID, valid())
	if err != nil {
		t.Fatal(err)
	}
	other := insertTestUser(t, pool, "email|other")
	if _, err := pool.Exec(t.Context(), "UPDATE users SET pseudo = 'other' WHERE id = $1", other); err != nil {
		t.Fatal(err)
	}
	joined, err := svc.Create(t.Context(), other, valid())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "INSERT INTO participants VALUES ($1, $2, 'abandoned', $3, $3)", joined.ID, creatorID, now); err != nil {
		t.Fatal(err)
	}
	deleted, err := svc.Create(t.Context(), creatorID, valid())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE challenges SET deleted_at = now() WHERE id = $1", deleted.ID); err != nil {
		t.Fatal(err)
	}
	// Not joined: left out.
	if _, err := svc.Create(t.Context(), other, valid()); err != nil {
		t.Fatal(err)
	}

	got, err := svc.Memberships(t.Context(), creatorID)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Membership{
		created.ID: {Creator: true, Participant: ParticipantActive},
		joined.ID:  {Participant: ParticipantAbandoned},
	}
	if len(got) != len(want) {
		t.Fatalf("Memberships() = %+v, want %d challenges", got, len(want))
	}
	factors := created.Categories[1].Params.Factors
	for _, m := range got {
		if w, ok := want[m.ID]; !ok || m.Membership != w || m.Timezone != created.Timezone || !m.StartsAt.Equal(created.StartsAt) || !m.EndsAt.Equal(created.EndsAt) {
			t.Fatalf("membership %+v, want %+v with the window of the challenge", m, w)
		}
		// The title and the factors of the distance category, frozen.
		if m.Title != created.Title || len(factors) == 0 || !maps.Equal(m.Factors, factors) {
			t.Fatalf("membership %+v, want the title %q and the factors %v", m, created.Title, factors)
		}
	}
}
