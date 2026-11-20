//go:build integration

package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damchi/sport-challenge-api/internal/activity"
	"github.com/damchi/sport-challenge-api/internal/challenge"
	"github.com/damchi/sport-challenge-api/internal/closure"
	"github.com/damchi/sport-challenge-api/internal/invitation"
	"github.com/damchi/sport-challenge-api/internal/leaderboard"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
	"github.com/damchi/sport-challenge-api/internal/sport"
	"github.com/damchi/sport-challenge-api/internal/user"
)

// deletedAtAuth0 stands for the Auth0 Management API: every deletion works.
type deletedAtAuth0 struct{}

func (deletedAtAuth0) DeleteUser(context.Context, string) error { return nil }

// erasureFixture wires the modules as run does, at recordNow, around gone,
// the account to delete, so that gone ranks last everywhere (the others keep
// their ranks without gone):
//   - finished, created by gone alone, over before recordNow and frozen by
//     the worker: gone, alice and bob survive or not, carol and gone are
//     eliminated in week 2;
//   - ongoing, created by alice with gone as co-creator: alice, gone, and
//     dave who joined through gone's invitation, which carol opened without
//     joining;
//   - other, private, created by alice: gone and erin opened alice's
//     invitation.
type erasureFixture struct {
	t              *testing.T
	pool           *pgxpool.Pool
	users          *user.Service
	leaderboards   *leaderboard.Service
	erasers        []user.Eraser
	participations user.Participations

	gone, alice, bob, carol, dave, erin string
	finished, ongoing, other            string
	goneInvitation, aliceInvitation     string
}

func newErasureFixture(t *testing.T) erasureFixture {
	t.Helper()
	pool := testdb.New(t)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	gdb, err := db.NewGorm(pool, logger)
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(recordNow)
	var (
		challenges  *challenge.Service
		activities  *activity.Service
		invitations *invitation.Service
	)
	f := erasureFixture{t: t, pool: pool}
	f.participations = abandonAllFunc(func(ctx context.Context, userID string) error {
		return challenges.AbandonAll(ctx, userID)
	})
	f.erasers = []user.Eraser{
		eraseUserFunc(func(ctx context.Context, userID string) error { return invitations.EraseUser(ctx, userID) }),
		eraseUserFunc(func(ctx context.Context, userID string) error { return activities.EraseUser(ctx, userID) }),
		eraseUserFunc(func(ctx context.Context, userID string) error { return challenges.EraseUser(ctx, userID) }),
	}
	f.users = user.NewService(user.NewRepository(gdb), f.participations, f.erasers, deletedAtAuth0{}, clk, logger)
	sports := sport.NewService(sport.NewRepository(gdb))
	challenges = challenge.NewService(challenge.NewRepository(gdb), challengeProfiles{f.users}, sports, standingsFunc(nil), clk)
	activities = activity.NewService(activity.NewRepository(gdb), f.users, sports, participations{challenges}, activityScopes{challenges}, clk)
	invitations = invitation.NewService(invitationChallenges{challenges}, invitation.NewRepository(gdb), clk)
	f.leaderboards = leaderboard.NewService(leaderboardChallenges{challenges}, leaderboardProfiles{f.users}, leaderboard.NewRepository(gdb), clk, 24*time.Hour)

	insertUser := func(subject string) string {
		var id string
		if err := pool.QueryRow(t.Context(), "INSERT INTO users (auth_subject, pseudo, first_name, last_name, email) VALUES ($1, $1, 'Test', 'User', $1 || '@example.com') RETURNING id", subject).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	f.gone, f.alice, f.bob = insertUser("gone"), insertUser("alice"), insertUser("bob")
	f.carol, f.dave, f.erin = insertUser("carol"), insertUser("dave"), insertUser("erin")

	insertChallenge := func(visibility string, start time.Time, createdBy string, creators ...string) string {
		var id string
		if err := pool.QueryRow(t.Context(), `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by)
			VALUES ('Spring', $1, 'America/Toronto', $2, $3, $4) RETURNING id`, visibility, start, start.AddDate(0, 0, 21), createdBy).Scan(&id); err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct{ typ, params string }{
			{challenge.CategoryCumulativeTime, `{}`},
			{challenge.CategoryCumulativeDistance, `{"factors": {"run": 1}}`},
			{challenge.CategoryRunElimination, `{"sport": "run", "step_meters": 2000}`},
		} {
			f.exec(`INSERT INTO categories (challenge_id, type, position, params)
				VALUES ($1, $2, (SELECT count(*) + 1 FROM categories WHERE challenge_id = $1), $3::jsonb)`, id, c.typ, c.params)
		}
		for _, c := range creators {
			f.exec("INSERT INTO challenge_creators (challenge_id, user_id) VALUES ($1, $2)", id, c)
		}
		return id
	}
	join := func(challengeID string, start time.Time, users ...string) {
		for _, u := range users {
			f.exec("INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3)", challengeID, u, start)
		}
	}
	// run records a run of km kilometres per week from start, 30 min each
	// (gone: 10 min, so that it is last in time too).
	run := func(userID string, start time.Time, km ...int) {
		for week, n := range km {
			if n == 0 {
				continue
			}
			duration := 1800
			if userID == f.gone {
				duration = 600
			}
			f.exec(`INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters)
				VALUES ($1, 'run', $2, $3, $4)`, userID, start.AddDate(0, 0, 7*week+1).Add(7*time.Hour), duration, n*1000)
		}
	}

	finishedStart := toronto(t, 2026, 5, 4, 0)
	f.finished = insertChallenge("public", finishedStart, f.gone, f.gone)
	join(f.finished, finishedStart, f.gone, f.alice, f.bob, f.carol)
	run(f.alice, finishedStart, 5, 7, 9)
	run(f.bob, finishedStart, 4, 6, 8)
	run(f.carol, finishedStart, 3, 4)
	run(f.gone, finishedStart, 1)
	// An activity gone deleted is erased too.
	f.exec(`INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters, deleted_at)
		VALUES ($1, 'run', $2, 600, 1000, $3)`, f.gone, finishedStart.Add(30*time.Hour), finishedStart.Add(31*time.Hour))

	ongoingStart := toronto(t, 2026, 6, 1, 0)
	f.ongoing = insertChallenge("private", ongoingStart, f.alice, f.alice, f.gone)
	join(f.ongoing, ongoingStart, f.alice, f.gone, f.dave)
	run(f.alice, ongoingStart, 5, 6)
	run(f.dave, ongoingStart, 3)
	f.other = insertChallenge("private", ongoingStart, f.alice, f.alice)
	join(f.other, ongoingStart, f.alice)

	invite := func(challengeID, author, token string, recipients ...string) string {
		hash := sha256.Sum256([]byte(token))
		var id string
		if err := pool.QueryRow(t.Context(), `INSERT INTO invitations (challenge_id, invited_by, token_hash, expires_at, created_at)
			VALUES ($1, $2, $3, $4, $5) RETURNING id`, challengeID, author, hash[:], recordNow.Add(24*time.Hour), ongoingStart).Scan(&id); err != nil {
			t.Fatal(err)
		}
		for _, r := range recipients {
			f.exec("INSERT INTO invitation_recipients (invitation_id, user_id, created_at) VALUES ($1, $2, $3)", id, r, ongoingStart)
		}
		return id
	}
	f.goneInvitation = invite(f.ongoing, f.gone, "gone-token", f.carol, f.dave)
	f.aliceInvitation = invite(f.other, f.alice, "alice-token", f.gone, f.erin)

	// The worker freezes the finished challenge, as it runs in production.
	worker := closure.NewService(closure.NewRepository(gdb), leaderboard.NewRepository(gdb), clk, logger)
	if _, err := worker.CloseWeeks(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.FinalizeChallenges(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := f.count("SELECT count(*) FROM elimination WHERE challenge_id = $1", f.finished); n != 2 {
		t.Fatalf("%d eliminations in the finished challenge, want carol's and gone's", n)
	}
	return f
}

func (f erasureFixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.t.Context(), sql, args...); err != nil {
		f.t.Fatal(err)
	}
}

func (f erasureFixture) count(sql string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.pool.QueryRow(f.t.Context(), sql, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

// rowsOf counts the rows of userID in every table the deletion erases.
func (f erasureFixture) rowsOf(userID string) map[string]int {
	f.t.Helper()
	return map[string]int{
		"activities":            f.count("SELECT count(*) FROM activities WHERE user_id = $1", userID),
		"participants":          f.count("SELECT count(*) FROM participants WHERE user_id = $1", userID),
		"elimination":           f.count("SELECT count(*) FROM elimination WHERE user_id = $1", userID),
		"challenge_creators":    f.count("SELECT count(*) FROM challenge_creators WHERE user_id = $1", userID),
		"invitations":           f.count("SELECT count(*) FROM invitations WHERE invited_by = $1", userID),
		"invitation_recipients": f.count("SELECT count(*) FROM invitation_recipients WHERE user_id = $1", userID),
		"result_snapshot": f.count(`SELECT count(*) FROM result_snapshot rs, jsonb_array_elements(rs.payload->'entries') e
			WHERE e->>'user_id' = $1`, userID),
	}
}

// snapshotEntries returns the frozen entries of the finished challenge by
// category type.
func (f erasureFixture) snapshotEntries() map[string][]map[string]any {
	f.t.Helper()
	rows, err := f.pool.Query(f.t.Context(), "SELECT category_type, payload FROM result_snapshot WHERE challenge_id = $1", f.finished)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	out := map[string][]map[string]any{}
	for rows.Next() {
		var typ string
		var payload []byte
		if err := rows.Scan(&typ, &payload); err != nil {
			f.t.Fatal(err)
		}
		var p struct {
			Entries []map[string]any `json:"entries"`
		}
		if err := json.Unmarshal(payload, &p); err != nil {
			f.t.Fatal(err)
		}
		out[typ] = p.Entries
	}
	if err := rows.Err(); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f erasureFixture) leaderboard(challengeID string) leaderboard.Leaderboard {
	f.t.Helper()
	got, err := f.leaderboards.Get(f.t.Context(), f.alice, challengeID)
	if err != nil {
		f.t.Fatal(err)
	}
	return got
}

// without returns lb with the entries of userID left out.
func without(lb leaderboard.Leaderboard, userID string) leaderboard.Leaderboard {
	out := leaderboard.Leaderboard{ComputedAt: lb.ComputedAt}
	for _, c := range lb.Categories {
		kept := leaderboard.CategoryRanking{Type: c.Type}
		for _, e := range c.Entries {
			if e.UserID != userID {
				kept.Entries = append(kept.Entries, e)
			}
		}
		out.Categories = append(out.Categories, kept)
	}
	return out
}

func TestAccountDeletionErasesEveryRow(t *testing.T) {
	f := newErasureFixture(t)
	finishedBefore, ongoingBefore := f.leaderboard(f.finished), f.leaderboard(f.ongoing)
	snapshotBefore := f.snapshotEntries()
	others := map[string]map[string]int{}
	for _, u := range []string{f.alice, f.bob, f.carol, f.dave, f.erin} {
		others[u] = f.rowsOf(u)
	}
	for table, n := range f.rowsOf(f.gone) {
		// gone has rows everywhere, so that the erasure is seen in each table.
		if n == 0 {
			t.Fatalf("fixture: gone has no row in %s", table)
		}
	}

	if err := f.users.Delete(t.Context(), f.gone); err != nil {
		t.Fatal(err)
	}

	for table, n := range f.rowsOf(f.gone) {
		if n != 0 {
			t.Errorf("%d rows of the deleted account left in %s, want none", n, table)
		}
	}
	// Done at Auth0: the deletion recorded in the transaction is removed.
	if n := f.count("SELECT count(*) FROM pending_auth0_deletions"); n != 0 {
		t.Errorf("%d pending Auth0 deletions, want none", n)
	}
	// The rows of the others stay, but for the links to gone's invitation.
	others[f.carol]["invitation_recipients"]--
	others[f.dave]["invitation_recipients"]--
	for u, want := range others {
		if got := f.rowsOf(u); !reflect.DeepEqual(got, want) {
			t.Errorf("rows of %s = %v, want %v", u, got, want)
		}
	}
	// alice's invitation stays, erin still linked to it; dave, who joined
	// through gone's invitation, stays a participant.
	if n := f.count("SELECT count(*) FROM invitation_recipients WHERE invitation_id = $1", f.aliceInvitation); n != 1 {
		t.Errorf("%d links to alice's invitation, want erin's", n)
	}
	if n := f.count("SELECT count(*) FROM invitations WHERE id = $1", f.goneInvitation); n != 0 {
		t.Errorf("gone's invitation still there")
	}
	if n := f.count("SELECT count(*) FROM participants WHERE challenge_id = $1 AND user_id = $2 AND status = 'active'", f.ongoing, f.dave); n != 1 {
		t.Errorf("dave no longer an active participant of the ongoing challenge")
	}

	// The challenge gone created stays, created_by on the anonymized row.
	var createdBy string
	var anonymized bool
	if err := f.pool.QueryRow(t.Context(), `SELECT c.created_by, u.anonymized_at IS NOT NULL AND u.pseudo IS NULL AND u.email IS NULL
		FROM challenges c JOIN users u ON u.id = c.created_by WHERE c.id = $1 AND c.deleted_at IS NULL`, f.finished).Scan(&createdBy, &anonymized); err != nil {
		t.Fatalf("finished challenge: %v, want it kept", err)
	}
	if createdBy != f.gone || !anonymized {
		t.Errorf("created by %s (anonymized %t), want gone's anonymized row", createdBy, anonymized)
	}

	// The frozen entries of the others are kept as they were, in order.
	snapshotAfter := f.snapshotEntries()
	for typ, entries := range snapshotBefore {
		var want []map[string]any
		for _, e := range entries {
			if e["user_id"] != f.gone {
				want = append(want, e)
			}
		}
		if !reflect.DeepEqual(snapshotAfter[typ], want) {
			t.Errorf("%s frozen entries = %v, want %v", typ, snapshotAfter[typ], want)
		}
	}
	// The rankings the others see do not change.
	if got, want := f.leaderboard(f.finished), without(finishedBefore, f.gone); !reflect.DeepEqual(got, want) {
		t.Errorf("finished leaderboard = %+v, want %+v", got, want)
	}
	if got, want := f.leaderboard(f.ongoing), without(ongoingBefore, f.gone); !reflect.DeepEqual(got, want) {
		t.Errorf("ongoing leaderboard = %+v, want %+v", got, want)
	}
}

func TestAccountDeletionErasesNothingOnFailure(t *testing.T) {
	f := newErasureFixture(t)
	failure := errors.New("erasure failed")
	// The failure comes after every eraser ran in the transaction.
	erasers := append(f.erasers, eraseUserFunc(func(context.Context, string) error { return failure }))
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	gdb, err := db.NewGorm(f.pool, logger)
	if err != nil {
		t.Fatal(err)
	}
	users := user.NewService(user.NewRepository(gdb), f.participations, erasers, deletedAtAuth0{}, clock.NewFake(recordNow), logger)
	before, snapshotBefore := f.rowsOf(f.gone), f.snapshotEntries()

	if err := users.Delete(t.Context(), f.gone); !errors.Is(err, failure) {
		t.Fatalf("Delete() error = %v, want the failure", err)
	}

	if got := f.rowsOf(f.gone); !reflect.DeepEqual(got, before) {
		t.Errorf("rows of gone = %v, want %v: nothing erased", got, before)
	}
	if got := f.snapshotEntries(); !reflect.DeepEqual(got, snapshotBefore) {
		t.Errorf("frozen entries changed: %v, want %v", got, snapshotBefore)
	}
	if n := f.count("SELECT count(*) FROM users WHERE id = $1 AND anonymized_at IS NULL AND pseudo = 'gone'", f.gone); n != 1 {
		t.Errorf("account anonymized, want it untouched")
	}
	if n := f.count("SELECT count(*) FROM participants WHERE user_id = $1 AND status = 'abandoned'", f.gone); n != 0 {
		t.Errorf("%d abandons kept, want none", n)
	}
	if n := f.count("SELECT count(*) FROM pending_auth0_deletions"); n != 0 {
		t.Errorf("%d pending Auth0 deletions, want none: the deletion rolled back", n)
	}
}
