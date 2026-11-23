package activity

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/damchi/sport-challenge-api/internal/api"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/httpx"
)

// defaultLimit is the page size when the client gives none (the contract's
// default).
const defaultLimit = 20

// Handler implements the activity operations of the API contract.
type Handler struct {
	service *Service
}

// NewHandler returns a Handler on service.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// CreateActivity records an activity of the authenticated user: 201 when
// created, 200 when its idempotency key gave one recorded before.
func (h *Handler) CreateActivity(ctx context.Context, req api.CreateActivityRequestObject) (api.CreateActivityResponseObject, error) {
	actorID, ok := auth.UserID(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	if req.Body == nil {
		return nil, apperr.InvalidFields(nil)
	}
	rec, err := h.service.Record(ctx, actorID, req.Params.IdempotencyKey, toDraft(*req.Body))
	if err != nil {
		return nil, err
	}
	out, err := toAPI(rec)
	if err != nil {
		return nil, err
	}
	if !rec.Created {
		return api.CreateActivity200JSONResponse(out), nil
	}
	return api.CreateActivity201JSONResponse(out), nil
}

// UpdateActivity replaces an activity of the authenticated user.
func (h *Handler) UpdateActivity(ctx context.Context, req api.UpdateActivityRequestObject) (api.UpdateActivityResponseObject, error) {
	actorID, ok := auth.UserID(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	if req.Body == nil {
		return nil, apperr.InvalidFields(nil)
	}
	rec, err := h.service.Update(ctx, actorID, req.ActivityId.String(), toDraft(*req.Body))
	if err != nil {
		return nil, err
	}
	out, err := toAPI(rec)
	if err != nil {
		return nil, err
	}
	return api.UpdateActivity200JSONResponse(out), nil
}

// DeleteActivity deletes an activity of the authenticated user.
func (h *Handler) DeleteActivity(ctx context.Context, req api.DeleteActivityRequestObject) (api.DeleteActivityResponseObject, error) {
	actorID, ok := auth.UserID(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	if err := h.service.Delete(ctx, actorID, req.ActivityId.String()); err != nil {
		return nil, err
	}
	return api.DeleteActivity204Response{}, nil
}

// ListMyActivities answers a page of the activities of the authenticated
// user.
func (h *Handler) ListMyActivities(ctx context.Context, req api.ListMyActivitiesRequestObject) (api.ListMyActivitiesResponseObject, error) {
	actorID, ok := auth.UserID(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	p := req.Params
	after, err := decodeAfter(p.Cursor)
	if err != nil {
		return nil, err
	}
	page, err := h.service.Mine(ctx, actorID, Filter{From: p.From, To: p.To, Sport: p.Sport}, limitOf(p.Limit), after)
	if err != nil {
		return nil, err
	}
	out, err := toPage(page)
	if err != nil {
		return nil, err
	}
	return api.ListMyActivities200JSONResponse(out), nil
}

// GetMyActivitySummary answers the totals of the current week and the
// number of activities of each sport of the authenticated user.
func (h *Handler) GetMyActivitySummary(ctx context.Context, req api.GetMyActivitySummaryRequestObject) (api.GetMyActivitySummaryResponseObject, error) {
	actorID, ok := auth.UserID(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	sum, err := h.service.MySummary(ctx, actorID, req.Params.Tz)
	if err != nil {
		return nil, err
	}
	w := sum.CurrentWeek
	out := api.ActivitySummary{
		CurrentWeek: api.CurrentActivityWeek{
			StartsAt:        w.StartsAt.UTC(),
			EndsAt:          w.EndsAt.UTC(),
			Sessions:        w.Sessions,
			DurationSeconds: w.DurationSeconds,
			DistanceMeters:  w.DistanceMeters,
			Days:            make([]api.ActivityDay, 0, len(w.Days)),
		},
		Sports: make([]api.SportCount, 0, len(sum.Sports)),
		Total:  sum.Total,
	}
	for _, d := range w.Days {
		out.CurrentWeek.Days = append(out.CurrentWeek.Days, api.ActivityDay{
			Date:            openapi_types.Date{Time: d.Date},
			Sessions:        d.Sessions,
			DurationSeconds: d.DurationSeconds,
		})
	}
	for _, c := range sum.Sports {
		out.Sports = append(out.Sports, api.SportCount{SportCode: c.SportCode, Count: c.Count})
	}
	return api.GetMyActivitySummary200JSONResponse(out), nil
}

// ListMyActivityWeeks answers a page of the weeks of the activities of the
// authenticated user, with their totals.
func (h *Handler) ListMyActivityWeeks(ctx context.Context, req api.ListMyActivityWeeksRequestObject) (api.ListMyActivityWeeksResponseObject, error) {
	actorID, ok := auth.UserID(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	p := req.Params
	var after *WeekPosition
	if p.Cursor != nil {
		after = &WeekPosition{}
		if err := httpx.DecodeCursor(*p.Cursor, after); err != nil {
			return nil, err
		}
		if after.Monday.IsZero() || after.Monday.Weekday() != time.Monday {
			return nil, apperr.InvalidFields(nil, "cursor")
		}
	}
	page, err := h.service.MyWeeks(ctx, actorID, p.Tz, p.Sport, limitOf(p.Limit), after)
	if err != nil {
		return nil, err
	}
	out := api.ActivityWeekPage{Items: make([]api.ActivityWeek, 0, len(page.Items))}
	for _, w := range page.Items {
		out.Items = append(out.Items, api.ActivityWeek{
			StartsAt:        w.StartsAt.UTC(),
			EndsAt:          w.EndsAt.UTC(),
			Sessions:        w.Sessions,
			DurationSeconds: w.DurationSeconds,
			DistanceMeters:  w.DistanceMeters,
		})
	}
	if page.Next != nil {
		cursor, err := httpx.EncodeCursor(page.Next)
		if err != nil {
			return nil, err
		}
		out.NextCursor = &cursor
	}
	return api.ListMyActivityWeeks200JSONResponse(out), nil
}

// ListParticipantActivities answers a page of the activities of a
// participant in a challenge.
func (h *Handler) ListParticipantActivities(ctx context.Context, req api.ListParticipantActivitiesRequestObject) (api.ListParticipantActivitiesResponseObject, error) {
	actorID, ok := auth.UserID(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	p := req.Params
	after, err := decodeAfter(p.Cursor)
	if err != nil {
		return nil, err
	}
	page, err := h.service.OfParticipant(ctx, actorID, req.ChallengeId.String(), req.UserId.String(), limitOf(p.Limit), after)
	if err != nil {
		return nil, err
	}
	out, err := toPage(page)
	if err != nil {
		return nil, err
	}
	return api.ListParticipantActivities200JSONResponse(out), nil
}

// ListChallengeActivities answers a page of the activities of all the
// participants of a challenge, each with its author.
func (h *Handler) ListChallengeActivities(ctx context.Context, req api.ListChallengeActivitiesRequestObject) (api.ListChallengeActivitiesResponseObject, error) {
	actorID, ok := auth.UserID(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	p := req.Params
	after, err := decodeAfter(p.Cursor)
	if err != nil {
		return nil, err
	}
	page, err := h.service.OfChallenge(ctx, actorID, req.ChallengeId.String(), limitOf(p.Limit), after)
	if err != nil {
		return nil, err
	}
	out := api.ChallengeActivityPage{Items: make([]api.ChallengeActivity, 0, len(page.Items))}
	for _, it := range page.Items {
		a, err := toChallengeActivity(it)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, a)
	}
	if page.Next != nil {
		cursor, err := httpx.EncodeCursor(page.Next)
		if err != nil {
			return nil, err
		}
		out.NextCursor = &cursor
	}
	return api.ListChallengeActivities200JSONResponse(out), nil
}

func limitOf(limit *int) int {
	if limit == nil {
		return defaultLimit
	}
	return *limit
}

// decodeAfter reads the position of a cursor, nil without one. It is
// checked whole and normalized: a position without its date (forged
// cursor) would restart the list.
func decodeAfter(cursor *string) (*Position, error) {
	if cursor == nil {
		return nil, nil
	}
	after := &Position{}
	if err := httpx.DecodeCursor(*cursor, after); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(after.ID)
	if err != nil {
		return nil, apperr.InvalidFields(err, "cursor")
	}
	after.ID = id.String()
	if after.StartedAt.IsZero() {
		return nil, apperr.InvalidFields(nil, "cursor")
	}
	return after, nil
}

func toPage(page Page) (api.ActivityPage, error) {
	out := api.ActivityPage{Items: make([]api.Activity, 0, len(page.Items))}
	for _, rec := range page.Items {
		a, err := toAPI(rec)
		if err != nil {
			return api.ActivityPage{}, err
		}
		out.Items = append(out.Items, a)
	}
	if page.Next != nil {
		cursor, err := httpx.EncodeCursor(page.Next)
		if err != nil {
			return api.ActivityPage{}, err
		}
		out.NextCursor = &cursor
	}
	return out, nil
}

func toDraft(b api.ActivityCreate) Draft {
	return Draft{
		SportCode:       b.SportCode,
		SportLabel:      b.SportLabel,
		StartedAt:       b.StartedAt,
		DurationSeconds: b.DurationSeconds,
		DistanceMeters:  b.DistanceMeters,
	}
}

func toAPI(rec Recorded) (api.Activity, error) {
	id, err := uuid.Parse(rec.ID)
	if err != nil {
		return api.Activity{}, fmt.Errorf("activity id: %w", err)
	}
	out := api.Activity{
		Id:              id,
		SportCode:       rec.SportCode,
		SportLabel:      rec.SportLabel,
		StartedAt:       rec.StartedAt.UTC(),
		DurationSeconds: rec.DurationSeconds,
		DistanceMeters:  rec.DistanceMeters,
		Source:          api.ActivitySource(rec.Source),
		Contributions:   make([]api.ActivityContribution, 0, len(rec.Contributions)),
		Locked:          rec.Locked,
	}
	if rec.DistanceMeters != nil {
		v := Speed(*rec.DistanceMeters, rec.DurationSeconds)
		out.SpeedMps, out.PaceSecondsPerKm = &v.MetersPerSecond, &v.SecondsPerKilometer
	}
	for _, c := range rec.Contributions {
		cid, err := uuid.Parse(c.ChallengeID)
		if err != nil {
			return api.Activity{}, fmt.Errorf("challenge id: %w", err)
		}
		out.Contributions = append(out.Contributions, api.ActivityContribution{
			ChallengeId:     cid,
			ChallengeTitle:  c.ChallengeTitle,
			DistanceMeters:  c.DistanceMeters,
			DurationSeconds: c.DurationSeconds,
		})
	}
	return out, nil
}

// toChallengeActivity is toAPI with the author of the activity. Avatar stays
// null in V1: the avatar upload comes with SC-API-023.
func toChallengeActivity(it Authored) (api.ChallengeActivity, error) {
	a, err := toAPI(it.Recorded)
	if err != nil {
		return api.ChallengeActivity{}, err
	}
	userID, err := uuid.Parse(it.UserID)
	if err != nil {
		return api.ChallengeActivity{}, fmt.Errorf("author id: %w", err)
	}
	return api.ChallengeActivity{
		Id:               a.Id,
		SportCode:        a.SportCode,
		SportLabel:       a.SportLabel,
		StartedAt:        a.StartedAt,
		DurationSeconds:  a.DurationSeconds,
		DistanceMeters:   a.DistanceMeters,
		Source:           api.ChallengeActivitySource(a.Source),
		SpeedMps:         a.SpeedMps,
		PaceSecondsPerKm: a.PaceSecondsPerKm,
		Contributions:    a.Contributions,
		Locked:           a.Locked,
		User:             api.LeaderboardUser{Id: userID, Pseudo: it.Author.Pseudo, FirstName: it.Author.FirstName, LastName: it.Author.LastName},
	}, nil
}
