//go:build integration

package challenge

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
)

func TestGetReadsTheChallengeAndMyPlace(t *testing.T) {
	pseudo := "marie_92"
	svc, pool, creatorID := setup(t, &pseudo)
	c, err := svc.Create(t.Context(), creatorID, with(func(d *Draft) { d.Visibility = VisibilityPrivate }))
	if err != nil {
		t.Fatal(err)
	}

	// The creator sees it, categories in position order.
	d, err := svc.Get(t.Context(), creatorID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Role != RoleCreator || d.Participant != ParticipantActive || len(d.Categories) != 3 {
		t.Fatalf("detail %+v, want the creator, active, with 3 categories", d)
	}
	for i, cat := range d.Categories {
		if cat.Position != i+1 {
			t.Fatalf("category %d at position %d, want them in order", i, cat.Position)
		}
	}
	if d.Status != StatusOngoing || d.CurrentWeek != 1 || d.CanJoin {
		t.Fatalf("detail %+v, want ongoing, week 1, cannot join again", d)
	}

	// An abandoned participant still sees the private challenge.
	other := insertTestUser(t, pool, "email|abandoned")
	if _, err := pool.Exec(t.Context(), "INSERT INTO participants VALUES ($1, $2, 'abandoned', $3, $3)", c.ID, other, now); err != nil {
		t.Fatal(err)
	}
	if d, err := svc.Get(t.Context(), other, c.ID); err != nil || d.Role != RoleAbandoned {
		t.Fatalf("got %+v, %v, want the abandoned role", d.Role, err)
	}
}

// TestGetNotFoundIsTheSameForEveryCase: missing, deleted and private
// without access answer the very same error.
func TestGetNotFoundIsTheSameForEveryCase(t *testing.T) {
	pseudo := "marie_92"
	svc, pool, creatorID := setup(t, &pseudo)
	private, err := svc.Create(t.Context(), creatorID, with(func(d *Draft) { d.Visibility = VisibilityPrivate }))
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := svc.Create(t.Context(), creatorID, valid())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE challenges SET deleted_at = now() WHERE id = $1", deleted.ID); err != nil {
		t.Fatal(err)
	}
	outsider := insertTestUser(t, pool, "email|outsider")

	cases := map[string]struct{ actor, id string }{
		"missing":                      {outsider, "00000000-0000-0000-0000-000000000000"},
		"deleted, even to its creator": {creatorID, deleted.ID},
		"private without access":       {outsider, private.ID},
	}
	var first *apperr.Error
	for name, tc := range cases {
		_, err := svc.Get(t.Context(), tc.actor, tc.id)
		var appErr *apperr.Error
		if !errors.As(err, &appErr) || appErr.Code != apperr.CodeChallengeNotFound {
			t.Fatalf("%s: error = %v, want challenge_not_found", name, err)
		}
		if first == nil {
			first = appErr
		}
		if appErr.Status != first.Status || appErr.Message != first.Message || appErr.Params != nil {
			t.Fatalf("%s: %+v differs from %+v", name, appErr, first)
		}
	}
}

// TestGetRegistrationDeadline reads the detail one second before and at the
// exact end of week 1, with the real storage.
func TestGetRegistrationDeadline(t *testing.T) {
	pseudo := "marie_92"
	svc, pool, creatorID := setup(t, &pseudo)
	c, err := svc.Create(t.Context(), creatorID, valid()) // from 2026-03-04, Toronto
	if err != nil {
		t.Fatal(err)
	}
	outsider := insertTestUser(t, pool, "email|late")
	deadline := time.Date(2026, 3, 11, 4, 0, 0, 0, time.UTC) // local midnight, after the time change

	gdb, err := db.NewGorm(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(deadline.Add(-time.Second))
	reader := NewService(NewRepository(gdb), nil, nil, clk)

	d, err := reader.Get(t.Context(), outsider, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !d.CanJoin || d.CurrentWeek != 1 || !d.RegistrationDeadline.Equal(deadline) || !d.WeekEndsAt.Equal(deadline) {
		t.Fatalf("one second before: %+v, want can join in week 1 ending %s", d, deadline)
	}

	clk.Set(deadline)
	if d, err = reader.Get(t.Context(), outsider, c.ID); err != nil {
		t.Fatal(err)
	}
	if d.CanJoin || d.CurrentWeek != 2 {
		t.Fatalf("at the deadline: can join %v, week %d, want registration closed in week 2", d.CanJoin, d.CurrentWeek)
	}
}

// TestGetInvited: a private challenge is read by the invited only through a
// valid invitation linked to them; a token never opened gives nothing, and an
// invitation stops giving access when it expires or is revoked.
func TestGetInvited(t *testing.T) {
	pseudo := "marie_92"
	_, pool, creatorID := setup(t, &pseudo)
	gdb, err := db.NewGorm(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(now)
	svc := NewService(NewRepository(gdb), sqlProfiles{pool}, sqlCatalogue{pool}, clk)
	c, err := svc.Create(t.Context(), creatorID, with(func(d *Draft) { d.Visibility = VisibilityPrivate }))
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.Create(t.Context(), creatorID, with(func(d *Draft) { d.Visibility = VisibilityPrivate }))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Date(2026, 3, 11, 4, 0, 0, 0, time.UTC) // end of week 1 of valid()

	invite := func(challengeID, token string, revoked bool) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(t.Context(), `INSERT INTO invitations (challenge_id, invited_by, token_hash, expires_at, created_at, revoked_at)
			VALUES ($1, $2, sha256($3::bytea), $4, $5, CASE WHEN $6 THEN $5::timestamptz END) RETURNING id`,
			challengeID, creatorID, []byte(token), deadline, now, revoked).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	link := func(invitationID, userID string) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), "INSERT INTO invitation_recipients VALUES ($1, $2, $3)", invitationID, userID, now); err != nil {
			t.Fatal(err)
		}
	}
	valid := invite(c.ID, "valid", false)
	revoked := invite(c.ID, "revoked", true)
	elsewhere := invite(other.ID, "elsewhere", false)

	guest := insertTestUser(t, pool, "email|guest")
	link(valid, guest)
	revokedGuest := insertTestUser(t, pool, "email|revoked")
	link(revoked, revokedGuest)
	otherGuest := insertTestUser(t, pool, "email|elsewhere")
	link(elsewhere, otherGuest)
	// Holds the token of a valid invitation, never opened while logged in.
	unlinked := insertTestUser(t, pool, "email|unlinked")

	d, err := svc.Get(t.Context(), guest, c.ID)
	if err != nil || d.Role != RoleInvited || !d.CanJoin {
		t.Fatalf("linked to a valid invitation: %+v, %v, want invited, may join", d.Role, err)
	}
	for name, user := range map[string]string{"revoked": revokedGuest, "invited elsewhere": otherGuest, "token not linked": unlinked} {
		if _, err := svc.Get(t.Context(), user, c.ID); !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
			t.Fatalf("%s: error = %v, want challenge_not_found", name, err)
		}
	}

	// The invitation expires at the exact end of week 1.
	clk.Set(deadline.Add(-time.Second))
	if d, err := svc.Get(t.Context(), guest, c.ID); err != nil || d.Role != RoleInvited {
		t.Fatalf("one second before the expiry: %+v, %v, want invited", d.Role, err)
	}
	clk.Set(deadline)
	if _, err := svc.Get(t.Context(), guest, c.ID); !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("at the expiry: error = %v, want challenge_not_found", err)
	}

	// Revoked after the link: the access goes with it.
	clk.Set(now)
	if _, err := pool.Exec(t.Context(), "UPDATE invitations SET revoked_at = $1 WHERE id = $2", now, valid); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(t.Context(), guest, c.ID); !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("revoked after the link: error = %v, want challenge_not_found", err)
	}

	// Invited by a participant while members may invite: the invitation
	// lasts as long as the author may invite, not after their exclusion.
	member := insertTestUser(t, pool, "email|member")
	if _, err := pool.Exec(t.Context(), "UPDATE challenges SET members_can_invite = true WHERE id = $1", c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "INSERT INTO participants VALUES ($1, $2, 'active', $3, NULL)", c.ID, member, now); err != nil {
		t.Fatal(err)
	}
	var byMember string
	if err := pool.QueryRow(t.Context(), `INSERT INTO invitations (challenge_id, invited_by, token_hash, expires_at, created_at)
		VALUES ($1, $2, sha256('member'::bytea), $3, $4) RETURNING id`, c.ID, member, deadline, now).Scan(&byMember); err != nil {
		t.Fatal(err)
	}
	friend := insertTestUser(t, pool, "email|friend")
	link(byMember, friend)
	// Invited by the member too, and joined before the exclusion.
	joined := insertTestUser(t, pool, "email|joined")
	link(byMember, joined)
	if _, err := pool.Exec(t.Context(), "INSERT INTO participants VALUES ($1, $2, 'active', $3, NULL)", c.ID, joined, now); err != nil {
		t.Fatal(err)
	}
	if d, err := svc.Get(t.Context(), friend, c.ID); err != nil || d.Role != RoleInvited {
		t.Fatalf("invited by an active participant: %+v, %v, want invited", d.Role, err)
	}
	if err := svc.Exclude(t.Context(), creatorID, c.ID, member); err != nil {
		t.Fatal(err)
	}
	// Excluding the member cancels their link for whoever has not joined
	// yet; whoever joined stays a participant.
	if _, err := svc.Get(t.Context(), friend, c.ID); !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("author excluded, not joined: error = %v, want challenge_not_found", err)
	}
	if d, err := svc.Get(t.Context(), joined, c.ID); err != nil || d.Role != RoleParticipant || d.Participant != ParticipantActive {
		t.Fatalf("author excluded, joined before: %+v, %v, want an active participant", d.Role, err)
	}
}

// TestJoinWithAValidInvitation: a user linked to a valid invitation joins
// the private challenge, judged under the lock like any join.
func TestJoinWithAValidInvitation(t *testing.T) {
	pseudo := "marie_92"
	svc, pool, creatorID := setup(t, &pseudo)
	c, err := svc.Create(t.Context(), creatorID, with(func(d *Draft) { d.Visibility = VisibilityPrivate }))
	if err != nil {
		t.Fatal(err)
	}
	guest := insertTestUser(t, pool, "email|guest")
	if _, err := pool.Exec(t.Context(), "UPDATE users SET pseudo = 'guest_1' WHERE id = $1", guest); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Join(t.Context(), guest, c.ID); !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("not invited: error = %v, want challenge_not_found", err)
	}
	if _, err := pool.Exec(t.Context(), `WITH i AS (
		INSERT INTO invitations (challenge_id, invited_by, token_hash, expires_at, created_at)
		VALUES ($1, $2, sha256('t'::bytea), '2026-03-11 04:00Z', $3) RETURNING id)
		INSERT INTO invitation_recipients SELECT id, $4, $3 FROM i`, c.ID, creatorID, now, guest); err != nil {
		t.Fatal(err)
	}
	d, err := svc.Join(t.Context(), guest, c.ID)
	if err != nil || d.Role != RoleParticipant {
		t.Fatalf("invited: %+v, %v, want participant", d.Role, err)
	}
}

func TestUpdateTextStoresTheChange(t *testing.T) {
	pseudo := "marie_92"
	svc, pool, creatorID := setup(t, &pseudo)
	desc := "Old description"
	c, err := svc.Create(t.Context(), creatorID, with(func(d *Draft) { d.Description = &desc }))
	if err != nil {
		t.Fatal(err)
	}

	title, empty := "  New title  ", ""
	d, err := svc.UpdateText(t.Context(), creatorID, c.ID, nil, TextChanges{Title: &title, Description: &empty})
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	var description *string
	if err := pool.QueryRow(t.Context(), "SELECT title, description FROM challenges WHERE id = $1", c.ID).Scan(&stored, &description); err != nil {
		t.Fatal(err)
	}
	if stored != "New title" || description != nil || d.Title != "New title" || d.Description != nil {
		t.Fatalf("stored %q %v, detail %q %v, want the trimmed title and no description", stored, description, d.Title, d.Description)
	}

	// A deleted challenge is never written: not found, even for its creator.
	if _, err := pool.Exec(t.Context(), "UPDATE challenges SET deleted_at = now() WHERE id = $1", c.ID); err != nil {
		t.Fatal(err)
	}
	again := "Again"
	if _, err := svc.UpdateText(t.Context(), creatorID, c.ID, nil, TextChanges{Title: &again}); !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("error = %v, want challenge_not_found", err)
	}
	if err := pool.QueryRow(t.Context(), "SELECT title FROM challenges WHERE id = $1", c.ID).Scan(&stored); err != nil || stored != "New title" {
		t.Fatalf("title %q (%v), want the deleted challenge untouched", stored, err)
	}
}

// joinFixture is a public challenge of 6 weeks from 2026-03-04 (Toronto)
// created by a creator, and a service whose clock the test moves.
func joinFixture(t *testing.T) (svc *Service, clk *clock.FakeClock, pool *pgxpool.Pool, challengeID string) {
	t.Helper()
	pseudo := "marie_92"
	creatorSvc, pool, creatorID := setup(t, &pseudo)
	c, err := creatorSvc.Create(t.Context(), creatorID, valid())
	if err != nil {
		t.Fatal(err)
	}
	gdb, err := db.NewGorm(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	clk = clock.NewFake(now)
	return NewService(NewRepository(gdb), sqlProfiles{pool}, sqlCatalogue{pool}, clk), clk, pool, c.ID
}

func insertProfile(t *testing.T, pool *pgxpool.Pool, subject string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(t.Context(), "INSERT INTO users (auth_subject, pseudo) VALUES ($1, $1) RETURNING id", subject).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestJoinAtTheEndOfWeekOne(t *testing.T) {
	svc, clk, pool, id := joinFixture(t)
	deadline := time.Date(2026, 3, 11, 4, 0, 0, 0, time.UTC) // local midnight, after the time change

	early := insertProfile(t, pool, "join_early")
	clk.Set(deadline.Add(-time.Second))
	if _, err := svc.Join(t.Context(), early, id); err != nil {
		t.Fatalf("one second before the end of week 1: %v", err)
	}
	var status string
	var joinedAt time.Time
	if err := pool.QueryRow(t.Context(), "SELECT status, joined_at FROM participants WHERE challenge_id = $1 AND user_id = $2", id, early).Scan(&status, &joinedAt); err != nil {
		t.Fatal(err)
	}
	if status != "active" || !joinedAt.Equal(deadline.Add(-time.Second)) {
		t.Fatalf("stored %s joined at %s, want active at the clock's now", status, joinedAt)
	}

	late := insertProfile(t, pool, "join_late")
	clk.Set(deadline)
	if _, err := svc.Join(t.Context(), late, id); !errors.Is(err, apperr.New(apperr.CodeRegistrationClosed, nil)) {
		t.Fatalf("at the exact end of week 1: %v, want registration_closed", err)
	}
	if n := count(t, pool, "SELECT count(*) FROM participants WHERE user_id = $1", late); n != 0 {
		t.Fatalf("%d participations stored after the deadline, want 0", n)
	}
}

func TestJoinAbandonIsFinal(t *testing.T) {
	svc, _, pool, id := joinFixture(t)
	user := insertProfile(t, pool, "join_quitter")
	if _, err := pool.Exec(t.Context(), "INSERT INTO participants VALUES ($1, $2, 'abandoned', $3, $3)", id, user, now); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Join(t.Context(), user, id); !errors.Is(err, apperr.New(apperr.CodeParticipationAbandoned, nil)) {
		t.Fatalf("error = %v, want participation_abandoned", err)
	}
}

func TestJoinRefusalsStoreNothing(t *testing.T) {
	svc, _, pool, id := joinFixture(t)
	incomplete := insertTestUser(t, pool, "join_no_pseudo")
	if _, err := svc.Join(t.Context(), incomplete, id); !errors.Is(err, apperr.New(apperr.CodeProfileIncomplete, nil)) {
		t.Fatalf("error = %v, want profile_incomplete", err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE challenges SET deleted_at = now() WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	user := insertProfile(t, pool, "join_deleted")
	if _, err := svc.Join(t.Context(), user, id); !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("deleted challenge: %v, want challenge_not_found", err)
	}
	if n := count(t, pool, "SELECT count(*) FROM participants WHERE user_id IN ($1, $2)", incomplete, user); n != 0 {
		t.Fatalf("%d participations stored, want 0", n)
	}
}

// TestJoinRace: the same user joins from 10 requests at once; the lock on
// the challenge makes exactly one succeed and the others see them in.
func TestJoinRace(t *testing.T) {
	svc, _, pool, id := joinFixture(t)
	user := insertProfile(t, pool, "join_racer")
	const n = 10
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Go(func() {
			<-start
			_, errs[i] = svc.Join(t.Context(), user, id)
		})
	}
	close(start)
	wg.Wait()

	var joined int
	for i, err := range errs {
		switch {
		case err == nil:
			joined++
		case !errors.Is(err, apperr.New(apperr.CodeAlreadyParticipant, nil)):
			t.Fatalf("request %d: %v, want nil or already_participant", i, err)
		}
	}
	if joined != 1 {
		t.Fatalf("%d requests joined, want exactly 1", joined)
	}
	if c := count(t, pool, "SELECT count(*) FROM participants WHERE challenge_id = $1 AND user_id = $2", id, user); c != 1 {
		t.Fatalf("%d participations stored, want 1", c)
	}
}

func TestAbandonIsStoredAndFinal(t *testing.T) {
	svc, clk, pool, id := joinFixture(t)
	user := insertProfile(t, pool, "abandon_me")
	if _, err := svc.Join(t.Context(), user, id); err != nil {
		t.Fatal(err)
	}

	clk.Advance(48 * time.Hour)
	d, err := svc.Abandon(t.Context(), user, id)
	if err != nil {
		t.Fatal(err)
	}
	var status string
	var abandonedAt time.Time
	if err := pool.QueryRow(t.Context(), "SELECT status, abandoned_at FROM participants WHERE challenge_id = $1 AND user_id = $2", id, user).Scan(&status, &abandonedAt); err != nil {
		t.Fatal(err)
	}
	if status != "abandoned" || !abandonedAt.Equal(clk.Now()) || d.Role != RoleAbandoned {
		t.Fatalf("stored %s at %s, role %v, want abandoned at the clock's now", status, abandonedAt, d.Role)
	}

	// Again: idempotent, the first instant stays.
	clk.Advance(time.Hour)
	if _, err := svc.Abandon(t.Context(), user, id); err != nil {
		t.Fatal(err)
	}
	var again time.Time
	if err := pool.QueryRow(t.Context(), "SELECT abandoned_at FROM participants WHERE challenge_id = $1 AND user_id = $2", id, user).Scan(&again); err != nil || !again.Equal(abandonedAt) {
		t.Fatalf("abandoned_at %s (%v), want the first abandon kept", again, err)
	}
	// Final: joining again is refused.
	if _, err := svc.Join(t.Context(), user, id); !errors.Is(err, apperr.New(apperr.CodeParticipationAbandoned, nil)) {
		t.Fatalf("join after abandon: %v, want participation_abandoned", err)
	}
}

func TestAbandonByTheCreatorAndAfterTheEnd(t *testing.T) {
	svc, clk, pool, id := joinFixture(t)
	var creatorID string
	if err := pool.QueryRow(t.Context(), "SELECT created_by FROM challenges WHERE id = $1", id).Scan(&creatorID); err != nil {
		t.Fatal(err)
	}
	d, err := svc.Abandon(t.Context(), creatorID, id)
	if err != nil {
		t.Fatal(err)
	}
	if d.Role != RoleCreator || d.Participant != ParticipantAbandoned {
		t.Fatalf("role %v, participation %s, want still creator, abandoned", d.Role, d.Participant)
	}

	user := insertProfile(t, pool, "abandon_late")
	if _, err := svc.Join(t.Context(), user, id); err != nil {
		t.Fatal(err)
	}
	clk.Set(time.Date(2026, 4, 15, 4, 0, 0, 0, time.UTC)) // the exact end of the 6 weeks
	if _, err := svc.Abandon(t.Context(), user, id); !errors.Is(err, apperr.New(apperr.CodeChallengeEnded, nil)) {
		t.Fatalf("abandon at the end: %v, want challenge_ended", err)
	}
	var status string
	if err := pool.QueryRow(t.Context(), "SELECT status FROM participants WHERE challenge_id = $1 AND user_id = $2", id, user).Scan(&status); err != nil || status != "active" {
		t.Fatalf("status %s (%v), want still active", status, err)
	}
}

func TestAbandonDeletedChallenge(t *testing.T) {
	svc, _, pool, id := joinFixture(t)
	user := insertProfile(t, pool, "abandon_deleted")
	if _, err := svc.Join(t.Context(), user, id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE challenges SET deleted_at = now() WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Abandon(t.Context(), user, id); !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("error = %v, want challenge_not_found", err)
	}
	var status string
	if err := pool.QueryRow(t.Context(), "SELECT status FROM participants WHERE challenge_id = $1 AND user_id = $2", id, user).Scan(&status); err != nil || status != "active" {
		t.Fatalf("status %s (%v), want nothing written", status, err)
	}
}

func TestAbandonWithoutParticipationWritesNothing(t *testing.T) {
	svc, _, pool, id := joinFixture(t)
	outsider := insertProfile(t, pool, "abandon_outsider")
	if _, err := svc.Abandon(t.Context(), outsider, id); !errors.Is(err, apperr.New(apperr.CodeNotFound, nil)) {
		t.Fatalf("error = %v, want not_found (visible, never joined)", err)
	}
	if n := count(t, pool, "SELECT count(*) FROM participants WHERE user_id = $1", outsider); n != 0 {
		t.Fatalf("%d participations stored, want 0", n)
	}
}

// TestJoinAndAbandonRace: users join and abandon at the same time. The lock
// shared by both (withLockedMembership) serializes them: the join always
// succeeds; the abandon either comes first (nothing to abandon, 404) or
// after (the participation ends abandoned). Never a 500, never a mixed state.
func TestJoinAndAbandonRace(t *testing.T) {
	svc, _, pool, id := joinFixture(t)
	const users = 8
	type outcome struct{ joinErr, abandonErr error }
	results := make([]outcome, users)
	ids := make([]string, users)
	for i := range users {
		ids[i] = insertProfile(t, pool, "race_"+string(rune('a'+i)))
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range users {
		wg.Go(func() {
			<-start
			_, results[i].joinErr = svc.Join(t.Context(), ids[i], id)
		})
		wg.Go(func() {
			<-start
			_, results[i].abandonErr = svc.Abandon(t.Context(), ids[i], id)
		})
	}
	close(start)
	wg.Wait()

	for i, r := range results {
		if r.joinErr != nil {
			t.Fatalf("user %d: join %v, want it to succeed", i, r.joinErr)
		}
		var status string
		if err := pool.QueryRow(t.Context(), "SELECT status FROM participants WHERE challenge_id = $1 AND user_id = $2", id, ids[i]).Scan(&status); err != nil {
			t.Fatal(err)
		}
		switch {
		case r.abandonErr == nil && status == "abandoned":
		case errors.Is(r.abandonErr, apperr.New(apperr.CodeNotFound, nil)) && status == "active":
		default:
			t.Fatalf("user %d: abandon %v with status %s, want abandoned after the join or 404 before it", i, r.abandonErr, status)
		}
	}
}

func TestExcludeIsStoredAsAnAbandon(t *testing.T) {
	svc, clk, pool, id := joinFixture(t)
	var creatorID string
	if err := pool.QueryRow(t.Context(), "SELECT created_by FROM challenges WHERE id = $1", id).Scan(&creatorID); err != nil {
		t.Fatal(err)
	}
	member := insertProfile(t, pool, "exclude_member")
	other := insertProfile(t, pool, "exclude_other")
	for _, u := range []string{member, other} {
		if _, err := svc.Join(t.Context(), u, id); err != nil {
			t.Fatal(err)
		}
	}

	// A participant may not exclude.
	if err := svc.Exclude(t.Context(), other, id, member); !errors.Is(err, apperr.New(apperr.CodeForbidden, nil)) {
		t.Fatalf("participant excludes: %v, want forbidden", err)
	}
	// Nobody excludes the creator.
	if err := svc.Exclude(t.Context(), creatorID, id, creatorID); !errors.Is(err, apperr.New(apperr.CodeCannotExcludeCreator, nil)) {
		t.Fatalf("creator excluded: %v, want cannot_exclude_creator", err)
	}
	// A user who never joined.
	stranger := insertProfile(t, pool, "exclude_stranger")
	if err := svc.Exclude(t.Context(), creatorID, id, stranger); !errors.Is(err, apperr.New(apperr.CodeNotFound, nil)) {
		t.Fatalf("stranger: %v, want not_found", err)
	}

	clk.Advance(24 * time.Hour)
	if err := svc.Exclude(t.Context(), creatorID, id, member); err != nil {
		t.Fatal(err)
	}
	var status string
	var abandonedAt time.Time
	if err := pool.QueryRow(t.Context(), "SELECT status, abandoned_at FROM participants WHERE challenge_id = $1 AND user_id = $2", id, member).Scan(&status, &abandonedAt); err != nil {
		t.Fatal(err)
	}
	if status != "abandoned" || !abandonedAt.Equal(clk.Now()) {
		t.Fatalf("stored %s at %s, want abandoned at the clock's now", status, abandonedAt)
	}
	// Shown as an abandon, with no mention of the exclusion.
	if d, err := svc.Get(t.Context(), member, id); err != nil || d.Role != RoleAbandoned || d.Participant != ParticipantAbandoned {
		t.Fatalf("detail for the excluded: %+v, %v, want an abandon", d.Role, err)
	}
	// Excluded is final, like an abandon; excluding again changes nothing.
	if _, err := svc.Join(t.Context(), member, id); !errors.Is(err, apperr.New(apperr.CodeParticipationAbandoned, nil)) {
		t.Fatalf("rejoin after exclusion: %v, want participation_abandoned", err)
	}
	if err := svc.Exclude(t.Context(), creatorID, id, member); err != nil {
		t.Fatalf("exclude again: %v, want nothing to do", err)
	}
	var again time.Time
	if err := pool.QueryRow(t.Context(), "SELECT abandoned_at FROM participants WHERE challenge_id = $1 AND user_id = $2", id, member).Scan(&again); err != nil || !again.Equal(abandonedAt) {
		t.Fatalf("abandoned_at %s (%v), want the first instant kept", again, err)
	}
	if n := count(t, pool, "SELECT count(*) FROM participants WHERE challenge_id = $1 AND status = 'active'", id); n != 2 {
		t.Fatalf("%d active participations, want the creator and the other one", n)
	}
}

func TestExcludeByACreatorWhoAbandonedAndAtTheEnd(t *testing.T) {
	svc, clk, pool, id := joinFixture(t)
	var creatorID string
	if err := pool.QueryRow(t.Context(), "SELECT created_by FROM challenges WHERE id = $1", id).Scan(&creatorID); err != nil {
		t.Fatal(err)
	}
	early, late := insertProfile(t, pool, "exclude_early"), insertProfile(t, pool, "exclude_late")
	for _, u := range []string{early, late} {
		if _, err := svc.Join(t.Context(), u, id); err != nil {
			t.Fatal(err)
		}
	}
	// The creator abandons, and still excludes.
	if _, err := svc.Abandon(t.Context(), creatorID, id); err != nil {
		t.Fatal(err)
	}
	if err := svc.Exclude(t.Context(), creatorID, id, early); err != nil {
		t.Fatalf("creator who abandoned excludes: %v, want allowed", err)
	}
	// At the exact end: refused, the target stays active.
	clk.Set(time.Date(2026, 4, 15, 4, 0, 0, 0, time.UTC))
	if err := svc.Exclude(t.Context(), creatorID, id, late); !errors.Is(err, apperr.New(apperr.CodeChallengeEnded, nil)) {
		t.Fatalf("at the end: %v, want challenge_ended", err)
	}
	var status string
	if err := pool.QueryRow(t.Context(), "SELECT status FROM participants WHERE challenge_id = $1 AND user_id = $2", id, late).Scan(&status); err != nil || status != "active" {
		t.Fatalf("status %s (%v), want still active", status, err)
	}
}

func TestExcludeDeletedChallenge(t *testing.T) {
	svc, _, pool, id := joinFixture(t)
	var creatorID string
	if err := pool.QueryRow(t.Context(), "SELECT created_by FROM challenges WHERE id = $1", id).Scan(&creatorID); err != nil {
		t.Fatal(err)
	}
	member := insertProfile(t, pool, "exclude_deleted")
	if _, err := svc.Join(t.Context(), member, id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE challenges SET deleted_at = now() WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	if err := svc.Exclude(t.Context(), creatorID, id, member); !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("error = %v, want challenge_not_found", err)
	}
	if n := count(t, pool, "SELECT count(*) FROM participants WHERE user_id = $1 AND status = 'active'", member); n != 1 {
		t.Fatalf("%d active participations, want the participation untouched", n)
	}
}

// TestExcludeAndAbandonRace: a creator excludes users while they abandon.
// The shared lock serializes both: each user ends abandoned exactly once,
// never a 500.
func TestExcludeAndAbandonRace(t *testing.T) {
	svc, _, pool, id := joinFixture(t)
	var creatorID string
	if err := pool.QueryRow(t.Context(), "SELECT created_by FROM challenges WHERE id = $1", id).Scan(&creatorID); err != nil {
		t.Fatal(err)
	}
	const users = 8
	ids := make([]string, users)
	for i := range users {
		ids[i] = insertProfile(t, pool, "exrace_"+string(rune('a'+i)))
		if _, err := svc.Join(t.Context(), ids[i], id); err != nil {
			t.Fatal(err)
		}
	}
	errs := make([]error, 2*users)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range users {
		wg.Go(func() {
			<-start
			errs[2*i] = svc.Exclude(t.Context(), creatorID, id, ids[i])
		})
		wg.Go(func() {
			<-start
			_, errs[2*i+1] = svc.Abandon(t.Context(), ids[i], id)
		})
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("request %d: %v, want both to succeed (the second one changes nothing)", i, err)
		}
	}
	if n := count(t, pool, "SELECT count(*) FROM participants WHERE challenge_id = $1 AND status = 'abandoned'", id); n != users {
		t.Fatalf("%d abandoned, want %d", n, users)
	}
}

func TestAddCreatorGivesTheCreatorRights(t *testing.T) {
	svc, clk, pool, id := joinFixture(t)
	var creatorID string
	if err := pool.QueryRow(t.Context(), "SELECT created_by FROM challenges WHERE id = $1", id).Scan(&creatorID); err != nil {
		t.Fatal(err)
	}
	member, quitter, stranger := insertProfile(t, pool, "co_member"), insertProfile(t, pool, "co_quitter"), insertProfile(t, pool, "co_stranger")
	for _, u := range []string{member, quitter} {
		if _, err := svc.Join(t.Context(), u, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.Abandon(t.Context(), quitter, id); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.AddCreator(t.Context(), member, id, quitter); !errors.Is(err, apperr.New(apperr.CodeForbidden, nil)) {
		t.Fatalf("participant adds: %v, want forbidden", err)
	}
	// Nobody makes themselves a creator.
	if _, err := svc.AddCreator(t.Context(), member, id, member); !errors.Is(err, apperr.New(apperr.CodeForbidden, nil)) {
		t.Fatalf("participant promotes themselves: %v, want forbidden", err)
	}
	for name, target := range map[string]string{"abandoned": quitter, "never joined": stranger} {
		if _, err := svc.AddCreator(t.Context(), creatorID, id, target); !errors.Is(err, apperr.New(apperr.CodeNotActiveParticipant, nil)) {
			t.Fatalf("%s target: %v, want not_active_participant", name, err)
		}
	}

	if _, err := svc.AddCreator(t.Context(), creatorID, id, member); err != nil {
		t.Fatal(err)
	}
	// Idempotent: adding again changes nothing.
	if _, err := svc.AddCreator(t.Context(), creatorID, id, member); err != nil {
		t.Fatalf("add again: %v, want nothing to do", err)
	}
	if n := count(t, pool, "SELECT count(*) FROM challenge_creators WHERE challenge_id = $1 AND user_id = $2", id, member); n != 1 {
		t.Fatalf("%d creator rows, want 1", n)
	}
	// The new creator has the creator rights: edit, and cannot be excluded.
	title := "Renamed by the new creator"
	if _, err := svc.UpdateText(t.Context(), member, id, nil, TextChanges{Title: &title}); err != nil {
		t.Fatalf("new creator edits: %v", err)
	}
	if err := svc.Exclude(t.Context(), creatorID, id, member); !errors.Is(err, apperr.New(apperr.CodeCannotExcludeCreator, nil)) {
		t.Fatalf("exclude the new creator: %v, want cannot_exclude_creator", err)
	}

	clk.Set(time.Date(2026, 4, 15, 4, 0, 0, 0, time.UTC)) // the exact end
	other := insertProfile(t, pool, "co_late")
	if _, err := pool.Exec(t.Context(), "INSERT INTO participants (challenge_id, user_id, status, joined_at) VALUES ($1, $2, 'active', $3)", id, other, now); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddCreator(t.Context(), creatorID, id, other); !errors.Is(err, apperr.New(apperr.CodeChallengeEnded, nil)) {
		t.Fatalf("after the end: %v, want challenge_ended", err)
	}
}

// TestAddCreatorAndExcludeRace: a creator makes a participant a creator
// while another excludes them. The shared lock serializes both: either the
// promotion wins (the exclusion answers cannot_exclude_creator) or the
// exclusion wins (the promotion answers not_active_participant). Never both.
func TestAddCreatorAndExcludeRace(t *testing.T) {
	svc, _, pool, id := joinFixture(t)
	var creatorID string
	if err := pool.QueryRow(t.Context(), "SELECT created_by FROM challenges WHERE id = $1", id).Scan(&creatorID); err != nil {
		t.Fatal(err)
	}
	const users = 8
	for i := range users {
		target := insertProfile(t, pool, "corace_"+string(rune('a'+i)))
		if _, err := svc.Join(t.Context(), target, id); err != nil {
			t.Fatal(err)
		}
		var addErr, excludeErr error
		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Go(func() { <-start; _, addErr = svc.AddCreator(t.Context(), creatorID, id, target) })
		wg.Go(func() { <-start; excludeErr = svc.Exclude(t.Context(), creatorID, id, target) })
		close(start)
		wg.Wait()

		var isCreator bool
		var status string
		if err := pool.QueryRow(t.Context(), `SELECT
			EXISTS (SELECT 1 FROM challenge_creators WHERE challenge_id = $1 AND user_id = $2),
			(SELECT status FROM participants WHERE challenge_id = $1 AND user_id = $2)`, id, target).Scan(&isCreator, &status); err != nil {
			t.Fatal(err)
		}
		switch {
		case addErr == nil && errors.Is(excludeErr, apperr.New(apperr.CodeCannotExcludeCreator, nil)) && isCreator && status == "active":
		case excludeErr == nil && errors.Is(addErr, apperr.New(apperr.CodeNotActiveParticipant, nil)) && !isCreator && status == "abandoned":
		default:
			t.Fatalf("target %d: add %v, exclude %v, creator %v, status %s: want exactly one to win", i, addErr, excludeErr, isCreator, status)
		}
	}
}

func TestDeleteMakesTheChallengeDisappear(t *testing.T) {
	svc, clk, pool, id := joinFixture(t)
	var creatorID string
	if err := pool.QueryRow(t.Context(), "SELECT created_by FROM challenges WHERE id = $1", id).Scan(&creatorID); err != nil {
		t.Fatal(err)
	}
	member := insertProfile(t, pool, "delete_member")
	if _, err := svc.Join(t.Context(), member, id); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(t.Context(), member, id); !errors.Is(err, apperr.New(apperr.CodeForbidden, nil)) {
		t.Fatalf("participant deletes: %v, want forbidden", err)
	}

	clk.Advance(48 * time.Hour) // ongoing
	if err := svc.Delete(t.Context(), creatorID, id); err != nil {
		t.Fatal(err)
	}
	var deletedAt time.Time
	if err := pool.QueryRow(t.Context(), "SELECT deleted_at FROM challenges WHERE id = $1", id).Scan(&deletedAt); err != nil || !deletedAt.Equal(clk.Now()) {
		t.Fatalf("deleted_at %s (%v), want the clock's now", deletedAt, err)
	}
	// Gone for everyone, creator included; data kept.
	for name, user := range map[string]string{"creator": creatorID, "participant": member} {
		if _, err := svc.Get(t.Context(), user, id); !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
			t.Fatalf("%s reads: %v, want challenge_not_found", name, err)
		}
	}
	if err := svc.Delete(t.Context(), creatorID, id); !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("delete again: %v, want challenge_not_found", err)
	}
	// Every write is refused from then on, and writes nothing.
	late := insertProfile(t, pool, "delete_late")
	if _, err := svc.Join(t.Context(), late, id); !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("join after deletion: %v, want challenge_not_found", err)
	}
	if n := count(t, pool, "SELECT count(*) FROM participants WHERE user_id = $1", late); n != 0 {
		t.Fatalf("%d participations written after the deletion, want 0", n)
	}
	title := "After the deletion"
	notFound := apperr.New(apperr.CodeChallengeNotFound, nil)
	for name, call := range map[string]func() error{
		"abandon":     func() error { _, err := svc.Abandon(t.Context(), member, id); return err },
		"exclude":     func() error { return svc.Exclude(t.Context(), creatorID, id, member) },
		"add creator": func() error { _, err := svc.AddCreator(t.Context(), creatorID, id, member); return err },
		"edit": func() error {
			_, err := svc.UpdateText(t.Context(), creatorID, id, nil, TextChanges{Title: &title})
			return err
		},
	} {
		if err := call(); !errors.Is(err, notFound) {
			t.Fatalf("%s after deletion: %v, want challenge_not_found", name, err)
		}
	}
	if n := count(t, pool, "SELECT count(*) FROM participants WHERE challenge_id = $1", id); n != 2 {
		t.Fatalf("%d participations left, want both kept", n)
	}
	if page, err := svc.List(t.Context(), member, ScopeMine, "", 20, nil); err != nil || len(page.Items) != 0 {
		t.Fatalf("mine after deletion: %d items (%v), want none", len(page.Items), err)
	}
	if page, err := svc.List(t.Context(), member, ScopePublic, "", 20, nil); err != nil || len(page.Items) != 0 {
		t.Fatalf("public after deletion: %d items (%v), want none", len(page.Items), err)
	}
}

// TestDeleteAndJoinRace: users join while a creator deletes. The lock on the
// challenge row serializes them: a join either commits before the deletion
// (and stays) or answers challenge_not_found, and there is never a 500.
func TestDeleteAndJoinRace(t *testing.T) {
	svc, _, pool, id := joinFixture(t)
	var creatorID string
	if err := pool.QueryRow(t.Context(), "SELECT created_by FROM challenges WHERE id = $1", id).Scan(&creatorID); err != nil {
		t.Fatal(err)
	}
	const users = 8
	ids := make([]string, users)
	for i := range users {
		ids[i] = insertProfile(t, pool, "delrace_"+string(rune('a'+i)))
	}
	joinErrs := make([]error, users)
	var deleteErr error
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range users {
		wg.Go(func() { <-start; _, joinErrs[i] = svc.Join(t.Context(), ids[i], id) })
	}
	wg.Go(func() { <-start; deleteErr = svc.Delete(t.Context(), creatorID, id) })
	close(start)
	wg.Wait()

	if deleteErr != nil {
		t.Fatalf("delete: %v", deleteErr)
	}
	// A join that commits just before the deletion stores its row but may
	// still answer challenge_not_found: the challenge is read again after
	// the commit. So every success has its row, and no join fails otherwise.
	for i, err := range joinErrs {
		stored := count(t, pool, "SELECT count(*) FROM participants WHERE challenge_id = $1 AND user_id = $2", id, ids[i])
		switch {
		case err == nil && stored != 1:
			t.Fatalf("join %d succeeded with %d rows, want 1", i, stored)
		case err != nil && !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)):
			t.Fatalf("join %d: %v, want nil or challenge_not_found", i, err)
		}
	}
}

func TestDeleteByACreatorWhoAbandoned(t *testing.T) {
	svc, _, pool, id := joinFixture(t)
	var creatorID string
	if err := pool.QueryRow(t.Context(), "SELECT created_by FROM challenges WHERE id = $1", id).Scan(&creatorID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Abandon(t.Context(), creatorID, id); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(t.Context(), creatorID, id); err != nil {
		t.Fatalf("creator who abandoned deletes: %v, want allowed", err)
	}
	if n := count(t, pool, "SELECT count(*) FROM challenges WHERE id = $1 AND deleted_at IS NOT NULL", id); n != 1 {
		t.Fatal("challenge not deleted")
	}
}

func TestParticipantsListing(t *testing.T) {
	svc, clk, pool, id := joinFixture(t)
	var creatorID string
	if err := pool.QueryRow(t.Context(), "SELECT created_by FROM challenges WHERE id = $1", id).Scan(&creatorID); err != nil {
		t.Fatal(err)
	}
	// Joined one minute apart, so the order of registration is known.
	ids := []string{creatorID}
	for _, subject := range []string{"list_a", "list_b", "list_c"} {
		clk.Advance(time.Minute)
		u := insertProfile(t, pool, subject)
		if _, err := svc.Join(t.Context(), u, id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, u)
	}
	if _, err := svc.Abandon(t.Context(), ids[1], id); err != nil {
		t.Fatal(err)
	}
	// A deleted account: abandoned and anonymized (ticket 039 does both).
	if _, err := pool.Exec(t.Context(), "UPDATE participants SET status = 'abandoned', abandoned_at = joined_at WHERE user_id = $1", ids[2]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE users SET anonymized_at = now() WHERE id = $1", ids[2]); err != nil {
		t.Fatal(err)
	}

	// An outsider of the public challenge reads it, two by two.
	outsider := insertProfile(t, pool, "list_outsider")
	var got []Participant
	var after *ParticipantPosition
	for range 3 {
		page, err := svc.Participants(t.Context(), outsider, id, 2, after)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, page.Items...)
		if after = page.Next; after == nil {
			break
		}
	}
	if len(got) != 4 {
		t.Fatalf("%d participants, want 4", len(got))
	}
	wantStatus := []ParticipantStatus{ParticipantActive, ParticipantAbandoned, ParticipantAbandoned, ParticipantActive}
	for i, p := range got {
		if p.UserID != ids[i] || p.Status != wantStatus[i] {
			t.Fatalf("participant %d: %s %s, want %s %s in order of registration", i, p.UserID, p.Status, ids[i], wantStatus[i])
		}
	}
	if p := got[1].Pseudo; p == nil || *p != "list_a" {
		t.Fatalf("abandoned participant pseudo %v, want list_a kept", p)
	}
	if got[2].Pseudo != nil {
		t.Fatalf("anonymized participant pseudo %q, want none", *got[2].Pseudo)
	}
	if !got[3].JoinedAt.Equal(now.Add(3 * time.Minute)) {
		t.Fatalf("joined at %s, want the instant of the join", got[3].JoinedAt)
	}

	// Private: the abandoned participant still sees the list, an outsider gets 404.
	if _, err := pool.Exec(t.Context(), "UPDATE challenges SET visibility = 'private' WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Participants(t.Context(), ids[1], id, 20, nil); err != nil {
		t.Fatalf("abandoned participant of a private challenge: %v", err)
	}
	notFound := apperr.New(apperr.CodeChallengeNotFound, nil)
	if _, err := svc.Participants(t.Context(), outsider, id, 20, nil); !errors.Is(err, notFound) {
		t.Fatalf("outsider of a private challenge: %v, want challenge_not_found", err)
	}
	// Deleted: hidden for everyone.
	if err := svc.Delete(t.Context(), creatorID, id); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Participants(t.Context(), creatorID, id, 20, nil); !errors.Is(err, notFound) {
		t.Fatalf("deleted challenge: %v, want challenge_not_found", err)
	}
}
