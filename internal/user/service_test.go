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
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
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
	// deleted and deletedAt record the user Delete anonymized and when, when
	// set; deleteErr fails Delete, removed records RemovePendingAuth0Deletion and removeErr fails it.
	deleted   *string
	deletedAt *time.Time
	deleteErr error
	removed   *[]string
	removeErr error
	// updated, when set, is what UpdateProfile returns instead of user;
	// lockErr fails Lock, which returns user; blocks records the
	// SetBlockedAt calls, when set.
	updated *User
	lockErr error
	blocks  *[]*time.Time
}

func (f fakeRepository) Lock(context.Context, string) (User, error) {
	if f.lockErr != nil {
		return User{}, f.lockErr
	}
	return f.user, nil
}

func (f fakeRepository) SetBlockedAt(_ context.Context, _ string, at *time.Time) error {
	if f.blocks != nil {
		*f.blocks = append(*f.blocks, at)
	}
	return nil
}

func (f fakeRepository) Delete(ctx context.Context, id string, at time.Time, erase func(context.Context) error) (string, error) {
	if f.deleteErr != nil {
		return "", f.deleteErr
	}
	if err := erase(ctx); err != nil {
		return "", err
	}
	if f.deleted != nil {
		*f.deleted = id
	}
	if f.deletedAt != nil {
		*f.deletedAt = at
	}
	return f.user.AuthSubject, nil
}

func (f fakeRepository) PendingAuth0Deletions(context.Context) ([]string, error) { return nil, f.err }

func (f fakeRepository) RemovePendingAuth0Deletion(_ context.Context, subject string) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	if f.removed != nil {
		*f.removed = append(*f.removed, subject)
	}
	return nil
}

type fakeDeletionLock struct {
	locked string
	err    error
}

func (f *fakeDeletionLock) LockForDeletion(_ context.Context, userID string) error {
	f.locked = userID
	return f.err
}

// fakeEraser records, in calls, its name once LockForDeletion ran (or "early").
type fakeEraser struct {
	name         string
	deletionLock *fakeDeletionLock
	calls        *[]string
	err          error
}

func (f fakeEraser) EraseUser(_ context.Context, userID string) error {
	call := f.name
	if f.deletionLock.locked != userID {
		call = "early"
	}
	*f.calls = append(*f.calls, call)
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
	if f.updated != nil {
		return *f.updated, f.err
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
			got, err := NewService(tt.repo, nil, nil, nil, nil, discard).PublicNames(t.Context(), tt.ids)
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
			got, err := NewService(repo, nil, nil, nil, nil, discard).GetOrCreateBySubject(t.Context(), "email|1", tt.token)
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
			got, err := NewService(tt.repo, nil, nil, nil, nil, discard).GetOrCreateBySubject(t.Context(), "email|1", "")

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
			got, err := NewService(tt.repo, nil, nil, nil, nil, discard).Profile(t.Context(), "u-1")
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

			got, err := NewService(repo, nil, nil, nil, nil, discard).UpdateProfile(t.Context(), "u-1", tt.changes)

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
			got, err := NewService(tt.repo, nil, nil, nil, nil, discard).ProfileComplete(t.Context(), "u-1")
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
	now := time.Date(2026, 3, 9, 14, 30, 0, 0, time.UTC)
	storageErr := errors.New("connection reset")
	auth0Err := errors.New("auth0: status 503")
	tests := []struct {
		name           string
		deleteErr      error
		lockErr        error
		eraseErr       error
		auth0Err       error
		removeErr      error
		wantCode       apperr.Code
		wantErr        error
		wantAuth0      bool
		wantRemoved    bool
		wantLog        string
		wantAnonymized bool
	}{
		{name: "deleted, then at Auth0: no longer pending", wantAuth0: true, wantRemoved: true, wantAnonymized: true},
		// The deletion recorded in the transaction stays for the worker.
		{name: "Auth0 fails: kept for the worker", auth0Err: auth0Err, wantAuth0: true, wantLog: "kept for retry", wantAnonymized: true},
		// Done at Auth0 but still pending: the worker replays it, harmlessly.
		{name: "pending record not removed", removeErr: storageErr, wantAuth0: true, wantLog: "not removed", wantAnonymized: true},
		// The lock fails (last creator): everything rolls back, nothing is deleted at Auth0.
		{name: "lock failure", lockErr: storageErr, wantErr: storageErr},
		// An erasure fails: the same, the erasers after it do not run.
		{name: "erasure failure", eraseErr: storageErr, wantErr: storageErr},
		{name: "deleted meanwhile", deleteErr: fmt.Errorf("delete user: %w", errUserNotFound), wantCode: apperr.CodeAccountDeleted},
		{name: "storage failure", deleteErr: storageErr, wantErr: storageErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var removed []string
			repo := fakeRepository{
				user: User{ID: "u-1", AuthSubject: "email|1"}, deleted: new(string), deletedAt: new(time.Time),
				deleteErr: tt.deleteErr, removed: &removed, removeErr: tt.removeErr,
			}
			deletionLock := &fakeDeletionLock{err: tt.lockErr}
			var erased []string
			erasers := []Eraser{
				fakeEraser{name: "first", deletionLock: deletionLock, calls: &erased, err: tt.eraseErr},
				fakeEraser{name: "second", deletionLock: deletionLock, calls: &erased},
			}
			identities := &fakeIdentities{err: tt.auth0Err}
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			err := NewService(repo, deletionLock, erasers, identities, clock.NewFake(now), logger).Delete(t.Context(), "u-1")
			switch {
			case tt.wantCode != "":
				if !errors.Is(err, apperr.New(tt.wantCode, nil)) {
					t.Fatalf("error = %v, want %s", err, tt.wantCode)
				}
			case !errors.Is(err, tt.wantErr):
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantAnonymized && (*repo.deleted != "u-1" || deletionLock.locked != "u-1") {
				t.Fatalf("anonymized %q, locked %q, want both for u-1", *repo.deleted, deletionLock.locked)
			}
			// The deletion is dated by the clock, not by the database.
			if tt.wantAnonymized && !repo.deletedAt.Equal(now) {
				t.Fatalf("deleted at %v, want the clock's %v", *repo.deletedAt, now)
			}
			// The erasers run after the lock, in order, and stop at a failure.
			var wantErased []string
			switch {
			case tt.deleteErr != nil || tt.lockErr != nil:
			case tt.eraseErr != nil:
				wantErased = []string{"first"}
			default:
				wantErased = []string{"first", "second"}
			}
			if !slices.Equal(erased, wantErased) {
				t.Fatalf("erasers called %v, want %v", erased, wantErased)
			}
			if got := identities.deleted == "email|1"; got != tt.wantAuth0 {
				t.Fatalf("deleted at Auth0: %v, want %v", got, tt.wantAuth0)
			}
			if got := slices.Equal(removed, []string{"email|1"}); got != tt.wantRemoved {
				t.Fatalf("removed %v, want the pending deletion removed: %v", removed, tt.wantRemoved)
			}
			// A failure after the commit leaves a trace, never the subject.
			if tt.wantLog == "" && logs.Len() > 0 || !strings.Contains(logs.String(), tt.wantLog) {
				t.Fatalf("logs %q, want %q", logs.String(), tt.wantLog)
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
	err := NewService(repo, lockThen(cancel), nil, identities, clock.Real{}, discard).Delete(ctx, "u-1")
	if err != nil || identities.ctxErr != nil {
		t.Fatalf("error %v, Auth0 called with a context error %v, want neither", err, identities.ctxErr)
	}
}

func TestServiceDeleteBoundsThePendingRemoval(t *testing.T) {
	tests := []struct {
		name      string
		leaves    bool
		removeErr error
		wantLog   string
	}{
		{name: "removed within the deadline"},
		// The client left once the account was deleted: the removal still runs.
		{name: "client gone before the end", leaves: true},
		// The pool stays full past the deadline: the worker replays the deletion.
		{name: "removal past the deadline", removeErr: context.DeadlineExceeded, wantLog: "not removed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			deletionLock := lockThen(func() {})
			if tt.leaves {
				deletionLock = lockThen(cancel)
			}
			repo := &deadlineRepository{
				fakeRepository: fakeRepository{user: User{ID: "u-1", AuthSubject: "email|1"}},
				err:            tt.removeErr,
			}
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			start := time.Now()
			err := NewService(repo, deletionLock, nil, &ctxIdentities{}, clock.Real{}, logger).Delete(ctx, "u-1")
			end := time.Now()
			if err != nil {
				t.Fatalf("error = %v, want nil: the account is deleted", err)
			}
			if !repo.called || repo.ctxErr != nil {
				t.Fatalf("removal called %v with a context error %v, want called, not cancelled", repo.called, repo.ctxErr)
			}
			// Set when the removal starts: the timeout after a moment of the call.
			if !repo.hasDeadline || repo.deadline.Before(start.Add(pendingRemovalTimeout)) || repo.deadline.After(end.Add(pendingRemovalTimeout)) {
				t.Fatalf("removal deadline %v (set %v), want within %v", repo.deadline.Sub(end), repo.hasDeadline, pendingRemovalTimeout)
			}
			if tt.wantLog == "" && logs.Len() > 0 {
				t.Fatalf("logs %q, want none", logs.String())
			}
			if tt.wantLog != "" && (!strings.Contains(logs.String(), tt.wantLog) || !strings.Contains(logs.String(), `"level":"WARN"`)) {
				t.Fatalf("logs %q, want a warning %q", logs.String(), tt.wantLog)
			}
			if strings.Contains(logs.String(), "email|1") {
				t.Fatalf("logs %q carry the subject", logs.String())
			}
		})
	}
}

// deadlineRepository records the context of RemovePendingAuth0Deletion and
// fails it with err.
type deadlineRepository struct {
	fakeRepository
	err         error
	called      bool
	ctxErr      error
	deadline    time.Time
	hasDeadline bool
}

func (d *deadlineRepository) RemovePendingAuth0Deletion(ctx context.Context, _ string) error {
	d.called = true
	d.ctxErr = ctx.Err()
	d.deadline, d.hasDeadline = ctx.Deadline()
	return d.err
}

type ctxIdentities struct{ ctxErr error }

func (c *ctxIdentities) DeleteUser(ctx context.Context, _ string) error {
	c.ctxErr = ctx.Err()
	return nil
}

// lockThen is a Participations whose LockForDeletion runs fn.
type lockThen func()

func (f lockThen) LockForDeletion(context.Context, string) error {
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
		got, err := NewService(repo, nil, nil, identities, nil, discard).RetryAuth0Deletions(t.Context())
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
		svc := NewService(repo, nil, nil, identities, nil, discard)
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
		got, err := NewService(repo, nil, nil, identities, nil, discard).RetryAuth0Deletions(t.Context())
		if err != nil || got != (RetryResult{}) || len(identities.calls) != 0 {
			t.Fatalf("result = %+v, err = %v, calls %v, want nothing", got, err, identities.calls)
		}
	})

	t.Run("a record that cannot be removed is an error", func(t *testing.T) {
		repo := &pendingRepository{subjects: []string{"email|1"}, removeErr: storageErr}
		got, err := NewService(repo, nil, nil, &flakyIdentities{}, nil, discard).RetryAuth0Deletions(t.Context())
		if !errors.Is(err, storageErr) || got != (RetryResult{Failed: 1}) {
			t.Fatalf("result = %+v, err = %v, want 1 failed with the storage error", got, err)
		}
	})

	t.Run("storage failure when listing", func(t *testing.T) {
		repo := &pendingRepository{listErr: storageErr}
		identities := &flakyIdentities{}
		_, err := NewService(repo, nil, nil, identities, nil, discard).RetryAuth0Deletions(t.Context())
		if !errors.Is(err, storageErr) || len(identities.calls) != 0 {
			t.Fatalf("err = %v, calls %v, want the storage error and no call", err, identities.calls)
		}
	})
}
