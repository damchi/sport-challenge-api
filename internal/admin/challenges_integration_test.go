//go:build integration

package admin

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// challengeRow is a challenge to insert.
type challengeRow struct {
	title, visibility string
	startsAt          time.Time
	weeks             int
	createdAt         time.Time
	deletedAt         *time.Time
}

func insertChallengeRow(t *testing.T, pool *pgxpool.Pool, creator string, c challengeRow) string {
	t.Helper()
	var id string
	err := pool.QueryRow(t.Context(), `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by, created_at, deleted_at)
		VALUES ($1, $2, 'America/Toronto', $3, $4, $5, $6, $7) RETURNING id`,
		c.title, c.visibility, c.startsAt, c.startsAt.AddDate(0, 0, 7*c.weeks), creator, c.createdAt, c.deletedAt,
	).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	exec(t, pool, `INSERT INTO challenge_creators (challenge_id, user_id, created_at) VALUES ($1, $2, $3)`, id, creator, c.createdAt)
	exec(t, pool, `INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3)`, id, creator, c.createdAt)
	return id
}

func ids(rows []ChallengeRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

func TestRepositoryListChallenges(t *testing.T) {
	r, pool := newRepository(t)
	now := time.Date(2026, 11, 23, 15, 0, 0, 0, time.UTC)
	old := now.AddDate(0, -2, 0)
	ann := insertUser(t, pool, "email|ann", old, false)
	exec(t, pool, `UPDATE users SET pseudo = 'Ann' WHERE id = $1`, ann)
	deleted := now.Add(-time.Hour)

	// Created one minute apart, most recent last.
	at := func(n int) time.Time { return old.Add(time.Duration(n) * time.Minute) }
	upcoming := insertChallengeRow(t, pool, ann, challengeRow{title: "Course d'hiver", visibility: "public", startsAt: now.Add(time.Hour), weeks: 2, createdAt: at(1)})
	ongoing := insertChallengeRow(t, pool, ann, challengeRow{title: "COURSE privée", visibility: "private", startsAt: now, weeks: 2, createdAt: at(2)})
	finished := insertChallengeRow(t, pool, ann, challengeRow{title: "Vélo 100%", visibility: "private", startsAt: now.AddDate(0, 0, -14), weeks: 2, createdAt: at(3)})
	gone := insertChallengeRow(t, pool, ann, challengeRow{title: "Nage_libre", visibility: "public", startsAt: now.Add(time.Hour), weeks: 2, createdAt: at(4), deletedAt: &deleted})

	// A second participant, then a deleted one, who counts nowhere.
	bob := insertUser(t, pool, "email|bob", old, false)
	exec(t, pool, `INSERT INTO participants (challenge_id, user_id, joined_at, status, abandoned_at) VALUES ($1, $2, $3, 'abandoned', $3)`, ongoing, bob, at(5))
	cid := insertUser(t, pool, "email|cid", old, true)
	exec(t, pool, `INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3)`, ongoing, cid, at(5))

	yes, no := true, false
	tests := []struct {
		name string
		q    ChallengeQuery
		want []string
	}{
		{name: "every challenge, deleted included, most recent first", q: ChallengeQuery{}, want: []string{gone, finished, ongoing, upcoming}},
		{name: "title, case insensitive", q: ChallengeQuery{Search: "course"}, want: []string{ongoing, upcoming}},
		{name: "accents count", q: ChallengeQuery{Search: "velo"}, want: nil},
		{name: "% is a plain character", q: ChallengeQuery{Search: "0%"}, want: []string{finished}},
		{name: "_ is a plain character", q: ChallengeQuery{Search: "e_l"}, want: []string{gone}},
		{name: "a lone %", q: ChallengeQuery{Search: "%"}, want: []string{finished}},
		{name: "upcoming, deleted or not", q: ChallengeQuery{Status: StatusUpcoming}, want: []string{gone, upcoming}},
		{name: "ongoing from its start", q: ChallengeQuery{Status: StatusOngoing}, want: []string{ongoing}},
		{name: "finished at its end", q: ChallengeQuery{Status: StatusFinished}, want: []string{finished}},
		{name: "private", q: ChallengeQuery{Visibility: "private"}, want: []string{finished, ongoing}},
		{name: "deleted only", q: ChallengeQuery{Deleted: &yes}, want: []string{gone}},
		{name: "not deleted", q: ChallengeQuery{Deleted: &no}, want: []string{finished, ongoing, upcoming}},
		{name: "filters together", q: ChallengeQuery{Search: "course", Visibility: "public", Deleted: &no, Status: StatusUpcoming}, want: []string{upcoming}},
		{name: "after a position", q: ChallengeQuery{After: &ChallengePosition{CreatedAt: at(3), ID: finished}}, want: []string{ongoing, upcoming}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := tt.q
			q.Now, q.Limit = now, 10
			got, err := r.ListChallenges(t.Context(), q)
			if err != nil {
				t.Fatal(err)
			}
			if g := ids(got); !slices.Equal(g, tt.want) {
				t.Fatalf("ListChallenges() = %v, want %v", g, tt.want)
			}
		})
	}

	t.Run("the columns", func(t *testing.T) {
		got, err := r.ListChallenges(t.Context(), ChallengeQuery{Now: now, Limit: 10, Search: "privée"})
		if err != nil {
			t.Fatal(err)
		}
		want := ChallengeRow{
			ID: ongoing, Title: "COURSE privée", Visibility: "private", Timezone: "America/Toronto",
			StartsAt: now, EndsAt: now.AddDate(0, 0, 14), CreatedAt: at(2),
			// The abandoned participant counts, the deleted account does not.
			ParticipantCount: 2,
		}
		if len(got) != 1 {
			t.Fatalf("ListChallenges() = %+v, want one challenge", got)
		}
		g := got[0]
		g.StartsAt, g.EndsAt, g.CreatedAt = g.StartsAt.UTC(), g.EndsAt.UTC(), g.CreatedAt.UTC()
		if !reflect.DeepEqual(g, want) {
			t.Fatalf("ListChallenges() = %+v, want %+v", g, want)
		}
		del, err := r.ListChallenges(t.Context(), ChallengeQuery{Now: now, Limit: 10, Deleted: &yes})
		if err != nil {
			t.Fatal(err)
		}
		if len(del) != 1 || del[0].DeletedAt == nil || !del[0].DeletedAt.Equal(deleted) {
			t.Fatalf("deleted challenge = %+v, want its deleted_at", del)
		}
	})

	t.Run("pages by cursor", func(t *testing.T) {
		var seen []string
		var after *ChallengePosition
		for range 3 {
			page, err := r.ListChallenges(t.Context(), ChallengeQuery{Now: now, Limit: 2, After: after})
			if err != nil {
				t.Fatal(err)
			}
			seen = append(seen, ids(page)...)
			if len(page) < 2 {
				break
			}
			last := page[len(page)-1]
			after = &ChallengePosition{CreatedAt: last.CreatedAt, ID: last.ID}
		}
		if want := []string{gone, finished, ongoing, upcoming}; !slices.Equal(seen, want) {
			t.Fatalf("pages = %v, want %v", seen, want)
		}
	})

	t.Run("the same instant: the id breaks the tie", func(t *testing.T) {
		twin := insertChallengeRow(t, pool, ann, challengeRow{title: "Jumeau", visibility: "public", startsAt: now, weeks: 2, createdAt: at(2)})
		first, second := ongoing, twin
		if second > first {
			first, second = second, first
		}
		got, err := r.ListChallenges(t.Context(), ChallengeQuery{Now: now, Limit: 10, After: &ChallengePosition{CreatedAt: at(2), ID: first}})
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{second, upcoming}; !slices.Equal(ids(got), want) {
			t.Fatalf("ListChallenges() = %v, want %v", ids(got), want)
		}
	})
}

func TestCreatorsOf(t *testing.T) {
	r, pool := newRepository(t)
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	ann := insertUser(t, pool, "email|ann", at, false)
	bob := insertUser(t, pool, "email|bob", at, false)
	exec(t, pool, `UPDATE users SET pseudo = 'Ann' WHERE id = $1`, ann)
	c1 := insertChallengeRow(t, pool, ann, challengeRow{title: "Un", visibility: "private", startsAt: at, weeks: 2, createdAt: at})
	c2 := insertChallengeRow(t, pool, bob, challengeRow{title: "Deux", visibility: "private", startsAt: at, weeks: 2, createdAt: at})
	exec(t, pool, `INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3)`, c1, bob, at)
	exec(t, pool, `INSERT INTO challenge_creators (challenge_id, user_id, created_at) VALUES ($1, $2, $3)`, c1, bob, at.Add(time.Hour))

	got, err := r.CreatorsOf(t.Context(), []string{c1, c2})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]UserRef{
		c1: {{ID: ann, Pseudo: ptr("Ann")}, {ID: bob}},
		c2: {{ID: bob}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CreatorsOf() = %+v, want %+v", got, want)
	}
	if none, err := r.CreatorsOf(t.Context(), nil); err != nil || len(none) != 0 {
		t.Fatalf("CreatorsOf(nil) = %v, %v, want nothing", none, err)
	}
}

func TestReadChallenge(t *testing.T) {
	r, pool := newRepository(t)
	start := time.Date(2026, 11, 9, 5, 0, 0, 0, time.UTC)
	ann := insertUser(t, pool, "email|ann", start, false)
	bob := insertUser(t, pool, "email|bob", start, false)
	exec(t, pool, `UPDATE users SET pseudo = 'Ann' WHERE id = $1`, ann)
	exec(t, pool, `UPDATE users SET pseudo = 'Bob', first_name = 'Bob', last_name = 'Lee' WHERE id = $1`, bob)
	deleted := start.AddDate(0, 0, 13)
	id := insertChallengeRow(t, pool, ann, challengeRow{title: "Défi", visibility: "private", startsAt: start, weeks: 2, createdAt: start.Add(-time.Hour), deletedAt: &deleted})
	exec(t, pool, `UPDATE challenges SET description = 'Go', members_can_invite = true WHERE id = $1`, id)
	exec(t, pool, `INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3)`, id, bob, start)
	// A deleted account counts nowhere.
	gone := insertUser(t, pool, "email|gone", start, true)
	exec(t, pool, `INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3)`, id, gone, start)

	exec(t, pool, `INSERT INTO categories (challenge_id, type, position, params) VALUES
		($1, 'cumulative_time', 1, '{}'), ($1, 'elimination', 3, '{"sport": "run", "step_meters": 1000}')`, id)
	var elimination string
	if err := pool.QueryRow(t.Context(), `SELECT id FROM categories WHERE challenge_id = $1 AND type = 'elimination'`, id).Scan(&elimination); err != nil {
		t.Fatal(err)
	}
	closed := start.AddDate(0, 0, 7).Add(7 * time.Hour)
	exec(t, pool, `INSERT INTO week_closure (challenge_id, week_number, closed_at) VALUES ($1, 1, $2)`, id, closed)
	exec(t, pool, `INSERT INTO elimination (challenge_id, category_id, user_id, week_number, distance_meters, required_meters)
		VALUES ($1, $2, $3, 2, 1000, 6000)`, id, elimination, bob)
	frozen := start.AddDate(0, 0, 14).Add(8 * time.Hour)
	exec(t, pool, `INSERT INTO result_snapshot (category_id, challenge_id, category_type, payload, created_at)
		SELECT id, challenge_id, type, '[]', $2::timestamptz + make_interval(secs => position) FROM categories WHERE challenge_id = $1`, id, frozen)

	var active, revoked string
	insertInvitation := func(author string, createdAt, expiresAt time.Time, revokedAt *time.Time, token byte) string {
		t.Helper()
		var inv string
		hash := make([]byte, 32)
		hash[0] = token
		if err := pool.QueryRow(t.Context(), `INSERT INTO invitations (challenge_id, invited_by, token_hash, expires_at, created_at, revoked_at)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, id, author, hash, expiresAt, createdAt, revokedAt).Scan(&inv); err != nil {
			t.Fatal(err)
		}
		return inv
	}
	revokedAt := start.Add(2 * time.Hour)
	revoked = insertInvitation(ann, start, start.AddDate(0, 0, 7), &revokedAt, 1)
	active = insertInvitation(ann, start.Add(time.Hour), start.AddDate(0, 0, 7), nil, 2)
	// An active participant, members may invite.
	byBob := insertInvitation(bob, start.Add(2*time.Hour), start.AddDate(0, 0, 7), nil, 3)

	// Another challenge, whose rows never leak into this one.
	other := insertChallengeRow(t, pool, bob, challengeRow{title: "Autre", visibility: "public", startsAt: start, weeks: 2, createdAt: start})
	exec(t, pool, `INSERT INTO week_closure (challenge_id, week_number, closed_at) VALUES ($1, 2, $2)`, other, closed)

	got, err := r.Challenge(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	c := got.Challenge
	c.StartsAt, c.EndsAt, c.CreatedAt, c.DeletedAt = c.StartsAt.UTC(), c.EndsAt.UTC(), c.CreatedAt.UTC(), utc(c.DeletedAt)
	wantRecord := ChallengeRecord{
		ID: id, Title: "Défi", Description: ptr("Go"), Visibility: "private", MembersCanInvite: true,
		Timezone: "America/Toronto", ReferenceSport: "run", StartsAt: start, EndsAt: start.AddDate(0, 0, 14),
		CreatedAt: start.Add(-time.Hour), DeletedAt: &deleted, ParticipantCount: 2,
	}
	if !reflect.DeepEqual(c, wantRecord) {
		t.Errorf("Challenge =\n%+v\nwant\n%+v", c, wantRecord)
	}
	if len(got.Categories) != 2 || got.Categories[0].Type != "cumulative_time" || got.Categories[1].Type != "elimination" ||
		got.Categories[1].Position != 3 || string(got.Categories[1].Params) != `{"sport": "run", "step_meters": 1000}` {
		t.Errorf("Categories = %+v (params %q), want both in position order", got.Categories, got.Categories[1].Params)
	}
	if want := []UserRef{{ID: ann, Pseudo: ptr("Ann")}}; !reflect.DeepEqual(got.Creators, want) {
		t.Errorf("Creators = %+v, want %+v", got.Creators, want)
	}
	if len(got.ClosedWeeks) != 1 || !got.ClosedWeeks[1].Equal(closed) {
		t.Errorf("ClosedWeeks = %v, want week 1 at %v", got.ClosedWeeks, closed)
	}
	if want := []EliminationRow{{User: UserRef{ID: bob, Pseudo: ptr("Bob"), FirstName: ptr("Bob"), LastName: ptr("Lee")}, WeekNumber: 2, DistanceMeters: 1000, RequiredMeters: 6000}}; !reflect.DeepEqual(got.Eliminations, want) {
		t.Errorf("Eliminations = %+v, want %+v", got.Eliminations, want)
	}
	// The first category frozen gives the instant.
	if wantFrozen := frozen.Add(time.Second); got.ResultsFrozenAt == nil || !got.ResultsFrozenAt.Equal(wantFrozen) {
		t.Errorf("ResultsFrozenAt = %v, want %v", got.ResultsFrozenAt, wantFrozen)
	}
	if len(got.Invitations) != 3 || got.Invitations[0].ID != byBob || got.Invitations[1].ID != active || got.Invitations[2].ID != revoked {
		t.Fatalf("Invitations = %+v, want Bob's, the active then the revoked one", got.Invitations)
	}
	bobRef := UserRef{ID: bob, Pseudo: ptr("Bob"), FirstName: ptr("Bob"), LastName: ptr("Lee")}
	if b := got.Invitations[0]; !reflect.DeepEqual(b.InvitedBy, bobRef) || !b.AuthorCanInvite {
		t.Errorf("Bob's invitation = %+v, want his names and that he may invite", b)
	}
	if !got.Invitations[1].AuthorCanInvite {
		t.Errorf("a creator's invitation = %+v, want that she may invite", got.Invitations[1])
	}
	inv := got.Invitations[2]
	if !reflect.DeepEqual(inv.InvitedBy, UserRef{ID: ann, Pseudo: ptr("Ann")}) || !inv.CreatedAt.Equal(start) ||
		!inv.ExpiresAt.Equal(start.AddDate(0, 0, 7)) || inv.RevokedAt == nil || !inv.RevokedAt.Equal(revokedAt) {
		t.Errorf("revoked invitation = %+v", inv)
	}
	if got.Invitations[1].RevokedAt != nil {
		t.Errorf("active invitation revoked at %v", got.Invitations[1].RevokedAt)
	}

	// Bob's link is cancelled once members may no longer invite, and once he
	// abandons (README §5.7).
	for _, membersCanInvite := range []bool{false, true} {
		exec(t, pool, `UPDATE challenges SET members_can_invite = $2 WHERE id = $1`, id, membersCanInvite)
		got, err := r.Challenge(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Invitations[0].AuthorCanInvite != membersCanInvite {
			t.Errorf("members may invite %v: Bob may invite = %v", membersCanInvite, got.Invitations[0].AuthorCanInvite)
		}
	}
	exec(t, pool, `UPDATE participants SET status = 'abandoned', abandoned_at = joined_at WHERE challenge_id = $1 AND user_id = $2`, id, bob)
	got, err = r.Challenge(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Invitations[0].AuthorCanInvite || !got.Invitations[1].AuthorCanInvite {
		t.Errorf("after Bob abandoned, invitations = %+v, want only his cancelled", got.Invitations)
	}
}

func TestReadChallengeWithNothingYet(t *testing.T) {
	r, pool := newRepository(t)
	start := time.Date(2026, 11, 9, 5, 0, 0, 0, time.UTC)
	ann := insertUser(t, pool, "email|ann", start, false)
	id := insertChallengeRow(t, pool, ann, challengeRow{title: "Défi", visibility: "public", startsAt: start, weeks: 2, createdAt: start})

	got, err := r.Challenge(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Challenge.DeletedAt != nil || got.Challenge.Description != nil || len(got.ClosedWeeks) != 0 ||
		len(got.Eliminations) != 0 || got.ResultsFrozenAt != nil || len(got.Invitations) != 0 {
		t.Fatalf("Challenge() = %+v, want nothing judged, frozen nor invited", got)
	}
}

func TestReadChallengeNotFound(t *testing.T) {
	r, _ := newRepository(t)
	_, err := r.Challenge(t.Context(), "5b1c3c2e-2d0f-4f8e-9a51-6f1f0c7a3d11")
	if !errors.Is(err, errChallengeNotFound) {
		t.Fatalf("Challenge() = %v, want errChallengeNotFound", err)
	}
}
