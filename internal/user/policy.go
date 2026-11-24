package user

import (
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
)

// adminRefusal lets an actor with the admin permission of their access
// token change any account (README §2.12); anyone else gets 404 not_found,
// as on every admin route.
func adminRefusal(actor auth.Actor) error {
	if !actor.Admin {
		return apperr.New(apperr.CodeNotFound, nil)
	}
	return nil
}

// blockRefusal refuses (403 forbidden) an admin blocking their own account
// (README §2.12): they would lock themselves out. Any other account may be
// blocked.
func blockRefusal(actor auth.Actor, targetID string) error {
	if actor.ID == targetID {
		return apperr.New(apperr.CodeForbidden, nil)
	}
	return nil
}
