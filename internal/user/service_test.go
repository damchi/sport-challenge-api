package user

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
)

type fakeRepository struct {
	user User
	err  error
}

func (f fakeRepository) GetOrCreateBySubject(context.Context, string) (User, error) {
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
