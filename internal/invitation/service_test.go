package invitation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

const (
	actor       = "0b9d4a5e-6a43-4c8e-9d1f-7a2b3c4d5e6f"
	challengeID = "11111111-1111-1111-1111-111111111111"
)

var (
	now      = time.Date(2026, 3, 4, 15, 0, 0, 0, time.UTC)
	deadline = time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC)
)

type fakeChallenges struct {
	err error
}

func (f fakeChallenges) InvitationDeadline(context.Context, string, string) (time.Time, error) {
	return deadline, f.err
}

type fakeRepository struct {
	err     error
	created *Invitation
	// valid is the token hash of the only valid invitation, to challengeID.
	valid []byte
	// linked records the link made: user and instant.
	linkedUser string
	linkedAt   time.Time
}

func (f *fakeRepository) Create(_ context.Context, inv Invitation) (Invitation, error) {
	if f.err != nil {
		return Invitation{}, f.err
	}
	inv.ID = "22222222-2222-2222-2222-222222222222"
	f.created = &inv
	return inv, nil
}

func (f *fakeRepository) Link(_ context.Context, tokenHash []byte, userID string, at time.Time) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if !bytes.Equal(tokenHash, f.valid) {
		return "", fmt.Errorf("link invitation: %w", errInvalid)
	}
	f.linkedUser, f.linkedAt = userID, at
	return challengeID, nil
}

func TestServiceCreate(t *testing.T) {
	repo := &fakeRepository{}
	inv, token, err := NewService(fakeChallenges{}, repo, clock.NewFake(now)).Create(t.Context(), actor, challengeID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		t.Fatalf("token %q decodes to %d bytes (%v), want 32 random bytes", token, len(raw), err)
	}
	hash := sha256.Sum256(raw)
	stored := repo.created
	if stored == nil || !bytes.Equal(stored.TokenHash, hash[:]) {
		t.Fatalf("stored %+v, want the SHA-256 of the token only", stored)
	}
	if stored.ChallengeID != challengeID || stored.InvitedBy != actor || !stored.ExpiresAt.Equal(deadline) || !stored.CreatedAt.Equal(now) {
		t.Fatalf("stored %+v, want by the actor, created at the clock's now, expiring at the end of week 1", stored)
	}
	if inv.ID == "" {
		t.Fatal("the stored invitation is not returned")
	}
}

// TestServiceCreateTokensDiffer: two invitations never share a token.
func TestServiceCreateTokensDiffer(t *testing.T) {
	svc := NewService(fakeChallenges{}, &fakeRepository{}, clock.NewFake(now))
	_, first, err := svc.Create(t.Context(), actor, challengeID)
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := svc.Create(t.Context(), actor, challengeID)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("both tokens are %q", first)
	}
}

// TestServiceCreateRefused: the challenge rule refuses (not visible,
// forbidden, registration closed), and nothing is written.
func TestServiceCreateRefused(t *testing.T) {
	for _, code := range []apperr.Code{apperr.CodeChallengeNotFound, apperr.CodeForbidden, apperr.CodeRegistrationClosed} {
		t.Run(string(code), func(t *testing.T) {
			repo := &fakeRepository{}
			_, token, err := NewService(fakeChallenges{err: apperr.New(code, nil)}, repo, clock.NewFake(now)).Create(t.Context(), actor, challengeID)
			if !errors.Is(err, apperr.New(code, nil)) {
				t.Fatalf("error = %v, want %s", err, code)
			}
			if repo.created != nil || token != "" {
				t.Fatal("invitation created despite the refusal")
			}
		})
	}
}

func TestServiceCreateStorageFails(t *testing.T) {
	storage := errors.New("connection refused")
	_, token, err := NewService(fakeChallenges{}, &fakeRepository{err: storage}, clock.NewFake(now)).Create(t.Context(), actor, challengeID)
	var appErr *apperr.Error
	if !errors.Is(err, storage) || errors.As(err, &appErr) || token != "" {
		t.Fatalf("error = %v, token %q, want it to wrap %v as an internal error, no token", err, token, storage)
	}
}

func TestServiceLink(t *testing.T) {
	raw := bytes.Repeat([]byte{7}, 32)
	hash := sha256.Sum256(raw)
	valid := base64.RawURLEncoding.EncodeToString(raw)
	tests := []struct {
		name  string
		token string
		want  apperr.Code // empty: linked
	}{
		{name: "valid", token: valid},
		{name: "unknown, expired or revoked", token: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32)), want: apperr.CodeInvitationInvalid},
		{name: "empty", token: "", want: apperr.CodeInvitationInvalid},
		{name: "not base64url", token: valid[:42] + "!", want: apperr.CodeInvitationInvalid},
		{name: "padded", token: valid + "=", want: apperr.CodeInvitationInvalid},
		{name: "another spelling of the same bytes", token: respelled(valid), want: apperr.CodeInvitationInvalid},
		{name: "too short", token: base64.RawURLEncoding.EncodeToString(raw[:31]), want: apperr.CodeInvitationInvalid},
		{name: "too long", token: base64.RawURLEncoding.EncodeToString(append(raw, 7)), want: apperr.CodeInvitationInvalid},
		{name: "the hash instead of the token", token: base64.RawURLEncoding.EncodeToString(hash[:]), want: apperr.CodeInvitationInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{valid: hash[:]}
			got, err := NewService(fakeChallenges{}, repo, clock.NewFake(now)).Link(t.Context(), actor, tt.token)
			if tt.want == "" {
				if err != nil || got != challengeID {
					t.Fatalf("Link() = %q, %v, want %s", got, err, challengeID)
				}
				if repo.linkedUser != actor || !repo.linkedAt.Equal(now) {
					t.Fatalf("linked %q at %s, want the actor at the clock's now", repo.linkedUser, repo.linkedAt)
				}
				return
			}
			if !errors.Is(err, apperr.New(tt.want, nil)) {
				t.Fatalf("error = %v, want %s", err, tt.want)
			}
			if repo.linkedUser != "" {
				t.Fatal("linked despite the refusal")
			}
		})
	}
}

// respelled sets the unused trailing bits of the last character of token:
// a lenient decoder reads the same bytes, a strict one refuses it.
func respelled(token string) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	last := strings.IndexByte(alphabet, token[len(token)-1])
	return token[:len(token)-1] + string(alphabet[last|1])
}

func TestServiceLinkStorageFails(t *testing.T) {
	storage := errors.New("connection refused")
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	_, err := NewService(fakeChallenges{}, &fakeRepository{err: storage}, clock.NewFake(now)).Link(t.Context(), actor, token)
	var appErr *apperr.Error
	if !errors.Is(err, storage) || errors.As(err, &appErr) {
		t.Fatalf("error = %v, want it to wrap %v as an internal error", err, storage)
	}
}
