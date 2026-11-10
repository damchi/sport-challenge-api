//go:build integration

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/challenge"
	"github.com/damchi/sport-challenge-api/internal/invitation"
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
			users := user.NewService(user.NewRepository(gdb), nil, nil, logger)
			challenges := challenge.NewService(challenge.NewRepository(gdb), users, sport.NewService(sport.NewRepository(gdb)), clk)
			invitations := invitation.NewService(challenges, invitation.NewRepository(gdb), clk)

			insertUser := func(subject string) string {
				var id string
				if err := pool.QueryRow(t.Context(), "INSERT INTO users (auth_subject, pseudo) VALUES ($1, $1) RETURNING id", subject).Scan(&id); err != nil {
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
