package user

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
)

type fakeRepository struct {
	user User
	err  error
	// changes records the last UpdateProfile call, when set.
	changes *ProfileChanges
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
		{name: "storage error", repo: fakeRepository{err: storageErr}, wantErr: storageErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewService(tt.repo).GetOrCreateBySubject(t.Context(), "email|1")

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
			got, err := NewService(tt.repo).Profile(t.Context(), "u-1")
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

			got, err := NewService(repo).UpdateProfile(t.Context(), "u-1", tt.changes)

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
