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

func (unimplemented) GetHealth(context.Context, api.GetHealthRequestObject) (api.GetHealthResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) GetReady(context.Context, api.GetReadyRequestObject) (api.GetReadyResponseObject, error) {
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

func (unimplemented) ExcludeParticipant(context.Context, api.ExcludeParticipantRequestObject) (api.ExcludeParticipantResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) AddCreator(context.Context, api.AddCreatorRequestObject) (api.AddCreatorResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) DeleteChallenge(context.Context, api.DeleteChallengeRequestObject) (api.DeleteChallengeResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) ListParticipants(context.Context, api.ListParticipantsRequestObject) (api.ListParticipantsResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) DeleteMe(context.Context, api.DeleteMeRequestObject) (api.DeleteMeResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) CreateActivity(context.Context, api.CreateActivityRequestObject) (api.CreateActivityResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) UpdateActivity(context.Context, api.UpdateActivityRequestObject) (api.UpdateActivityResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) DeleteActivity(context.Context, api.DeleteActivityRequestObject) (api.DeleteActivityResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) ListMyActivities(context.Context, api.ListMyActivitiesRequestObject) (api.ListMyActivitiesResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) GetMyActivitySummary(context.Context, api.GetMyActivitySummaryRequestObject) (api.GetMyActivitySummaryResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) ListMyActivityWeeks(context.Context, api.ListMyActivityWeeksRequestObject) (api.ListMyActivityWeeksResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) ListParticipantActivities(context.Context, api.ListParticipantActivitiesRequestObject) (api.ListParticipantActivitiesResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) ListChallengeActivities(context.Context, api.ListChallengeActivitiesRequestObject) (api.ListChallengeActivitiesResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) GetLeaderboard(context.Context, api.GetLeaderboardRequestObject) (api.GetLeaderboardResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) CreateInvitation(context.Context, api.CreateInvitationRequestObject) (api.CreateInvitationResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) OpenInvitation(context.Context, api.OpenInvitationRequestObject) (api.OpenInvitationResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) AcceptInvitation(context.Context, api.AcceptInvitationRequestObject) (api.AcceptInvitationResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) GetAdminStats(context.Context, api.GetAdminStatsRequestObject) (api.GetAdminStatsResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) ListAdminChallenges(context.Context, api.ListAdminChallengesRequestObject) (api.ListAdminChallengesResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) GetAdminChallenge(context.Context, api.GetAdminChallengeRequestObject) (api.GetAdminChallengeResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) UpdateAdminChallenge(context.Context, api.UpdateAdminChallengeRequestObject) (api.UpdateAdminChallengeResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) ListAdminUsers(context.Context, api.ListAdminUsersRequestObject) (api.ListAdminUsersResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) GetAdminUser(context.Context, api.GetAdminUserRequestObject) (api.GetAdminUserResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) ListAdminUserChallenges(context.Context, api.ListAdminUserChallengesRequestObject) (api.ListAdminUserChallengesResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) ListAdminUserActivities(context.Context, api.ListAdminUserActivitiesRequestObject) (api.ListAdminUserActivitiesResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) SearchAdminUsers(context.Context, api.SearchAdminUsersRequestObject) (api.SearchAdminUsersResponseObject, error) {
	return nil, errNotImplemented
}

func (unimplemented) ListAdminActions(context.Context, api.ListAdminActionsRequestObject) (api.ListAdminActionsResponseObject, error) {
	return nil, errNotImplemented
}
