package user

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"slices"
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
	names   map[string]PublicName
	// email records the last UpdateEmail call, when set; emailErr fails it.
	email    *string
	emailErr error
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

func (f fakeRepository) PendingAuth0Deletions(context.Context) ([]string, error) { return nil, f.err }

func (f fakeRepository) RemovePendingAuth0Deletion(context.Context, string) error { return f.err }

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

func (f fakeRepository) PublicNames(context.Context, []string) (map[string]PublicName, error) {
	return f.names, f.err
}

func (f fakeRepository) UpdateEmail(_ context.Context, _, email string) error {
	if f.emailErr != nil {
		return f.emailErr
	}
	if f.email != nil {
		*f.email = email
	}
	return nil
}

func TestServicePublicNames(t *testing.T) {
	storageErr := errors.New("connection reset")
	stored := map[string]PublicName{"u1": {Pseudo: ptr("marie_92"), FirstName: ptr("Marie"), LastName: ptr("Tremblay")}}
	tests := []struct {
		name    string
		repo    fakeRepository
		ids     []string
		want    map[string]PublicName
		wantErr error
	}{
		// No user to look up: the database is not queried.
		{name: "nobody", repo: fakeRepository{err: storageErr}, want: map[string]PublicName{}},
		{name: "some users", repo: fakeRepository{names: stored}, ids: []string{"u1", "u2"}, want: stored},
		{name: "storage failure", repo: fakeRepository{err: storageErr}, ids: []string{"u1"}, wantErr: storageErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewService(tt.repo, nil, nil, discard).PublicNames(t.Context(), tt.ids)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("PublicNames() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestServiceGetOrCreateBySubjectKeepsTheEmail: the e-mail of the token is
// stored when it differs from the one kept; a token without one, or with a
// value that is no address, leaves it as is.
func TestServiceGetOrCreateBySubjectKeepsTheEmail(t *testing.T) {
	storageErr := errors.New("connection reset")
	tests := []struct {
		name      string
		stored    *string
		token     string
		emailErr  error
		wantWrite string
		wantEmail *string
	}{
		{name: "first token with an e-mail", token: "marie@example.com", wantWrite: "marie@example.com", wantEmail: ptr("marie@example.com")},
		{name: "e-mail changed at the provider", stored: ptr("old@example.com"), token: "new@example.com", wantWrite: "new@example.com", wantEmail: ptr("new@example.com")},
		{name: "same e-mail: no write", stored: ptr("marie@example.com"), token: "marie@example.com", wantEmail: ptr("marie@example.com")},
		{name: "spaces around: trimmed, same e-mail", stored: ptr("marie@example.com"), token: " marie@example.com ", wantEmail: ptr("marie@example.com")},
		{name: "token without e-mail keeps the stored one", stored: ptr("marie@example.com"), wantEmail: ptr("marie@example.com")},
		{name: "not an address: ignored", token: "marie", wantEmail: nil},
		{name: "too long: ignored", token: strings.Repeat("a", 250) + "@x.ca", wantEmail: nil},
		// Failing to store it does not refuse the request: the stored one stays.
		{name: "write failure", stored: ptr("old@example.com"), token: "marie@example.com", emailErr: storageErr, wantEmail: ptr("old@example.com")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var written string
			repo := fakeRepository{user: User{ID: "u-1", Email: tt.stored}, email: &written, emailErr: tt.emailErr}
			got, err := NewService(repo, nil, nil, discard).GetOrCreateBySubject(t.Context(), "email|1", tt.token)
			if err != nil {
				t.Fatal(err)
			}
			if written != tt.wantWrite {
				t.Fatalf("written %q, want %q", written, tt.wantWrite)
			}
			if !reflect.DeepEqual(got.Email, tt.wantEmail) {
				t.Fatalf("email %v, want %v", got.Email, tt.wantEmail)
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
			got, err := NewService(tt.repo, nil, nil, discard).GetOrCreateBySubject(t.Context(), "email|1", "")

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
		name     string
		repo     fakeRepository
		wantCode apperr.Code
		wantErr  error
	}{
		{name: "found", repo: fakeRepository{user: User{ID: "u-1", Pseudo: ptr("marie")}}},
		// The actor comes from the token, so their row exists: missing, it was
		// anonymized after the middleware read it (deletion racing the request).
		{name: "account deleted meanwhile", repo: fakeRepository{err: fmt.Errorf("read user: %w", errUserNotFound)}, wantCode: apperr.CodeAccountDeleted},
		{name: "storage error", repo: fakeRepository{err: storageErr}, wantErr: storageErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewService(tt.repo, nil, nil, discard).Profile(t.Context(), "u-1")
			var appErr *apperr.Error
			switch {
			case tt.wantCode != "":
				if !errors.As(err, &appErr) || appErr.Code != tt.wantCode || appErr.Status != http.StatusUnauthorized {
					t.Fatalf("error = %v, want 401 %s", err, tt.wantCode)
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
		{name: "pseudo removed", changes: ProfileChanges{RemovePseudo: true}, wantWritten: true},
		{name: "pseudo removed, invalid language", changes: ProfileChanges{RemovePseudo: true, Language: ptr("de")}, wantStatus: http.StatusBadRequest, wantFields: "language"},
		{name: "invalid pseudo and language", changes: ProfileChanges{Pseudo: ptr("é"), Language: ptr("de")}, wantStatus: http.StatusBadRequest, wantFields: "language,pseudo"},
		{name: "invalid language only", changes: ProfileChanges{Pseudo: ptr("marie"), Language: ptr("FR")}, wantStatus: http.StatusBadRequest, wantFields: "language"},
		{name: "pseudo taken", changes: ProfileChanges{Pseudo: ptr("marie")}, repoErr: fmt.Errorf("update user: %w", errPseudoTaken), wantStatus: http.StatusConflict, wantFields: "pseudo", wantWritten: true},
		{name: "account deleted meanwhile", changes: ProfileChanges{Language: ptr("en")}, repoErr: fmt.Errorf("update user: %w", errUserNotFound), wantStatus: http.StatusUnauthorized, wantWritten: true},
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
			if tt.wantWritten && written.RemovePseudo != tt.changes.RemovePseudo {
				t.Fatalf("pseudo removed = %v, want %v", written.RemovePseudo, tt.changes.RemovePseudo)
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
			// Several codes answer 401: a deleted account must be told apart.
			if tt.wantStatus == http.StatusUnauthorized && appErr.Code != apperr.CodeAccountDeleted {
				t.Fatalf("code = %s, want %s", appErr.Code, apperr.CodeAccountDeleted)
			}
		})
	}
}

func TestServiceProfileComplete(t *testing.T) {
	storageErr := errors.New("connection reset")
	tests := []struct {
		name     string
		repo     fakeRepository
		want     bool
		wantErr  bool
		wantCode apperr.Code
	}{
		{name: "pseudo and names set", repo: fakeRepository{user: User{ID: "u-1", Pseudo: ptr("marie"), FirstName: ptr("Marie"), LastName: ptr("Roy")}}, want: true},
		{name: "names without a pseudo", repo: fakeRepository{user: User{ID: "u-1", FirstName: ptr("Marie"), LastName: ptr("Roy")}}, want: true},
		// A profile from before the names: incomplete until they are set.
		{name: "pseudo without names", repo: fakeRepository{user: User{ID: "u-1", Pseudo: ptr("marie")}}},
		{name: "no last name", repo: fakeRepository{user: User{ID: "u-1", Pseudo: ptr("marie"), FirstName: ptr("Marie")}}},
		// Anonymization clears the names: a deleted account is deleted, not
		// incomplete (a request racing the deletion). The repository never
		// returns an anonymized row: it answers errUserNotFound.
		{name: "account deleted meanwhile", repo: fakeRepository{err: fmt.Errorf("read user: %w", errUserNotFound)}, wantErr: true, wantCode: apperr.CodeAccountDeleted},
		{name: "storage error", repo: fakeRepository{err: storageErr}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewService(tt.repo, nil, nil, discard).ProfileComplete(t.Context(), "u-1")
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("ProfileComplete() = %v, %v, want %v (error %v)", got, err, tt.want, tt.wantErr)
			}
			if tt.wantCode != "" && !errors.Is(err, apperr.New(tt.wantCode, nil)) {
				t.Fatalf("ProfileComplete() error = %v, want %s", err, tt.wantCode)
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

// pendingRepository keeps the pending deletions in memory, as the database
// does: removing a subject twice is not an error.
type pendingRepository struct {
	fakeRepository
	subjects  []string
	listErr   error
	removeErr error
}

func (p *pendingRepository) PendingAuth0Deletions(context.Context) ([]string, error) {
	return append([]string(nil), p.subjects...), p.listErr
}

func (p *pendingRepository) RemovePendingAuth0Deletion(_ context.Context, subject string) error {
	if p.removeErr != nil {
		return p.removeErr
	}
	p.subjects = slices.DeleteFunc(p.subjects, func(s string) bool { return s == subject })
	return nil
}

// flakyIdentities fails for the subjects in failing, and records the calls.
type flakyIdentities struct {
	failing map[string]bool
	calls   []string
}

func (f *flakyIdentities) DeleteUser(_ context.Context, subject string) error {
	f.calls = append(f.calls, subject)
	if f.failing[subject] {
		return errors.New("auth0 delete user: status 503")
	}
	return nil
}

func TestServiceRetryAuth0Deletions(t *testing.T) {
	storageErr := errors.New("connection reset")
	t.Run("success removes every pending deletion", func(t *testing.T) {
		repo := &pendingRepository{subjects: []string{"email|1", "email|2"}}
		identities := &flakyIdentities{}
		got, err := NewService(repo, nil, identities, discard).RetryAuth0Deletions(t.Context())
		if err != nil || got != (RetryResult{Deleted: 2}) {
			t.Fatalf("result = %+v, err = %v, want 2 deleted", got, err)
		}
		if len(repo.subjects) != 0 || !slices.Equal(identities.calls, []string{"email|1", "email|2"}) {
			t.Fatalf("pending %v, calls %v, want none pending and both replayed", repo.subjects, identities.calls)
		}
	})

	t.Run("a failure stays pending, then the next run succeeds", func(t *testing.T) {
		repo := &pendingRepository{subjects: []string{"email|1", "email|2"}}
		identities := &flakyIdentities{failing: map[string]bool{"email|1": true}}
		svc := NewService(repo, nil, identities, discard)
		got, err := svc.RetryAuth0Deletions(t.Context())
		if err == nil || got != (RetryResult{Deleted: 1, Failed: 1}) {
			t.Fatalf("result = %+v, err = %v, want 1 deleted, 1 failed and an error", got, err)
		}
		if !slices.Equal(repo.subjects, []string{"email|1"}) {
			t.Fatalf("pending %v, want only the failed one", repo.subjects)
		}
		// The service adds no subject to the error; that the Auth0 client
		// keeps it out of its own errors is tested in platform/auth.
		if strings.Contains(err.Error(), "email|") {
			t.Fatalf("error %q carries a subject", err)
		}
		identities.failing = nil
		got, err = svc.RetryAuth0Deletions(t.Context())
		if err != nil || got != (RetryResult{Deleted: 1}) || len(repo.subjects) != 0 {
			t.Fatalf("second run = %+v, err = %v, pending %v, want the last one done", got, err, repo.subjects)
		}
	})

	t.Run("running again with nothing pending does nothing", func(t *testing.T) {
		repo := &pendingRepository{}
		identities := &flakyIdentities{}
		got, err := NewService(repo, nil, identities, discard).RetryAuth0Deletions(t.Context())
		if err != nil || got != (RetryResult{}) || len(identities.calls) != 0 {
			t.Fatalf("result = %+v, err = %v, calls %v, want nothing", got, err, identities.calls)
		}
	})

	t.Run("a record that cannot be removed is an error", func(t *testing.T) {
		repo := &pendingRepository{subjects: []string{"email|1"}, removeErr: storageErr}
		got, err := NewService(repo, nil, &flakyIdentities{}, discard).RetryAuth0Deletions(t.Context())
		if !errors.Is(err, storageErr) || got != (RetryResult{Failed: 1}) {
			t.Fatalf("result = %+v, err = %v, want 1 failed with the storage error", got, err)
		}
	})

	t.Run("storage failure when listing", func(t *testing.T) {
		repo := &pendingRepository{listErr: storageErr}
		identities := &flakyIdentities{}
		_, err := NewService(repo, nil, identities, discard).RetryAuth0Deletions(t.Context())
		if !errors.Is(err, storageErr) || len(identities.calls) != 0 {
			t.Fatalf("err = %v, calls %v, want the storage error and no call", err, identities.calls)
		}
	})
}
