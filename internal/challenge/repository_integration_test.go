//go:build integration

package challenge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
	"github.com/damchi/sport-challenge-api/internal/scoring"
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

func (p sqlProfiles) Names(ctx context.Context, ids []string) (map[string]Name, error) {
	rows, err := p.pool.Query(ctx, "SELECT id::text, pseudo, first_name, last_name FROM users WHERE id = ANY($1::uuid[]) AND anonymized_at IS NULL", ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := map[string]Name{}
	for rows.Next() {
		var id string
		var n Name
		if err := rows.Scan(&id, &n.Pseudo, &n.FirstName, &n.LastName); err != nil {
			return nil, err
		}
		names[id] = n
	}
	return names, rows.Err()
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

func (c sqlCatalogue) HasExercise(ctx context.Context, sportCode, code string) (bool, error) {
	var found bool
	err := c.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM exercises WHERE sport_code = $1 AND code = $2)", sportCode, code).Scan(&found)
	return found, err
}

func (c sqlCatalogue) DistanceMode(ctx context.Context, code string) (string, bool, error) {
	var mode string
	err := c.pool.QueryRow(ctx, "SELECT distance_mode FROM sports WHERE code = $1", code).Scan(&mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return mode, err == nil, err
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
	svc := NewService(NewRepository(gdb), sqlProfiles{pool}, sqlCatalogue{pool}, fakeStandings{}, clock.NewFake(now))
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
		(SELECT params->>'sport' FROM categories WHERE challenge_id = $1 AND type = 'elimination'),
		(SELECT (params->>'step_meters')::float8 FROM categories WHERE challenge_id = $1 AND type = 'elimination')`, c.ID).Scan(&ride, &walk, &sport, &step); err != nil {
		t.Fatal(err)
	}
	if ride != 0.25 || walk != 1 || sport != "run" || step != 2000 {
		t.Fatalf("frozen params ride %v walk %v sport %s step %v, want 0.25, the suggested 1, run and 2000", ride, walk, sport, step)
	}
}

// TestCreateStoresTheReferenceSport: with the catalogue of the migrations,
// the factors are brought back to the reference, the elimination is run in
// it, and the reference is stored and read back; run when none is given.
func TestCreateStoresTheReferenceSport(t *testing.T) {
	pseudo := "marie_92"
	svc, pool, userID := setup(t, &pseudo)
	tests := []struct {
		reference   string
		want        string
		wantFactors map[string]float64
	}{
		{reference: "", want: "run", wantFactors: map[string]float64{"run": 1, "walk": 1, "ride": 0.3, "rowing": 0.5}},
		{reference: "ride", want: "ride", wantFactors: map[string]float64{"run": 1, "walk": 1, "ride": 1, "rowing": 1}},
		{reference: "rowing", want: "rowing", wantFactors: map[string]float64{"run": 1, "ride": 0.6, "rowing": 1, "xc_ski": 1, "hyrox": 1, "paragliding": 0.6}},
		// SC-API-174: hyrox (×1) and paragliding (×0.3) may be the reference.
		{reference: "hyrox", want: "hyrox", wantFactors: map[string]float64{"run": 1, "ride": 0.3, "rowing": 0.5, "hyrox": 1, "paragliding": 0.3}},
		{reference: "paragliding", want: "paragliding", wantFactors: map[string]float64{"run": 1, "ride": 1, "rowing": 1, "hyrox": 1, "paragliding": 1}},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			c, err := svc.Create(t.Context(), userID, with(func(d *Draft) {
				if tt.reference != "" {
					d.ReferenceSport = sportCode(tt.reference)
				}
			}))
			if err != nil {
				t.Fatal(err)
			}
			var stored, sport string
			if err := pool.QueryRow(t.Context(), `SELECT c.reference_sport, cat.params->>'sport'
				FROM challenges c JOIN categories cat ON cat.challenge_id = c.id AND cat.type = 'elimination'
				WHERE c.id = $1`, c.ID).Scan(&stored, &sport); err != nil {
				t.Fatal(err)
			}
			if stored != tt.want || sport != tt.want {
				t.Fatalf("stored reference %s, elimination in %s, want %s", stored, sport, tt.want)
			}
			for code, want := range tt.wantFactors {
				var f float64
				if err := pool.QueryRow(t.Context(), "SELECT (params->'factors'->>$2)::float8 FROM categories WHERE challenge_id = $1 AND type = 'cumulative_distance'", c.ID, code).Scan(&f); err != nil {
					t.Fatal(err)
				}
				if f != want {
					t.Errorf("factor of %s = %v, want %v", code, f, want)
				}
			}
			d, err := svc.Get(t.Context(), auth.Actor{ID: userID}, c.ID)
			if err != nil {
				t.Fatal(err)
			}
			if d.ReferenceSport != tt.want {
				t.Fatalf("reference read back %q, want %s", d.ReferenceSport, tt.want)
			}
		})
	}
}

// TestCreateStoresAReferenceWithoutDistance: with the catalogue of the
// migrations, a crossfit challenge (no distance, SC-API-212) is created and
// read back with points but no factor frozen anywhere.
func TestCreateStoresAReferenceWithoutDistance(t *testing.T) {
	pseudo := "marie_92"
	svc, pool, userID := setup(t, &pseudo)
	c, err := svc.Create(t.Context(), userID, noDistance("crossfit", func(d *Draft) {
		d.Points = points(scoring.CriterionLongestDuration, scoring.CriterionSessionCount)
	}))
	if err != nil {
		t.Fatal(err)
	}
	d, err := svc.Get(t.Context(), auth.Actor{ID: userID}, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.ReferenceSport != "crossfit" {
		t.Fatalf("reference read back %q, want crossfit", d.ReferenceSport)
	}
	if n := count(t, pool, "SELECT count(*) FROM categories WHERE challenge_id = $1", c.ID); n != 2 {
		t.Fatalf("%d categories, want the cumulative time and the points", n)
	}
	if n := count(t, pool, "SELECT count(*) FROM categories WHERE challenge_id = $1 AND params ? 'factors'", c.ID); n != 0 {
		t.Fatalf("%d categories with factors, want none", n)
	}
}

// TestCreateRefusesAReferenceWithoutRequiredDistance: with the catalogue of
// the migrations, other (optional) and an unknown code.
func TestCreateRefusesAReferenceWithoutRequiredDistance(t *testing.T) {
	pseudo := "marie_92"
	svc, pool, userID := setup(t, &pseudo)
	for _, reference := range []string{"other", "quidditch"} {
		t.Run(reference, func(t *testing.T) {
			_, err := svc.Create(t.Context(), userID, with(func(d *Draft) { d.ReferenceSport = sportCode(reference) }))
			var appErr *apperr.Error
			if !errors.As(err, &appErr) || appErr.Code != apperr.CodeValidationFailed || appErr.Params[apperr.FieldsParam] != "reference_sport" {
				t.Fatalf("error %v, want 400 validation_failed on reference_sport", err)
			}
		})
	}
	if n := count(t, pool, "SELECT count(*) FROM challenges"); n != 0 {
		t.Fatalf("%d challenges stored, want none", n)
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
	if want := []string{"1 cumulative_time", "2 cumulative_distance", "3 elimination"}; !slices.Equal(got, want) {
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
	abandonedAt := now
	want := map[string]Membership{
		created.ID: {Creator: true, Participant: ParticipantActive},
		joined.ID:  {Participant: ParticipantAbandoned, AbandonedAt: &abandonedAt},
	}
	if len(got) != len(want) {
		t.Fatalf("Memberships() = %+v, want %d challenges", got, len(want))
	}
	factors := created.Categories[1].Params.Factors
	for _, m := range got {
		if w, ok := want[m.ID]; !ok || withoutAbandonedAt(m.Membership) != withoutAbandonedAt(w) || !sameInstant(m.AbandonedAt, w.AbandonedAt) ||
			m.Timezone != created.Timezone || !m.StartsAt.Equal(created.StartsAt) || !m.EndsAt.Equal(created.EndsAt) {
			t.Fatalf("membership %+v, want %+v with the window of the challenge", m, w)
		}
		// The title and the factors of the distance category, frozen.
		if m.Title != created.Title || len(factors) == 0 || !maps.Equal(m.Factors, factors) {
			t.Fatalf("membership %+v, want the title %q and the factors %v", m, created.Title, factors)
		}
	}

	// Without a distance category (never the case of a challenge created by
	// the API), a challenge is still listed, with no factors: the week lock
	// reads every challenge joined.
	if _, err := pool.Exec(t.Context(), "DELETE FROM categories WHERE challenge_id = $1 AND type = $2", joined.ID, CategoryCumulativeDistance); err != nil {
		t.Fatal(err)
	}
	got, err = svc.Memberships(t.Context(), creatorID)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(got))
	for _, m := range got {
		ids = append(ids, m.ID)
		if m.ID == joined.ID && m.Factors != nil {
			t.Fatalf("membership %+v, want no factors without a distance category", m)
		}
	}
	if !slices.Contains(ids, joined.ID) || len(got) != len(want) {
		t.Fatalf("Memberships() = %v, want every challenge joined, %s included", ids, joined.ID)
	}
}

// withoutAbandonedAt is m without its instant of abandon, compared apart
// (sameInstant): a pointer to a time read back has another location.
func withoutAbandonedAt(m Membership) Membership {
	m.AbandonedAt = nil
	return m
}

// sameInstant reports whether a and b are both nil or the same instant.
func sameInstant(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// TestCreateStoresAnEliminationByRepetitions: CrossFit with wall balls, X =
// 10, read back with its exercise; an admin then changes the exercise,
// checked against the catalogue of the migrations.
func TestCreateStoresAnEliminationByRepetitions(t *testing.T) {
	pseudo := "marie_92"
	svc, _, userID := setup(t, &pseudo)
	c, err := svc.Create(t.Context(), userID, noDistance("crossfit", repetitions("wall_ball", 10)))
	if err != nil {
		t.Fatal(err)
	}
	eliminationOf := func() Params {
		t.Helper()
		d, err := svc.Get(t.Context(), auth.Actor{ID: userID}, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(d.Categories) != 2 || d.Categories[1].Type != CategoryElimination {
			t.Fatalf("categories %+v, want the cumulative time and the elimination", d.Categories)
		}
		return d.Categories[1].Params
	}
	if got, want := eliminationOf(), (Params{Sport: "crossfit", Exercise: "wall_ball", StepRepetitions: 10}); !reflect.DeepEqual(got, want) {
		t.Fatalf("elimination read back %+v, want %+v", got, want)
	}

	admin := auth.Actor{ID: userID, Admin: true}
	choice := func(exercise string) SettingsChanges {
		return SettingsChanges{Categories: &CategoryChoice{Elimination: &EliminationDraft{Exercise: exercise, StepRepetitions: 20}}}
	}
	_, err = svc.AdminUpdate(t.Context(), admin, c.ID, choice("squat"))
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Params[apperr.FieldsParam] != "categories.elimination.exercise" {
		t.Fatalf("AdminUpdate(squat, no exercise of the catalogue) = %v, want 400 on categories.elimination.exercise", err)
	}
	if _, err := svc.AdminUpdate(t.Context(), admin, c.ID, choice("burpee")); err != nil {
		t.Fatal(err)
	}
	if got, want := eliminationOf(), (Params{Sport: "crossfit", Exercise: "burpee", StepRepetitions: 20}); !reflect.DeepEqual(got, want) {
		t.Fatalf("elimination after the admin change %+v, want %+v", got, want)
	}
	// Back to run, the categories kept: repetitions are refused with a distance.
	_, err = svc.AdminUpdate(t.Context(), admin, c.ID, SettingsChanges{ReferenceSport: sportCode("run")})
	if !errors.As(err, &appErr) || appErr.Params[apperr.FieldsParam] != "categories.elimination.exercise,categories.elimination.step_meters,categories.elimination.step_repetitions" {
		t.Fatalf("AdminUpdate(run) = %v, want 400 on the three elimination fields", err)
	}
}

// TestMembershipsGiveTheExercise: the reference sport and the exercise of an
// elimination by repetitions, none for one in meters or without elimination;
// one row per challenge.
func TestMembershipsGiveTheExercise(t *testing.T) {
	pseudo := "marie_92"
	svc, _, userID := setup(t, &pseudo)
	want := map[string][2]string{}
	for _, d := range []Draft{noDistance("crossfit", repetitions("wall_ball", 10)), valid(), noDistance("crossfit", nil)} {
		c, err := svc.Create(t.Context(), userID, d)
		if err != nil {
			t.Fatal(err)
		}
		want[c.ID] = [2]string{d.reference(), ""}
		if d.Elimination != nil {
			want[c.ID] = [2]string{d.reference(), d.Elimination.Exercise}
		}
	}
	got, err := svc.Memberships(t.Context(), userID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range got {
		if w, ok := want[m.ID]; !ok || [2]string{m.Reference, m.Exercise} != w {
			t.Fatalf("membership %s: %q %q, want %v", m.ID, m.Reference, m.Exercise, w)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("%d memberships, want %d", len(got), len(want))
	}
}
