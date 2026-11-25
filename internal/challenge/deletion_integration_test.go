//go:build integration

package challenge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/user"
)

// deletedAtAuth0 stands for the Auth0 Management API: every deletion works.
type deletedAtAuth0 struct{}

func (deletedAtAuth0) DeleteUser(context.Context, string) error { return nil }

// accounts returns the user service of pool, whose deletions lock the
// challenges of svc and erase the rows of the account there (the erasure of
// the other modules is tested in cmd/api, wired as in production).
func accounts(t *testing.T, pool *pgxpool.Pool, svc *Service) *user.Service {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	gdb, err := db.NewGorm(pool, logger)
	if err != nil {
		t.Fatal(err)
	}
	return user.NewService(user.NewRepository(gdb), svc, []user.Eraser{svc}, deletedAtAuth0{}, svc.clock, logger)
}

func statusOf(t *testing.T, pool *pgxpool.Pool, challengeID, userID string) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(t.Context(), "SELECT status FROM participants WHERE challenge_id = $1 AND user_id = $2", challengeID, userID).
		Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

// TestAccountDeletionErasesTheChallengeRows: the deletion leaves no row of
// the account in any challenge, upcoming, ongoing, finished, abandoned
// earlier or deleted (README §2.2), and keeps the challenges it created.
func TestAccountDeletionErasesTheChallengeRows(t *testing.T) {
	svc, clk, pool, ongoing := joinFixture(t)
	var creatorID string
	if err := pool.QueryRow(t.Context(), "SELECT created_by FROM challenges WHERE id = $1", ongoing).Scan(&creatorID); err != nil {
		t.Fatal(err)
	}
	member := insertProfile(t, pool, "del_member")
	if _, err := svc.Join(t.Context(), member, ongoing); err != nil {
		t.Fatal(err)
	}
	// One the member created (so a creator), finished since.
	finished, err := svc.Create(t.Context(), member, valid())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE challenges SET starts_at = starts_at - interval '1 year', ends_at = ends_at - interval '1 year' WHERE id = $1", finished.ID); err != nil {
		t.Fatal(err)
	}
	// One abandoned earlier, and one deleted.
	left, err := svc.Create(t.Context(), creatorID, valid())
	if err != nil {
		t.Fatal(err)
	}
	gone, err := svc.Create(t.Context(), creatorID, valid())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{left.ID, gone.ID} {
		if _, err := svc.Join(t.Context(), member, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.Abandon(t.Context(), member, left.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(t.Context(), creatorID, gone.ID); err != nil {
		t.Fatal(err)
	}
	// One upcoming, and one ending at the instant of the deletion.
	upcoming, err := svc.Create(t.Context(), creatorID, with(func(d *Draft) { d.StartDate = "2026-04-01" }))
	if err != nil {
		t.Fatal(err)
	}
	endingNow, err := svc.Create(t.Context(), creatorID, valid())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{upcoming.ID, endingNow.ID} {
		if _, err := svc.Join(t.Context(), member, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(t.Context(), "UPDATE challenges SET starts_at = $2::timestamptz - interval '7 days', ends_at = $2 WHERE id = $1", endingNow.ID, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	clk.Advance(time.Hour)
	users := accounts(t, pool, svc)
	if err := users.Delete(t.Context(), member); err != nil {
		t.Fatal(err)
	}

	for name, id := range map[string]string{"ongoing": ongoing, "finished": finished.ID, "abandoned before": left.ID, "deleted": gone.ID, "upcoming": upcoming.ID, "ending at the deletion": endingNow.ID} {
		if n := count(t, pool, "SELECT count(*) FROM participants WHERE challenge_id = $1 AND user_id = $2", id, member); n != 0 {
			t.Fatalf("%s challenge: %d participations of the deleted account, want none", name, n)
		}
	}
	if n := count(t, pool, "SELECT count(*) FROM challenge_creators WHERE user_id = $1", member); n != 0 {
		t.Fatalf("%d creator rows, want none", n)
	}
	if n := count(t, pool, "SELECT count(*) FROM challenges WHERE id = $1 AND created_by = $2", finished.ID, member); n != 1 {
		t.Fatal("the challenge the account created is gone, want it kept")
	}
	page, err := svc.Participants(t.Context(), auth.Actor{ID: creatorID}, ongoing, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range page.Items {
		if p.UserID == member {
			t.Fatalf("listed as %+v, want the deleted account left out", p)
		}
	}

	// A request read once the account is deleted: deleted, not an
	// incomplete profile (anonymization clears the pseudo).
	if _, err := svc.Create(t.Context(), member, valid()); !errors.Is(err, apperr.New(apperr.CodeAccountDeleted, nil)) {
		t.Fatalf("create after deletion: %v, want account_deleted", err)
	}
	if _, err := svc.Join(t.Context(), member, ongoing); !errors.Is(err, apperr.New(apperr.CodeAccountDeleted, nil)) {
		t.Fatalf("join after deletion: %v, want account_deleted", err)
	}

	// A login with the former subject (insertProfile uses the pseudo).
	if _, err := users.GetOrCreateBySubject(t.Context(), "del_member", ""); !errors.Is(err, apperr.New(apperr.CodeAccountDeleted, nil)) {
		t.Fatalf("login after deletion: %v, want account_deleted", err)
	}
	if n := count(t, pool, "SELECT count(*) FROM users WHERE auth_subject = 'del_member'"); n != 0 {
		t.Fatalf("%d accounts with the former subject, want none", n)
	}
}

// TestAccountDeletionRaces: the member joins challenges and a creator excludes
// them while the account is deleted. Whatever the order, no participation
// is left to the anonymized account (a challenge it created
// meanwhile included), nothing fails with a 500 and no deadlock: a join or a
// creation answers or is refused (account_deleted, or not_found when the
// profile is read once deleted), an exclusion answers, making them a creator
// answers or is refused once the account is deleted.
func TestAccountDeletionRaces(t *testing.T) {
	svc, _, pool, excludeFrom := joinFixture(t)
	var creatorID string
	if err := pool.QueryRow(t.Context(), "SELECT created_by FROM challenges WHERE id = $1", excludeFrom).Scan(&creatorID); err != nil {
		t.Fatal(err)
	}
	member := insertProfile(t, pool, "race_member")
	if _, err := svc.Join(t.Context(), member, excludeFrom); err != nil {
		t.Fatal(err)
	}
	promoteIn, err := svc.Create(t.Context(), creatorID, valid())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Join(t.Context(), member, promoteIn.ID); err != nil {
		t.Fatal(err)
	}
	const joins = 6
	ids := make([]string, joins)
	for i := range ids {
		c, err := svc.Create(t.Context(), creatorID, valid())
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = c.ID
	}
	users := accounts(t, pool, svc)

	joinErrs := make([]error, joins)
	var deleteErr, excludeErr, promoteErr, createErr error
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range ids {
		wg.Go(func() { <-start; _, joinErrs[i] = svc.Join(t.Context(), member, ids[i]) })
	}
	wg.Go(func() { <-start; excludeErr = svc.Exclude(t.Context(), creatorID, excludeFrom, member) })
	wg.Go(func() { <-start; _, promoteErr = svc.AddCreator(t.Context(), creatorID, promoteIn.ID, member) })
	wg.Go(func() { <-start; _, createErr = svc.Create(t.Context(), member, valid()) })
	wg.Go(func() { <-start; deleteErr = users.Delete(t.Context(), member) })
	close(start)
	wg.Wait()

	// The challenge the member creates may commit first: the account is then
	// its last creator, and its deletion is refused (README §2.2).
	lastCreator := errors.Is(deleteErr, apperr.New(apperr.CodeLastCreator, nil))
	if deleteErr != nil && !lastCreator {
		t.Fatalf("delete: %v, want done or last_creator", deleteErr)
	}
	refusedOrDone := func(err error, codes ...apperr.Code) bool {
		if err == nil {
			return true
		}
		for _, code := range codes {
			if errors.Is(err, apperr.New(code, nil)) {
				return true
			}
		}
		return false
	}
	// Excluded before the deletion, or not found once the account is deleted
	// (as for someone who never joined, SC-API-113).
	if !refusedOrDone(excludeErr, apperr.CodeNotFound) {
		t.Fatalf("exclude: %v, want done or not_found", excludeErr)
	}
	for i, err := range joinErrs {
		if !refusedOrDone(err, apperr.CodeAccountDeleted, apperr.CodeNotFound) {
			t.Fatalf("join %d: %v, want nil, account_deleted or not_found", i, err)
		}
	}
	// Made a creator before the deletion, or refused once the account is
	// deleted (README §2.2: not_active_participant).
	if !refusedOrDone(promoteErr, apperr.CodeNotActiveParticipant) {
		t.Fatalf("add creator: %v, want nil or not_active_participant", promoteErr)
	}
	if !refusedOrDone(createErr, apperr.CodeAccountDeleted, apperr.CodeNotFound) {
		t.Fatalf("create: %v, want nil, account_deleted or not_found", createErr)
	}
	if lastCreator {
		if createErr != nil {
			t.Fatalf("deletion refused as last creator, but the creation failed: %v", createErr)
		}
		if n := count(t, pool, "SELECT count(*) FROM users WHERE id = $1 AND anonymized_at IS NULL", member); n != 1 {
			t.Fatal("deletion refused, but the account is anonymized")
		}
		return
	}
	if n := count(t, pool, "SELECT count(*) FROM participants WHERE user_id = $1", member); n != 0 {
		t.Fatalf("%d participations left to the anonymized account, want 0", n)
	}
}

// TestAccountDeletionOfTheLastCreator follows the rule of the last creator
// (README §2.2): the deletion of an account that is the last creator, with an
// account, of a challenge not finished is refused (last_creator, listing it)
// and changes nothing; once another creator is added, it goes through.
// Finished and deleted challenges never block it, and a co-creator whose
// account is deleted does not count.
func TestAccountDeletionOfTheLastCreator(t *testing.T) {
	svc, _, pool, _ := joinFixture(t)
	member := insertProfile(t, pool, "last_creator")
	other := insertProfile(t, pool, "other_creator")
	gone := insertProfile(t, pool, "gone_creator")
	create := func(creators ...string) string {
		t.Helper()
		c, err := svc.Create(t.Context(), member, valid())
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range creators {
			if _, err := pool.Exec(t.Context(), "INSERT INTO challenge_creators (challenge_id, user_id) VALUES ($1, $2)", c.ID, id); err != nil {
				t.Fatal(err)
			}
		}
		return c.ID
	}
	quitter := insertProfile(t, pool, "quitter_creator")
	alone := create()
	upcoming, err := svc.Create(t.Context(), member, with(func(d *Draft) { d.StartDate = "2026-04-01" }))
	if err != nil {
		t.Fatal(err)
	}
	withGone := create(gone)
	// A co-creator who abandoned keeps the rights of a creator (README §5.7).
	withQuitter := create(quitter)
	if _, err := pool.Exec(t.Context(), "INSERT INTO participants (challenge_id, user_id, status, joined_at, abandoned_at) VALUES ($1, $2, 'abandoned', $3, $3)", withQuitter, quitter, now); err != nil {
		t.Fatal(err)
	}
	// Finished at the very instant of the deletion: it no longer blocks.
	endingNow := create()
	if _, err := pool.Exec(t.Context(), "UPDATE challenges SET starts_at = $2::timestamptz - interval '7 days', ends_at = $2 WHERE id = $1", endingNow, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE users SET anonymized_at = now() WHERE id = $1", gone); err != nil {
		t.Fatal(err)
	}
	finished := create()
	if _, err := pool.Exec(t.Context(), "UPDATE challenges SET starts_at = starts_at - interval '1 year', ends_at = ends_at - interval '1 year' WHERE id = $1", finished); err != nil {
		t.Fatal(err)
	}
	deleted := create()
	if err := svc.Delete(t.Context(), member, deleted); err != nil {
		t.Fatal(err)
	}
	users := accounts(t, pool, svc)

	err = users.Delete(t.Context(), member)
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != apperr.CodeLastCreator {
		t.Fatalf("delete: %v, want last_creator", err)
	}
	want := []string{alone, upcoming.ID, withGone}
	slices.Sort(want)
	if got := appErr.Params[apperr.ChallengesParam]; got != strings.Join(want, ",") {
		t.Fatalf("challenges = %q, want %q (not the finished, ending, deleted ones nor the one with a co-creator who abandoned)", got, strings.Join(want, ","))
	}
	if n := count(t, pool, "SELECT count(*) FROM users WHERE id = $1 AND anonymized_at IS NULL", member); n != 1 {
		t.Fatal("refused, but the account is anonymized")
	}
	if status := statusOf(t, pool, alone, member); status != "active" {
		t.Fatalf("refused, but the participation is %s", status)
	}

	// Another creator with an account in each: the deletion goes through.
	for _, id := range []string{alone, upcoming.ID, withGone} {
		if _, err := pool.Exec(t.Context(), "INSERT INTO challenge_creators (challenge_id, user_id) VALUES ($1, $2)", id, other); err != nil {
			t.Fatal(err)
		}
	}
	if err := users.Delete(t.Context(), member); err != nil {
		t.Fatalf("delete with another creator: %v", err)
	}
	if n := count(t, pool, "SELECT count(*) FROM participants WHERE challenge_id = $1 AND user_id = $2", alone, member); n != 0 {
		t.Fatalf("deleted, but %d participations left, want them erased", n)
	}
}

// TestAccountDeletionOfTwoLastCreatorsAtOnce: the only two creators with an
// account of a challenge delete their accounts at the same time. The lock on
// the challenge makes one wait for the other: exactly one deletion goes
// through, the other is refused (last_creator), so the challenge always keeps
// a creator with an account.
func TestAccountDeletionOfTwoLastCreatorsAtOnce(t *testing.T) {
	svc, _, pool, _ := joinFixture(t)
	first := insertProfile(t, pool, "first_creator")
	second := insertProfile(t, pool, "second_creator")
	c, err := svc.Create(t.Context(), first, valid())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "INSERT INTO challenge_creators (challenge_id, user_id) VALUES ($1, $2)", c.ID, second); err != nil {
		t.Fatal(err)
	}
	users := accounts(t, pool, svc)

	errs := make([]error, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, id := range []string{first, second} {
		wg.Go(func() { <-start; errs[i] = users.Delete(t.Context(), id) })
	}
	close(start)
	wg.Wait()

	var done, refused int
	for _, err := range errs {
		switch {
		case err == nil:
			done++
		case errors.Is(err, apperr.New(apperr.CodeLastCreator, nil)):
			refused++
		default:
			t.Fatalf("delete: %v, want done or last_creator", err)
		}
	}
	if done != 1 || refused != 1 {
		t.Fatalf("%d deleted, %d refused, want exactly one of each", done, refused)
	}
	if n := count(t, pool, `SELECT count(*) FROM challenge_creators cc JOIN users u ON u.id = cc.user_id
		WHERE cc.challenge_id = $1 AND u.anonymized_at IS NULL`, c.ID); n != 1 {
		t.Fatalf("%d creators with an account left, want 1", n)
	}
}

// TestAccountDeletionsCrossedNeverDeadlock: a creates x and joined z, b
// creates z and joined x, and both delete their accounts at once (each
// challenge keeps another creator, so neither is refused). Locking the
// challenges each deletion touches in one global order means they never wait
// for each other in a cycle: both go through, none fails with a deadlock.
func TestAccountDeletionsCrossedNeverDeadlock(t *testing.T) {
	svc, _, pool, _ := joinFixture(t)
	keeper := insertProfile(t, pool, "keeper")
	for round := range 5 {
		a := insertProfile(t, pool, fmt.Sprintf("cross_a%d", round))
		b := insertProfile(t, pool, fmt.Sprintf("cross_b%d", round))
		x, err := svc.Create(t.Context(), a, valid())
		if err != nil {
			t.Fatal(err)
		}
		z, err := svc.Create(t.Context(), b, valid())
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{x.ID, z.ID} {
			if _, err := pool.Exec(t.Context(), "INSERT INTO challenge_creators (challenge_id, user_id) VALUES ($1, $2)", id, keeper); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := svc.Join(t.Context(), a, z.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Join(t.Context(), b, x.ID); err != nil {
			t.Fatal(err)
		}
		users := accounts(t, pool, svc)
		errs := make([]error, 2)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i, id := range []string{a, b} {
			wg.Go(func() { <-start; errs[i] = users.Delete(t.Context(), id) })
		}
		close(start)
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d, delete %d: %v, want done", round, i, err)
			}
		}
	}
}
