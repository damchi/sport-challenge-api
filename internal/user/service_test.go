package user

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
)

type fakeRepository struct {
	user User
	err  error
	// changes records the last UpdateProfile call, when set.
	changes *ProfileChanges
	pseudos map[string]string
	// deleted records the user Delete anonymized, when set; deleteErr fails
	// Delete, pending records AddPendingAuth0Deletion and pendingErr fails it.
	deleted    *string
	deleteErr  error
	pending    *[]string
	pendingErr error
}

func (f fakeRepository) Delete(ctx context.Context, id string, abandon func(context.Context) error) (string, error) {
	if f.deleteErr != nil {
		return "", f.deleteErr
	}
	if err := abandon(ctx); err != nil {
		return "", err
	}
	if f.deleted != nil {
		*f.deleted = id
	}
	return f.user.AuthSubject, nil
}

func (f fakeRepository) AddPendingAuth0Deletion(_ context.Context, subject string) error {
	if f.pendingErr != nil {
		return f.pendingErr
	}
	if f.pending != nil {
		*f.pending = append(*f.pending, subject)
	}
	return nil
}

type fakeParticipations struct {
	abandoned string
	err       error
}

func (f *fakeParticipations) AbandonAll(_ context.Context, userID string) error {
	f.abandoned = userID
	return f.err
}

type fakeIdentities struct {
	deleted string
	err     error
}

func (f *fakeIdentities) DeleteUser(_ context.Context, subject string) error {
	f.deleted = subject
	return f.err
}

func (f fakeRepository) GetOrCreateBySubject(context.Context, string) (User, error) {
	return f.user, f.err
}

func (f fakeRepository) GetByID(context.Context, string) (User, error) {
	return f.user, f.err
}

func (f fakeRepository) UpdateProfile(_ context.Context, _ string, changes ProfileChanges) (User, error) {
	if f.changes != nil {
		*f.changes = changes
	}
	return f.user, f.err
}

func (f fakeRepository) Pseudos(context.Context, []string) (map[string]string, error) {
	return f.pseudos, f.err
}

func TestServicePseudos(t *testing.T) {
	storageErr := errors.New("connection reset")
	stored := map[string]string{"u1": "marie_92"}
	tests := []struct {
		name    string
		repo    fakeRepository
		ids     []string
		want    map[string]string
		wantErr error
	}{
		// No user to look up: the database is not queried.
		{name: "nobody", repo: fakeRepository{err: storageErr}, want: map[string]string{}},
		{name: "some users", repo: fakeRepository{pseudos: stored}, ids: []string{"u1", "u2"}, want: stored},
		{name: "storage failure", repo: fakeRepository{err: storageErr}, ids: []string{"u1"}, wantErr: storageErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewService(tt.repo, nil, nil, discard).Pseudos(t.Context(), tt.ids)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && (got == nil || len(got) != len(tt.want) || got["u1"] != tt.want["u1"]) {
				t.Fatalf("Pseudos() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestServiceGetOrCreateBySubject(t *testing.T) {
	deletedAt := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	storageErr := errors.New("connection reset")

	tests := []struct {
		name     string
		repo     fakeRepository
		wantID   string
		wantCode apperr.Code
		wantErr  error
	}{
		{name: "active user", repo: fakeRepository{user: User{ID: "u-1", AuthSubject: "email|1"}}, wantID: "u-1"},
		{name: "anonymized account", repo: fakeRepository{user: User{ID: "u-2", AnonymizedAt: &deletedAt}}, wantCode: apperr.CodeAccountDeleted},
		{name: "subject of a deleted account", repo: fakeRepository{err: fmt.Errorf("read user: %w", errAccountDeleted)}, wantCode: apperr.CodeAccountDeleted},
		{name: "storage error", repo: fakeRepository{err: storageErr}, wantErr: storageErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewService(tt.repo, nil, nil, discard).GetOrCreateBySubject(t.Context(), "email|1")

			switch {
			case tt.wantCode != "":
				if !errors.Is(err, apperr.New(tt.wantCode, nil)) {
					t.Fatalf("error = %v, want %s", err, tt.wantCode)
				}
				if got != (User{}) {
					t.Fatalf("user = %+v, want none for an anonymized account", got)
				}
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want it to wrap %v", err, tt.wantErr)
				}
			default:
				if err != nil || got.ID != tt.wantID {
					t.Fatalf("got %+v, %v, want user %s", got, err, tt.wantID)
				}
			}
		})
	}
}

func ptr(v string) *string { return &v }

// discard is the logger of the tests that do not read the logs.
var discard = slog.New(slog.NewJSONHandler(io.Discard, nil))

func TestServiceProfile(t *testing.T) {
	storageErr := errors.New("connection reset")
	tests := []struct {
		name       string
		repo       fakeRepository
		wantStatus int
		wantErr    error
	}{
		{name: "found", repo: fakeRepository{user: User{ID: "u-1", Pseudo: ptr("marie")}}},
		{name: "not found", repo: fakeRepository{err: fmt.Errorf("read user: %w", errUserNotFound)}, wantStatus: http.StatusNotFound},
		{name: "storage error", repo: fakeRepository{err: storageErr}, wantErr: storageErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewService(tt.repo, nil, nil, discard).Profile(t.Context(), "u-1")
			var appErr *apperr.Error
			switch {
			case tt.wantStatus != 0:
				if !errors.As(err, &appErr) || appErr.Status != tt.wantStatus {
					t.Fatalf("error = %v, want status %d", err, tt.wantStatus)
				}
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) || errors.As(err, &appErr) {
					t.Fatalf("error = %v, want it to wrap %v as an internal error", err, tt.wantErr)
				}
			default:
				if err != nil || got.ID != "u-1" {
					t.Fatalf("got %+v, %v, want user u-1", got, err)
				}
			}
		})
	}
}

func TestServiceUpdateProfile(t *testing.T) {
	stored := User{ID: "u-1", Pseudo: ptr("Marie"), Language: ptr("fr")}
	tests := []struct {
		name        string
		changes     ProfileChanges
		repoErr     error
		wantStatus  int
		wantFields  string
		wantWritten bool
	}{
		{name: "pseudo and language", changes: ProfileChanges{Pseudo: ptr("Marie"), Language: ptr("fr")}, wantWritten: true},
		{name: "nothing to change", wantWritten: true},
		{name: "invalid pseudo and language", changes: ProfileChanges{Pseudo: ptr("é"), Language: ptr("de")}, wantStatus: http.StatusBadRequest, wantFields: "language,pseudo"},
		{name: "invalid language only", changes: ProfileChanges{Pseudo: ptr("marie"), Language: ptr("FR")}, wantStatus: http.StatusBadRequest, wantFields: "language"},
		{name: "pseudo taken", changes: ProfileChanges{Pseudo: ptr("marie")}, repoErr: fmt.Errorf("update user: %w", errPseudoTaken), wantStatus: http.StatusConflict, wantFields: "pseudo", wantWritten: true},
		{name: "user gone", changes: ProfileChanges{Language: ptr("en")}, repoErr: fmt.Errorf("update user: %w", errUserNotFound), wantStatus: http.StatusNotFound, wantWritten: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var written ProfileChanges
			sentinel := ProfileChanges{Pseudo: ptr("not-called")}
			written = sentinel
			repo := fakeRepository{user: stored, err: tt.repoErr, changes: &written}

			got, err := NewService(repo, nil, nil, discard).UpdateProfile(t.Context(), "u-1", tt.changes)

			if wrote := written != sentinel; wrote != tt.wantWritten {
				t.Fatalf("repository called = %v, want %v", wrote, tt.wantWritten)
			}
			if tt.wantStatus == 0 {
				if err != nil || got.ID != "u-1" {
					t.Fatalf("got %+v, %v, want the stored user", got, err)
				}
				return
			}
			var appErr *apperr.Error
			if !errors.As(err, &appErr) || appErr.Status != tt.wantStatus || appErr.Params[apperr.FieldsParam] != tt.wantFields {
				t.Fatalf("error = %+v, want status %d with fields %q", appErr, tt.wantStatus, tt.wantFields)
			}
		})
	}
}

func TestServiceProfileComplete(t *testing.T) {
	storageErr := errors.New("connection reset")
	tests := []struct {
		name    string
		repo    fakeRepository
		want    bool
		wantErr bool
	}{
		{name: "pseudo set", repo: fakeRepository{user: User{ID: "u-1", Pseudo: ptr("marie")}}, want: true},
		{name: "no pseudo", repo: fakeRepository{user: User{ID: "u-1"}}},
		{name: "storage error", repo: fakeRepository{err: storageErr}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewService(tt.repo, nil, nil, discard).ProfileComplete(t.Context(), "u-1")
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("ProfileComplete() = %v, %v, want %v (error %v)", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestServiceDelete(t *testing.T) {
	storageErr := errors.New("connection reset")
	auth0Err := errors.New("auth0: status 503")
	tests := []struct {
		name           string
		deleteErr      error
		abandonErr     error
		auth0Err       error
		pendingErr     error
		wantCode       apperr.Code
		wantErr        error
		wantAuth0      bool
		wantPending    bool
		wantAnonymized bool
	}{
		{name: "deleted, then at Auth0", wantAuth0: true, wantAnonymized: true},
		{name: "Auth0 fails: recorded for the worker", auth0Err: auth0Err, wantAuth0: true, wantPending: true, wantAnonymized: true},
		{name: "Auth0 fails and the record too", auth0Err: auth0Err, pendingErr: storageErr, wantErr: storageErr, wantAuth0: true, wantAnonymized: true},
		// The abandons fail: everything rolls back, nothing is deleted at Auth0.
		{name: "abandon failure", abandonErr: storageErr, wantErr: storageErr},
		{name: "deleted meanwhile", deleteErr: fmt.Errorf("delete user: %w", errUserNotFound), wantCode: apperr.CodeAccountDeleted},
		{name: "storage failure", deleteErr: storageErr, wantErr: storageErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pending []string
			repo := fakeRepository{
				user: User{ID: "u-1", AuthSubject: "email|1"}, deleted: new(string),
				deleteErr: tt.deleteErr, pending: &pending, pendingErr: tt.pendingErr,
			}
			participations := &fakeParticipations{err: tt.abandonErr}
			identities := &fakeIdentities{err: tt.auth0Err}
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			err := NewService(repo, participations, identities, logger).Delete(t.Context(), "u-1")
			switch {
			case tt.wantCode != "":
				if !errors.Is(err, apperr.New(tt.wantCode, nil)) {
					t.Fatalf("error = %v, want %s", err, tt.wantCode)
				}
			case !errors.Is(err, tt.wantErr):
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantAnonymized && (*repo.deleted != "u-1" || participations.abandoned != "u-1") {
				t.Fatalf("anonymized %q, abandoned %q, want both for u-1", *repo.deleted, participations.abandoned)
			}
			if got := identities.deleted == "email|1"; got != tt.wantAuth0 {
				t.Fatalf("deleted at Auth0: %v, want %v", got, tt.wantAuth0)
			}
			if got := len(pending) == 1 && pending[0] == "email|1"; got != tt.wantPending {
				t.Fatalf("pending %v, want recorded: %v", pending, tt.wantPending)
			}
			// A failure at Auth0 leaves a trace, never the subject.
			if got := strings.Contains(logs.String(), "recorded for retry"); got != tt.wantPending {
				t.Fatalf("logs %q, want the warning: %v", logs.String(), tt.wantPending)
			}
			if strings.Contains(logs.String(), "email|1") {
				t.Fatalf("logs %q carry the subject", logs.String())
			}
		})
	}
}

func TestServiceDeleteSurvivesTheClientLeaving(t *testing.T) {
	// The request is cancelled once the account is deleted: Auth0 is still called.
	ctx, cancel := context.WithCancel(t.Context())
	identities := &ctxIdentities{}
	repo := fakeRepository{user: User{ID: "u-1", AuthSubject: "email|1"}}
	err := NewService(repo, abandonThen(cancel), identities, discard).Delete(ctx, "u-1")
	if err != nil || identities.ctxErr != nil {
		t.Fatalf("error %v, Auth0 called with a context error %v, want neither", err, identities.ctxErr)
	}
}

type ctxIdentities struct{ ctxErr error }

func (c *ctxIdentities) DeleteUser(ctx context.Context, _ string) error {
	c.ctxErr = ctx.Err()
	return nil
}

// abandonThen is a Participations whose AbandonAll runs fn.
type abandonThen func()

func (f abandonThen) AbandonAll(context.Context, string) error {
	f()
	return nil
}
