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

// selfDeletionRoute is the deletion of one's own account, the only route a
// blocked account may still call (README §2.12): its holder can always have
// their data erased. It is the route of the contract as registered: the walk
// of the registered routes in cmd/api
// (TestOnlyTheDeletionOfItsAccountIsOpenToABlockedAccount) fails if they part.
const selfDeletionRoute = "DELETE /api/v1/me"

// blockedRefusal refuses (403 account_blocked) every request of an account
// an admin blocked, except on route selfDeletionRoute (the route of the
// request, "METHOD /path" as registered). An account not blocked is
// refused nothing here.
func blockedRefusal(u User, route string) error {
	if u.BlockedAt != nil && route != selfDeletionRoute {
		return apperr.New(apperr.CodeAccountBlocked, nil)
	}
	return nil
}
