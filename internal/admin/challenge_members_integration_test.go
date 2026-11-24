//go:build integration

package admin

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/challenge"
	"github.com/damchi/sport-challenge-api/internal/closure"
	"github.com/damchi/sport-challenge-api/internal/leaderboard"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/sport"
)

// completeProfiles is module user for the challenges: every profile is
// complete, no name is read.
type completeProfiles struct{}

func (completeProfiles) ProfileComplete(context.Context, string) (bool, error) { return true, nil }

func (completeProfiles) Names(context.Context, []string) (map[string]challenge.Name, error) {
	return nil, nil
}

// member is the membership of an account in a challenge, as stored.
type member struct {
	Status      string
	JoinedAt    time.Time
	AbandonedAt *time.Time
	Creator     bool
}

// TestMemberCorrectionsFromTheDatabase: in a challenge whose two weeks
// are locked, a participant excluded in week 2 and reinstated is judged
// again from week 2; an account added after registration closed takes
// part; a creator excluded keeps the role; the last creator stays; a
// preview changes nothing.
func TestMemberCorrectionsFromTheDatabase(t *testing.T) {
	r, pool := newRepository(t)
	gdb := r.db
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	// Three weeks from Monday 2026-04-06, no change of time; week n locks
	// at 7 am on the first day of week n+1.
	start := time.Date(2026, 4, 6, 0, 0, 0, 0, loc)
	locked := func(n int) time.Time { return time.Date(2026, 4, 6+7*n, 7, 0, 0, 0, loc).UTC() }
	created := start.AddDate(0, 0, -7).UTC()
	ann := insertUser(t, pool, "email|ann", created, false)
	bob := insertUser(t, pool, "email|bob", created, false)
	dan := insertUser(t, pool, "email|dan", created, false)
	gone := insertUser(t, pool, "email|gone", created, true)
	id := insertChallengeRow(t, pool, ann, challengeRow{title: "Spring", visibility: "private", startsAt: start.UTC(), weeks: 3, createdAt: created})
	exec(t, pool, `INSERT INTO categories (challenge_id, type, position, params) VALUES ($1, 'cumulative_time', 1, '{}'),
		($1, 'cumulative_distance', 2, '{"factors": {"run": 1}}'),
		($1, 'elimination', 3, '{"sport": "run", "step_meters": 2000}')`, id)
	// bob, excluded in week 2, ran under the minimum of week 2 (7 km).
	exec(t, pool, `INSERT INTO participants (challenge_id, user_id, joined_at, status, abandoned_at) VALUES ($1, $2, $3, 'abandoned', $4)`,
		id, bob, start.UTC(), start.AddDate(0, 0, 10).UTC())
	for _, a := range []struct {
		user         string
		week, meters int
	}{{ann, 1, 5000}, {bob, 1, 5000}, {ann, 2, 7000}, {bob, 2, 6000}} {
		exec(t, pool, `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters) VALUES ($1, 'run', $2, 1800, $3)`,
			a.user, start.AddDate(0, 0, 7*(a.week-1)).Add(10*time.Hour), a.meters)
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	now := locked(2).Add(24 * time.Hour)
	clk := clock.NewFake(now)
	worker := closure.NewService(closure.NewRepository(gdb), leaderboard.NewRepository(gdb), clk, logger)
	if _, err := worker.CloseWeeks(t.Context()); err != nil {
		t.Fatal(err)
	}
	challenges := challenge.NewService(challenge.NewRepository(gdb), completeProfiles{}, sport.NewService(sport.NewRepository(gdb)), nil, clk)
	corrections := NewMemberCorrections(NewCorrections(r, NewJournal(r, clk), worker, noBoards{}), challenges)
	admin := auth.Actor{ID: dan, Admin: true}

	read := func(userID string) member {
		t.Helper()
		var m member
		err := pool.QueryRow(t.Context(), `SELECT COALESCE(p.status, ''), COALESCE(p.joined_at, 'epoch'), p.abandoned_at,
			EXISTS (SELECT 1 FROM challenge_creators WHERE challenge_id = $1 AND user_id = $2)
			FROM (SELECT 1) one LEFT JOIN participants p ON p.challenge_id = $1 AND p.user_id = $2`, id, userID).
			Scan(&m.Status, &m.JoinedAt, &m.AbandonedAt, &m.Creator)
		if err != nil {
			t.Fatal(err)
		}
		m.JoinedAt = m.JoinedAt.UTC()
		return m
	}
	actions := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM admin_actions`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	eliminations := func() []EliminationRow {
		t.Helper()
		rows, err := r.Eliminations(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	// The journal lines share the instant of the fake clock: each one is
	// found by its action, every action made once at most.
	journalLine := func(action string) Action {
		t.Helper()
		list, err := r.ListActions(t.Context(), ActionQuery{Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range list {
			if a.Action == action {
				return a
			}
		}
		t.Fatalf("no journal line %s", action)
		return Action{}
	}
	bobOut := []EliminationRow{{User: UserRef{ID: bob}, WeekNumber: 2, DistanceMeters: 6000, RequiredMeters: 7000}}
	if got := eliminations(); len(got) != 0 {
		t.Fatalf("eliminations %+v, want none: bob abandoned before week 2 was judged", got)
	}

	t.Run("a preview changes nothing", func(t *testing.T) {
		before, n := read(bob), actions()
		previews, err := corrections.ReinstateParticipant(t.Context(), admin, id, bob, "Exclu par erreur", true)
		if err != nil {
			t.Fatal(err)
		}
		if len(previews) != 1 || !reflect.DeepEqual(previews[0].Eliminations, []EliminationChange{{User: bobOut[0].User, After: &bobOut[0]}}) {
			t.Fatalf("previews %+v, want bob eliminated in week 2", previews)
		}
		if got := read(bob); !reflect.DeepEqual(got, before) || actions() != n || len(eliminations()) != 0 {
			t.Fatalf("after a preview: %+v, %d actions, want %+v, %d", got, actions(), before, n)
		}
	})

	t.Run("reinstated after a locked week, judged again from it", func(t *testing.T) {
		if _, err := corrections.ReinstateParticipant(t.Context(), admin, id, bob, "Exclu par erreur", false); err != nil {
			t.Fatal(err)
		}
		if got := read(bob); got.Status != "active" || got.AbandonedAt != nil {
			t.Fatalf("bob %+v, want active again", got)
		}
		if got := eliminations(); !reflect.DeepEqual(got, bobOut) {
			t.Fatalf("eliminations %+v, want %+v", got, bobOut)
		}
		if a := journalLine(ActionParticipantReinstate); a.TargetType != TargetParticipant || a.TargetID != bob ||
			!reflect.DeepEqual(a.ChallengeIDs, []string{id}) || string(a.OldValue) != `{"status": "abandoned"}` || string(a.NewValue) != `{"status": "active"}` {
			t.Fatalf("journal line %+v %s %s", a, a.OldValue, a.NewValue)
		}
	})

	t.Run("added after registration closed", func(t *testing.T) {
		if _, err := corrections.AddParticipant(t.Context(), admin, id, dan, "Oubli d'inscription", false); err != nil {
			t.Fatal(err)
		}
		if got := read(dan); got.Status != "active" || !got.JoinedAt.Equal(now) || got.Creator {
			t.Fatalf("dan %+v, want an active participant since %v", got, now)
		}
		if a := journalLine(ActionParticipantAdd); a.TargetID != dan || a.OldValue != nil || string(a.NewValue) != `{"status": "active"}` {
			t.Fatalf("journal line %+v %s %s", a, a.OldValue, a.NewValue)
		}
		_, err := corrections.AddParticipant(t.Context(), admin, id, dan, "Oubli d'inscription", false)
		if !errors.Is(err, apperr.New(apperr.CodeAlreadyParticipant, nil)) {
			t.Fatalf("added twice: %v, want already_participant", err)
		}
		_, err = corrections.AddParticipant(t.Context(), admin, id, gone, "Oubli d'inscription", false)
		if !errors.Is(err, apperr.New(apperr.CodeUserNotFound, nil)) {
			t.Fatalf("a deleted account: %v, want user_not_found", err)
		}
	})

	t.Run("a creator excluded keeps the role; the last creator stays", func(t *testing.T) {
		if _, err := corrections.ExcludeParticipant(t.Context(), admin, id, ann, "Tricherie", false); err != nil {
			t.Fatal(err)
		}
		if got := read(ann); got.Status != "abandoned" || got.AbandonedAt == nil || !got.AbandonedAt.Equal(now) || !got.Creator {
			t.Fatalf("ann %+v, want abandoned at %v, still a creator", got, now)
		}
		n := actions()
		_, err := corrections.RemoveCreator(t.Context(), admin, id, ann, "Tricherie", false)
		if !errors.Is(err, apperr.New(apperr.CodeLastCreator, nil)) || !read(ann).Creator || actions() != n {
			t.Fatalf("last creator removed: %v, want last_creator, nothing written", err)
		}
		// An admin who is no member of the challenge, whose membership is
		// read and left aside.
		outsider := auth.Actor{ID: insertUser(t, pool, "email|eve", created, false), Admin: true}
		if _, err := corrections.AddCreator(t.Context(), outsider, id, bob, "Nouveau créateur", false); err != nil {
			t.Fatal(err)
		}
		if _, err := corrections.RemoveCreator(t.Context(), admin, id, ann, "Tricherie", false); err != nil {
			t.Fatal(err)
		}
		if a, b := read(ann), read(bob); a.Creator || a.Status != "abandoned" || !b.Creator {
			t.Fatalf("ann %+v, bob %+v, want bob the creator, ann out", a, b)
		}
		_, err = corrections.AddCreator(t.Context(), admin, id, gone, "Nouveau créateur", false)
		if !errors.Is(err, apperr.New(apperr.CodeNotActiveParticipant, nil)) {
			t.Fatalf("a deleted account made a creator: %v, want not_active_participant", err)
		}
	})
}
