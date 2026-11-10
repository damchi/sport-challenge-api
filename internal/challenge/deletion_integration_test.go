//go:build integration

package challenge

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/user"
)

// deletedAtAuth0 stands for the Auth0 Management API: every deletion works.
type deletedAtAuth0 struct{}

func (deletedAtAuth0) DeleteUser(context.Context, string) error { return nil }

// accounts returns the user service of pool, whose deletions abandon the
// challenges of svc, as cmd/api wires it.
func accounts(t *testing.T, pool *pgxpool.Pool, svc *Service) *user.Service {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	gdb, err := db.NewGorm(pool, logger)
	if err != nil {
		t.Fatal(err)
	}
	return user.NewService(user.NewRepository(gdb), svc, deletedAtAuth0{}, logger)
}

func statusOf(t *testing.T, pool *pgxpool.Pool, challengeID, userID string) (string, *time.Time) {
	t.Helper()
	var status string
	var abandonedAt *time.Time
	if err := pool.QueryRow(t.Context(), "SELECT status, abandoned_at FROM participants WHERE challenge_id = $1 AND user_id = $2", challengeID, userID).
		Scan(&status, &abandonedAt); err != nil {
		t.Fatal(err)
	}
	return status, abandonedAt
}

func TestAccountDeletionAbandonsTheChallenges(t *testing.T) {
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
	_, leftAt := statusOf(t, pool, left.ID, member)
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

	if status, at := statusOf(t, pool, ongoing, member); status != "abandoned" || at == nil || !at.Equal(now.Add(time.Hour)) {
		t.Fatalf("ongoing challenge: %s at %v, want abandoned at the clock's now", status, at)
	}
	if status, _ := statusOf(t, pool, finished.ID, member); status != "active" {
		t.Fatalf("finished challenge: %s, want active: its results stay", status)
	}
	if status, at := statusOf(t, pool, left.ID, member); status != "abandoned" || !at.Equal(*leftAt) {
		t.Fatalf("challenge abandoned before: %s at %v, want the first abandon kept (%v)", status, at, leftAt)
	}
	if status, _ := statusOf(t, pool, gone.ID, member); status != "active" {
		t.Fatalf("deleted challenge: %s, want untouched", status)
	}
	if status, _ := statusOf(t, pool, upcoming.ID, member); status != "abandoned" {
		t.Fatalf("upcoming challenge: %s, want abandoned", status)
	}
	if status, _ := statusOf(t, pool, endingNow.ID, member); status != "active" {
		t.Fatalf("challenge ending at the deletion: %s, want active: finished from that instant", status)
	}
	// The creator stays a creator of the finished challenge, under no name.
	if n := count(t, pool, "SELECT count(*) FROM challenge_creators WHERE challenge_id = $1 AND user_id = $2", finished.ID, member); n != 1 {
		t.Fatalf("%d creator rows, want the link kept", n)
	}
	page, err := svc.Participants(t.Context(), creatorID, ongoing, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range page.Items {
		if p.UserID == member && (p.Pseudo != nil || p.Status != ParticipantAbandoned) {
			t.Fatalf("listed as %+v, want abandoned without pseudo", p)
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
	if _, err := users.GetOrCreateBySubject(t.Context(), "del_member"); !errors.Is(err, apperr.New(apperr.CodeAccountDeleted, nil)) {
		t.Fatalf("login after deletion: %v, want account_deleted", err)
	}
	if n := count(t, pool, "SELECT count(*) FROM users WHERE auth_subject = 'del_member'"); n != 0 {
		t.Fatalf("%d accounts with the former subject, want none", n)
	}
}

// TestAccountDeletionRaces: the member joins challenges and a creator excludes
// them while the account is deleted. Whatever the order, no active
// participation is left to the anonymized account (a challenge it created
// meanwhile included), nothing fails with a 500 and no deadlock: a join or a
// creation answers or is refused (account_deleted, or not_found when the
// profile is read once deleted), an exclusion answers, making them a creator
// answers or is refused once they abandoned.
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

	if deleteErr != nil {
		t.Fatalf("delete: %v", deleteErr)
	}
	if excludeErr != nil {
		t.Fatalf("exclude: %v, want done or nothing to do", excludeErr)
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
	for i, err := range joinErrs {
		if !refusedOrDone(err, apperr.CodeAccountDeleted, apperr.CodeNotFound) {
			t.Fatalf("join %d: %v, want nil, account_deleted or not_found", i, err)
		}
	}
	// Made a creator before the deletion, or refused once abandoned.
	if !refusedOrDone(promoteErr, apperr.CodeNotActiveParticipant) {
		t.Fatalf("add creator: %v, want nil or not_active_participant", promoteErr)
	}
	if !refusedOrDone(createErr, apperr.CodeAccountDeleted, apperr.CodeNotFound) {
		t.Fatalf("create: %v, want nil, account_deleted or not_found", createErr)
	}
	if n := count(t, pool, "SELECT count(*) FROM participants WHERE user_id = $1 AND status = 'active'", member); n != 0 {
		t.Fatalf("%d active participations left to the anonymized account, want 0", n)
	}
}
