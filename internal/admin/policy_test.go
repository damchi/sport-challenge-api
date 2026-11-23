package admin

import (
	"errors"
	"net/http"
	"testing"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
)

func TestRequireAdmin(t *testing.T) {
	tests := []struct {
		name    string
		actor   auth.Actor
		wantErr bool
	}{
		{name: "admin", actor: auth.Actor{ID: "u1", Admin: true}},
		{name: "not an admin: hidden", actor: auth.Actor{ID: "u1"}, wantErr: true},
		{name: "no actor: hidden", actor: auth.Actor{}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RequireAdmin(tt.actor)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("RequireAdmin() = %v, want nil", err)
				}
				return
			}
			var appErr *apperr.Error
			if !errors.As(err, &appErr) || appErr.Code != apperr.CodeNotFound || appErr.Status != http.StatusNotFound {
				t.Fatalf("RequireAdmin() = %v, want 404 not_found", err)
			}
		})
	}
}
