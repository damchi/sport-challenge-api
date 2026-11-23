package admin

import (
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
)

// RequireAdmin lets an actor with the admin permission of their access token
// through. Anyone else gets 404 not_found: an admin route answers as if it
// did not exist (README §5.7).
func RequireAdmin(actor auth.Actor) error {
	if !actor.Admin {
		return apperr.New(apperr.CodeNotFound, nil)
	}
	return nil
}
