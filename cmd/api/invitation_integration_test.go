//go:build integration

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/challenge"
	"github.com/damchi/sport-challenge-api/internal/invitation"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
	"github.com/damchi/sport-challenge-api/internal/sport"
	"github.com/damchi/sport-challenge-api/internal/user"
)

// TestInvitationValidityAgreesAcrossModules: the rule "an invitation counts
// while its author may invite" is written twice in SQL, in the membership
// query of module challenge and in the link query of module invitation.
// For every situation of the author, opening the link and reading the
// private challenge through an earlier link must both follow
// challenge.CanInvite, the rule in Go: changing it without both queries
// fails here.
func TestInvitationValidityAgreesAcrossModules(t *testing.T) {
	tests := []struct {
		name             string
		creator          bool
		status           challenge.ParticipantStatus // empty: never joined
		membersCanInvite bool
	}{
		{name: "creator", creator: true, status: challenge.ParticipantActive},
		{name: "creator who abandoned", creator: true, status: challenge.ParticipantAbandoned},
		{name: "creator, members may invite", creator: true, status: challenge.ParticipantActive, membersCanInvite: true},
		{name: "active participant, members may invite", status: challenge.ParticipantActive, membersCanInvite: true},
		{name: "active participant, members may not invite", status: challenge.ParticipantActive},
		{name: "abandoned or excluded participant", status: challenge.ParticipantAbandoned, membersCanInvite: true},
		{name: "abandoned participant, members may not invite", status: challenge.ParticipantAbandoned},
		{name: "never joined", membersCanInvite: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			role := challenge.RoleOf(challenge.Membership{Creator: tt.creator, Participant: tt.status})
			want := challenge.CanInvite(role, tt.membersCanInvite)
			pool := testdb.New(t)
			logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
			gdb, err := db.NewGorm(pool, logger)
			if err != nil {
				t.Fatal(err)
			}
			clk := clock.NewFake(recordNow)
			users := user.NewService(user.NewRepository(gdb), nil, nil, nil, logger)
			challenges := challenge.NewService(challenge.NewRepository(gdb), challengeProfiles{users}, sport.NewService(sport.NewRepository(gdb)), standingsFunc(nil), clk)
			invitations := invitation.NewService(invitationChallenges{challenges}, invitation.NewRepository(gdb), clk)

			insertUser := func(subject string) string {
				var id string
				if err := pool.QueryRow(t.Context(), "INSERT INTO users (auth_subject, pseudo, first_name, last_name) VALUES ($1, $1, 'Test', 'User') RETURNING id", subject).Scan(&id); err != nil {
					t.Fatal(err)
				}
				return id
			}
			exec := func(sql string, args ...any) {
				t.Helper()
				if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
					t.Fatal(err)
				}
			}
			owner, author, linked, opener := insertUser("owner"), insertUser("author"), insertUser("linked"), insertUser("opener")

			// A private challenge in week 2 at recordNow; the invitation
			// expires later, so only the author's rights decide.
			start := toronto(t, 2026, 6, 1, 0)
			var id string
			if err := pool.QueryRow(t.Context(), `INSERT INTO challenges (title, visibility, members_can_invite, timezone, starts_at, ends_at, created_by)
				VALUES ('Spring', 'private', $1, 'America/Toronto', $2, $3, $4) RETURNING id`,
				tt.membersCanInvite, start, start.AddDate(0, 0, 21), owner).Scan(&id); err != nil {
				t.Fatal(err)
			}
			exec("INSERT INTO challenge_creators (challenge_id, user_id) VALUES ($1, $2)", id, owner)
			if tt.creator {
				exec("INSERT INTO challenge_creators (challenge_id, user_id) VALUES ($1, $2)", id, author)
			}
			switch tt.status {
			case challenge.ParticipantActive:
				exec("INSERT INTO participants VALUES ($1, $2, 'active', $3, NULL)", id, author, start)
			case challenge.ParticipantAbandoned:
				exec("INSERT INTO participants VALUES ($1, $2, 'abandoned', $3, $4)", id, author, start, recordNow)
			}

			raw := bytes.Repeat([]byte{9}, 32)
			hash := sha256.Sum256(raw)
			var invitationID string
			if err := pool.QueryRow(t.Context(), `INSERT INTO invitations (challenge_id, invited_by, token_hash, expires_at, created_at)
				VALUES ($1, $2, $3, $4, $5) RETURNING id`, id, author, hash[:], recordNow.Add(24*time.Hour), start).Scan(&invitationID); err != nil {
				t.Fatal(err)
			}
			exec("INSERT INTO invitation_recipients VALUES ($1, $2, $3)", invitationID, linked, start)

			_, linkErr := invitations.Link(t.Context(), opener, base64.RawURLEncoding.EncodeToString(raw))
			_, readErr := challenges.Get(t.Context(), linked, id)
			if opened, read := linkErr == nil, readErr == nil; opened != want || read != want {
				t.Fatalf("link opened %v (%v), challenge read %v (%v), want both %v (CanInvite)", opened, linkErr, read, readErr, want)
			}
		})
	}
}

// TestAcceptInvitation: the whole path of an invitation link, wired as run
// does: a creator creates it, a user opens it and reads the private
// challenge, then joins by accepting it, under the join rules (never twice,
// never after an abandon, never from the end of week 1).
func TestAcceptInvitation(t *testing.T) {
	pool := testdb.New(t)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	gdb, err := db.NewGorm(pool, logger)
	if err != nil {
		t.Fatal(err)
	}
	// A private challenge in its week 1.
	start := toronto(t, 2026, 6, 1, 0)
	deadline := start.AddDate(0, 0, 7)
	clk := clock.NewFake(start.Add(24 * time.Hour))
	users := user.NewService(user.NewRepository(gdb), nil, nil, nil, logger)
	challenges := challenge.NewService(challenge.NewRepository(gdb), challengeProfiles{users}, sport.NewService(sport.NewRepository(gdb)), standingsFunc(nil), clk)
	invitations := invitation.NewService(invitationChallenges{challenges}, invitation.NewRepository(gdb), clk)

	insertUser := func(subject string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(t.Context(), "INSERT INTO users (auth_subject, pseudo, first_name, last_name) VALUES ($1, $1, 'Test', 'User') RETURNING id", subject).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	status := func(userID, challengeID string) string {
		t.Helper()
		var s string
		if err := pool.QueryRow(t.Context(), "SELECT COALESCE((SELECT status FROM participants WHERE challenge_id = $1 AND user_id = $2), '')", challengeID, userID).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	owner, guest, stranger, quitter, late := insertUser("owner"), insertUser("guest"), insertUser("stranger"), insertUser("quitter"), insertUser("late")
	var id string
	if err := pool.QueryRow(t.Context(), `INSERT INTO challenges (title, visibility, members_can_invite, timezone, starts_at, ends_at, created_by)
		VALUES ('Spring', 'private', false, 'America/Toronto', $1, $2, $3) RETURNING id`, start, start.AddDate(0, 0, 21), owner).Scan(&id); err != nil {
		t.Fatal(err)
	}
	exec("INSERT INTO challenge_creators (challenge_id, user_id) VALUES ($1, $2)", id, owner)
	exec("INSERT INTO participants VALUES ($1, $2, 'active', $3, NULL)", id, owner, start)
	exec("INSERT INTO participants VALUES ($1, $2, 'abandoned', $3, $3)", id, quitter, start)

	inv, token, err := invitations.Create(t.Context(), owner, id)
	if err != nil || !inv.ExpiresAt.Equal(deadline) {
		t.Fatalf("Create() = %+v, %v, want an invitation expiring at the end of week 1", inv, err)
	}
	if _, _, err := invitations.Create(t.Context(), guest, id); !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("invitation by a stranger: error = %v, want challenge_not_found", err)
	}

	// Opening the link: a preview, and from then on the challenge is read.
	if _, err := challenges.Get(t.Context(), guest, id); !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("before opening the link: error = %v, want challenge_not_found", err)
	}
	preview, err := invitations.Preview(t.Context(), guest, token)
	if want := (invitation.Challenge{ID: id, Title: "Spring", StartsAt: start, EndsAt: start.AddDate(0, 0, 21)}); err != nil ||
		preview.ID != want.ID || preview.Title != want.Title || !preview.StartsAt.Equal(want.StartsAt) || !preview.EndsAt.Equal(want.EndsAt) {
		t.Fatalf("Preview() = %+v, %v, want %+v", preview, err, want)
	}
	if d, err := challenges.Get(t.Context(), guest, id); err != nil || d.Role != challenge.RoleInvited || !d.CanJoin {
		t.Fatalf("after opening the link: %+v, %v, want invited, may join", d.Role, err)
	}
	// An invited user who has not joined may not invite.
	if _, _, err := invitations.Create(t.Context(), guest, id); !errors.Is(err, apperr.New(apperr.CodeForbidden, nil)) {
		t.Fatalf("invitation by an invited user: error = %v, want forbidden", err)
	}

	// Accepting joins, once.
	if got, err := invitations.Accept(t.Context(), guest, token); err != nil || got != id || status(guest, id) != "active" {
		t.Fatalf("Accept() = %q, %v, participation %q, want an active participant of %s", got, err, status(guest, id), id)
	}
	if _, err := invitations.Accept(t.Context(), guest, token); !errors.Is(err, apperr.New(apperr.CodeAlreadyParticipant, nil)) {
		t.Fatalf("accepting twice: error = %v, want already_participant", err)
	}

	// An invalid token joins nothing.
	bogus := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	if _, err := invitations.Accept(t.Context(), stranger, bogus); !errors.Is(err, apperr.New(apperr.CodeInvitationInvalid, nil)) || status(stranger, id) != "" {
		t.Fatalf("invalid token: error = %v, participation %q, want invitation_invalid and none", err, status(stranger, id))
	}

	// An abandon is final, even with a valid invitation.
	if _, err := invitations.Accept(t.Context(), quitter, token); !errors.Is(err, apperr.New(apperr.CodeParticipationAbandoned, nil)) || status(quitter, id) != "abandoned" {
		t.Fatalf("abandoned before: error = %v, participation %q, want participation_abandoned, still abandoned", err, status(quitter, id))
	}

	// From the exact end of week 1 the invitation is expired: nobody joins,
	// not even a user who opened the link in time.
	clk.Set(deadline.Add(-time.Second))
	if _, err := invitations.Preview(t.Context(), late, token); err != nil {
		t.Fatalf("one second before the deadline: %v, want the link opened", err)
	}
	clk.Set(deadline)
	if _, err := invitations.Accept(t.Context(), late, token); !errors.Is(err, apperr.New(apperr.CodeInvitationInvalid, nil)) || status(late, id) != "" {
		t.Fatalf("at the deadline: error = %v, participation %q, want invitation_invalid and none", err, status(late, id))
	}
	if _, err := challenges.Join(t.Context(), late, id); !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) || status(late, id) != "" {
		t.Fatalf("join at the deadline: error = %v, participation %q, want challenge_not_found and none", err, status(late, id))
	}
}
