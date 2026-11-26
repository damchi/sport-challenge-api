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
	start    = time.Date(2026, 3, 2, 5, 0, 0, 0, time.UTC)
	end      = time.Date(2026, 4, 13, 4, 0, 0, 0, time.UTC)
)

type fakeChallenges struct {
	// err refuses the invitation deadline, previewErr the preview, joinErr
	// the join.
	err, previewErr, joinErr error
	// joined, if set, records the join made: user and challenge.
	joined *[2]string
	// judgedAt, if set, records the instant the rule was judged at;
	// clock, if set, is moved to the end of week 1 once the rule passed,
	// as when week 1 ends between the rule and the insert.
	judgedAt *time.Time
	clock    *clock.FakeClock
}

func (f fakeChallenges) InvitationDeadline(_ context.Context, _, _ string, now time.Time) (time.Time, error) {
	if f.judgedAt != nil {
		*f.judgedAt = now
	}
	if f.err == nil && f.clock != nil {
		f.clock.Set(deadline)
	}
	return deadline, f.err
}

func (f fakeChallenges) Preview(_ context.Context, _, id string) (Challenge, error) {
	if f.previewErr != nil {
		return Challenge{}, f.previewErr
	}
	return Challenge{ID: id, Title: "Spring run", StartsAt: start, EndsAt: end}, nil
}

func (f fakeChallenges) Join(_ context.Context, actorID, id string) error {
	if f.joinErr != nil {
		return f.joinErr
	}
	if f.joined != nil {
		*f.joined = [2]string{actorID, id}
	}
	return nil
}

type fakeRepository struct {
	err     error
	created *Invitation
	// valid is the token hash of the only valid invitation, to challengeID.
	valid []byte
	// linked records the link made: user and instant.
	linkedUser string
	linkedAt   time.Time
	// rescheduled and rescheduledAt record the last Reschedule.
	rescheduled   string
	rescheduledAt time.Time
}

func (f *fakeRepository) Create(_ context.Context, inv Invitation) (Invitation, error) {
	if f.err != nil {
		return Invitation{}, f.err
	}
	inv.ID = "22222222-2222-2222-2222-222222222222"
	f.created = &inv
	return inv, nil
}

// EraseUser is covered by the integration tests.
func (f *fakeRepository) EraseUser(context.Context, string) error { return nil }

// Reschedule records what it is asked; f.err fails it. Its SQL is covered
// by the integration tests.
func (f *fakeRepository) Reschedule(_ context.Context, challengeID string, at time.Time) error {
	if f.err != nil {
		return f.err
	}
	f.rescheduled, f.rescheduledAt = challengeID, at
	return nil
}

func TestServiceReschedule(t *testing.T) {
	at := time.Date(2026, 3, 16, 4, 0, 0, 0, time.UTC)
	repo := &fakeRepository{}
	svc := NewService(nil, repo, clock.NewFake(at))
	if err := svc.Reschedule(t.Context(), "c1", at); err != nil {
		t.Fatal(err)
	}
	if repo.rescheduled != "c1" || !repo.rescheduledAt.Equal(at) {
		t.Fatalf("rescheduled %q at %v, want c1 at %v", repo.rescheduled, repo.rescheduledAt, at)
	}
	boom := errors.New("boom")
	if err := NewService(nil, &fakeRepository{err: boom}, clock.NewFake(at)).Reschedule(t.Context(), "c1", at); !errors.Is(err, boom) {
		t.Fatalf("Reschedule() = %v, want the error of the repository", err)
	}
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

// TestServiceCreateAtTheEndOfWeek1: week 1 ends between the rule, which let
// the invitation through a nanosecond before, and the insert. The invitation
// is created at the instant the rule judged, before it expires: never a
// violated invitations_expires_after_created_check.
func TestServiceCreateAtTheEndOfWeek1(t *testing.T) {
	repo := &fakeRepository{}
	clk := clock.NewFake(deadline.Add(-time.Nanosecond))
	var judgedAt time.Time
	if _, _, err := NewService(fakeChallenges{judgedAt: &judgedAt, clock: clk}, repo, clk).Create(t.Context(), actor, challengeID); err != nil {
		t.Fatal(err)
	}
	if !judgedAt.Equal(deadline.Add(-time.Nanosecond)) {
		t.Fatalf("rule judged at %s, want the clock's now before the insert", judgedAt)
	}
	if repo.created == nil || !repo.created.CreatedAt.Equal(judgedAt) || !repo.created.CreatedAt.Before(repo.created.ExpiresAt) {
		t.Fatalf("stored %+v, want created at %s, the instant the rule judged, before it expires", repo.created, judgedAt)
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

// TestServiceOfADeletedAccount: an account deleted while the request ran
// (the repository found it anonymized once locked) answers account_deleted.
func TestServiceOfADeletedAccount(t *testing.T) {
	deleted := fmt.Errorf("lock user: %w", errAccountDeleted)
	svc := NewService(fakeChallenges{}, &fakeRepository{err: deleted}, clock.NewFake(now))
	want := apperr.New(apperr.CodeAccountDeleted, nil)
	if _, token, err := svc.Create(t.Context(), actor, challengeID); !errors.Is(err, want) || token != "" {
		t.Fatalf("Create() = %v, token %q, want account_deleted and no token", err, token)
	}
	token, _ := validToken()
	if _, err := svc.Link(t.Context(), actor, token); !errors.Is(err, want) {
		t.Fatalf("Link() = %v, want account_deleted", err)
	}
}

// validToken is a well-formed token and its hash, the valid invitation of a
// fakeRepository.
func validToken() (token string, hash []byte) {
	raw := bytes.Repeat([]byte{7}, 32)
	sum := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(raw), sum[:]
}

func TestServicePreview(t *testing.T) {
	token, hash := validToken()
	storage := errors.New("connection refused")
	tests := []struct {
		name       string
		token      string
		previewErr error
		want       apperr.Code // empty: previewed
		wantLinked bool
	}{
		{name: "valid", token: token, wantLinked: true},
		{name: "invalid token", token: "nope", want: apperr.CodeInvitationInvalid},
		// The invitation stopped being valid, or the challenge was deleted,
		// right after the link: never an answer about the challenge.
		{name: "challenge hidden after the link", token: token, previewErr: apperr.New(apperr.CodeChallengeNotFound, nil), want: apperr.CodeInvitationInvalid, wantLinked: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{valid: hash}
			got, err := NewService(fakeChallenges{previewErr: tt.previewErr}, repo, clock.NewFake(now)).Preview(t.Context(), actor, tt.token)
			if linked := repo.linkedUser == actor; linked != tt.wantLinked {
				t.Fatalf("linked %v, want %v", linked, tt.wantLinked)
			}
			if tt.want == "" {
				want := Challenge{ID: challengeID, Title: "Spring run", StartsAt: start, EndsAt: end}
				if err != nil || got != want {
					t.Fatalf("Preview() = %+v, %v, want %+v", got, err, want)
				}
				return
			}
			if !errors.Is(err, apperr.New(tt.want, nil)) || errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
				t.Fatalf("error = %v, want %s only", err, tt.want)
			}
		})
	}

	_, err := NewService(fakeChallenges{previewErr: storage}, &fakeRepository{valid: hash}, clock.NewFake(now)).Preview(t.Context(), actor, token)
	var appErr *apperr.Error
	if !errors.Is(err, storage) || errors.As(err, &appErr) {
		t.Fatalf("error = %v, want %v as an internal error", err, storage)
	}
}

func TestServiceAccept(t *testing.T) {
	token, hash := validToken()
	tests := []struct {
		name    string
		token   string
		joinErr error
		want    apperr.Code // empty: joined
	}{
		{name: "valid", token: token},
		{name: "invalid token", token: "nope", want: apperr.CodeInvitationInvalid},
		{name: "abandoned before", token: token, joinErr: apperr.New(apperr.CodeParticipationAbandoned, nil), want: apperr.CodeParticipationAbandoned},
		{name: "already in", token: token, joinErr: apperr.New(apperr.CodeAlreadyParticipant, nil), want: apperr.CodeAlreadyParticipant},
		{name: "incomplete profile", token: token, joinErr: apperr.New(apperr.CodeProfileIncomplete, nil), want: apperr.CodeProfileIncomplete},
		{name: "registration closed while accepting", token: token, joinErr: apperr.New(apperr.CodeRegistrationClosed, nil), want: apperr.CodeRegistrationClosed},
		{name: "challenge hidden after the link", token: token, joinErr: apperr.New(apperr.CodeChallengeNotFound, nil), want: apperr.CodeInvitationInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var joined [2]string
			repo := &fakeRepository{valid: hash}
			got, err := NewService(fakeChallenges{joinErr: tt.joinErr, joined: &joined}, repo, clock.NewFake(now)).Accept(t.Context(), actor, tt.token)
			if tt.want == "" {
				if err != nil || got != challengeID || joined != [2]string{actor, challengeID} {
					t.Fatalf("Accept() = %q, %v, joined %v, want the actor in %s", got, err, joined, challengeID)
				}
				return
			}
			if !errors.Is(err, apperr.New(tt.want, nil)) || got != "" {
				t.Fatalf("Accept() = %q, %v, want %s", got, err, tt.want)
			}
			if joined != [2]string{} {
				t.Fatal("joined despite the refusal")
			}
		})
	}
}
