package apitest

import (
	"context"
	"errors"

	"github.com/damchi/sport-challenge-api/internal/api"
)

// errNotImplemented answers an operation a test did not plan to call.
var errNotImplemented = errors.New("operation not implemented in this test")

// Others completes the handler of one module into a whole
// api.StrictServerInterface, for that module's route tests. Embed it next to
// the handler:
//
//	type server struct {
//		*Handler
//		apitest.Others
//	}
//
// The handler's methods win: they are one level shallower than those of
// Others, which answer every other operation with an error (a 500). A new
// operation of the contract is added here once.
type Others struct{ unimplemented }

var _ api.StrictServerInterface = Others{}

type unimplemented struct{}

func (unimplemented) GetHealthz(context.Context, api.GetHealthzRequestObject) (api.GetHealthzResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) GetReadyz(context.Context, api.GetReadyzRequestObject) (api.GetReadyzResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) GetMe(context.Context, api.GetMeRequestObject) (api.GetMeResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) PatchMe(context.Context, api.PatchMeRequestObject) (api.PatchMeResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) GetSports(context.Context, api.GetSportsRequestObject) (api.GetSportsResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) CreateChallenge(context.Context, api.CreateChallengeRequestObject) (api.CreateChallengeResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) ListChallenges(context.Context, api.ListChallengesRequestObject) (api.ListChallengesResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) GetChallenge(context.Context, api.GetChallengeRequestObject) (api.GetChallengeResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) UpdateChallenge(context.Context, api.UpdateChallengeRequestObject) (api.UpdateChallengeResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) JoinChallenge(context.Context, api.JoinChallengeRequestObject) (api.JoinChallengeResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) AbandonChallenge(context.Context, api.AbandonChallengeRequestObject) (api.AbandonChallengeResponseObject, error) {
	return nil, errNotImplemented
}
