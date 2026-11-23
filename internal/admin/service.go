package admin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/buildinfo"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/httpx"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// repository is what the service needs from the storage.
type repository interface {
	Figures(ctx context.Context, now, since time.Time) (Figures, error)
	LastWorkerRuns(ctx context.Context) ([]TaskRun, error)
	ListChallenges(ctx context.Context, q ChallengeQuery) ([]ChallengeRow, error)
	CreatorsOf(ctx context.Context, ids []string) (map[string][]UserRef, error)
	Challenge(ctx context.Context, id string) (ChallengeData, error)
	ListUsers(ctx context.Context, q UserQuery) ([]UserRow, error)
	User(ctx context.Context, id string) (UserRow, error)
	ListUserChallenges(ctx context.Context, id string, q PageQuery) ([]UserChallengeRow, error)
	ListUserActivities(ctx context.Context, id string, q ActivityQuery) ([]ActivityRow, error)
	ListActions(ctx context.Context, q ActionQuery) ([]Action, error)
}

// Service holds the admin reads.
type Service struct {
	repo  repository
	clock clock.Clock
	build buildinfo.Info
}

// NewService returns a Service on repo; build is the build of the running
// API, given on the dashboard.
func NewService(repo repository, clk clock.Clock, build buildinfo.Info) *Service {
	return &Service{repo: repo, clock: clk, build: build}
}

// Stats returns the figures of the dashboard, read now, never stored. Admin
// only (RequireAdmin), checked before anything is read.
func (s *Service) Stats(ctx context.Context, actor auth.Actor) (Stats, error) {
	if err := RequireAdmin(actor); err != nil {
		return Stats{}, err
	}
	now := s.clock.Now()
	figures, err := s.repo.Figures(ctx, now, now.Add(-recentWindow))
	if err != nil {
		return Stats{}, fmt.Errorf("read dashboard figures: %w", err)
	}
	runs, err := s.repo.LastWorkerRuns(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("read worker runs: %w", err)
	}
	return Stats{ComputedAt: now, Figures: figures, WorkerTasks: byTask(runs), Build: s.build}, nil
}

// byTask gives every task of workerTasks, in that order, its last run and
// last failure among runs; a task of runs not in workerTasks is left out.
func byTask(runs []TaskRun) []WorkerTask {
	out := make([]WorkerTask, len(workerTasks))
	index := make(map[string]int, len(workerTasks))
	for i, task := range workerTasks {
		out[i] = WorkerTask{Task: task}
		index[task] = i
	}
	for _, r := range runs {
		i, ok := index[r.Task]
		if !ok {
			continue
		}
		run := r.Run
		if r.Failure {
			out[i].LastFailure = &run
		} else {
			out[i].LastRun = &run
		}
	}
	return out
}

// Challenges returns a page of at most q.Limit (at least 1) challenges of
// q, q.Now and q.After (from cursor, "" for the first page) set here,
// deleted challenges included, each with its status at now and its
// creators. Admin only (RequireAdmin), checked before anything is read, the
// cursor included: a bad cursor answers a non-admin 404, not 400.
func (s *Service) Challenges(ctx context.Context, actor auth.Actor, q ChallengeQuery, cursor string) (ChallengePage, error) {
	if err := RequireAdmin(actor); err != nil {
		return ChallengePage{}, err
	}
	if cursor != "" {
		after, err := decodePosition(cursor)
		if err != nil {
			return ChallengePage{}, err
		}
		q.After = &after
	}
	q.Now = s.clock.Now()
	limit := pageLimit(q.Limit)
	q.Limit = limit + 1
	rows, err := s.repo.ListChallenges(ctx, q)
	if err != nil {
		return ChallengePage{}, fmt.Errorf("list challenges: %w", err)
	}
	var page ChallengePage
	rows, last, more := cut(rows, limit)
	if more {
		page.Next = &Position{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	creators, err := s.repo.CreatorsOf(ctx, ids)
	if err != nil {
		return ChallengePage{}, fmt.Errorf("read creators: %w", err)
	}
	page.Items = make([]ChallengeSummary, len(rows))
	for i, r := range rows {
		page.Items[i] = ChallengeSummary{ChallengeRow: r, Status: statusAt(q.Now, r.StartsAt, r.EndsAt), Creators: creators[r.ID]}
	}
	return page, nil
}

// Challenge returns what an admin sees of challenge id, deleted or not:
// its status, weeks and invitation links at now, and its last admin
// changes. Admin only (RequireAdmin), checked before anything is read; 404
// challenge_not_found when there is no such challenge.
func (s *Service) Challenge(ctx context.Context, actor auth.Actor, id string) (ChallengeDetail, error) {
	if err := RequireAdmin(actor); err != nil {
		return ChallengeDetail{}, err
	}
	data, err := s.repo.Challenge(ctx, id)
	if errors.Is(err, errChallengeNotFound) {
		return ChallengeDetail{}, apperr.New(apperr.CodeChallengeNotFound, err)
	}
	if err != nil {
		return ChallengeDetail{}, fmt.Errorf("read challenge: %w", err)
	}
	c := data.Challenge
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return ChallengeDetail{}, fmt.Errorf("challenge time zone %q: %w", c.Timezone, err)
	}
	now := s.clock.Now()
	w := scoring.Window{Start: c.StartsAt, End: c.EndsAt, Location: loc, Now: now}
	locked := w.LockedWeeks()
	weeks := make([]Week, w.Weeks())
	for i := range weeks {
		n := i + 1
		start, end := w.WeekBounds(n)
		weeks[i] = Week{Number: n, StartsAt: start, EndsAt: end, LocksAt: w.WeekLocksAt(n), Locked: n <= locked}
		if at, ok := data.ClosedWeeks[n]; ok {
			weeks[i].ClosedAt = &at
		}
	}
	invitations := make([]Invitation, len(data.Invitations))
	for i, inv := range data.Invitations {
		invitations[i] = Invitation{InvitationRow: inv, Status: invitationStatus(inv, now)}
	}
	return ChallengeDetail{
		Challenge:       c,
		Categories:      data.Categories,
		Status:          statusAt(now, c.StartsAt, c.EndsAt),
		Creators:        data.Creators,
		Weeks:           weeks,
		Eliminations:    data.Eliminations,
		ResultsFrozenAt: data.ResultsFrozenAt,
		Invitations:     invitations,
		RecentActions:   data.RecentActions,
	}, nil
}

// pageLimit is the size of a page for limit: the contract bounds it, and a
// direct call below 1 still gets one item per page. The repository is asked
// one more, which tells whether a next page exists (cut).
func pageLimit(limit int) int {
	return max(limit, 1)
}

// cut keeps the first limit of rows, read with limit+1, and tells whether a
// next page exists; last is the last row kept, the position of the next
// page.
func cut[T any](rows []T, limit int) (kept []T, last T, more bool) {
	if len(rows) <= limit {
		return rows, last, false
	}
	return rows[:limit], rows[limit-1], true
}

// decodePosition reads the cursor of a list ordered by Position. It comes
// from the client: a position that is not one (bad id, no date) answers 400
// validation_failed on the field cursor, as a cursor that does not decode.
func decodePosition(cursor string) (Position, error) {
	var after Position
	if err := httpx.DecodeCursor(cursor, &after); err != nil {
		return Position{}, err
	}
	id, err := positionID(after.ID, after.CreatedAt)
	if err != nil {
		return Position{}, err
	}
	after.ID = id
	return after, nil
}

// decodeActivityPosition reads the cursor of the activities of an account,
// as decodePosition.
func decodeActivityPosition(cursor string) (ActivityPosition, error) {
	var after ActivityPosition
	if err := httpx.DecodeCursor(cursor, &after); err != nil {
		return ActivityPosition{}, err
	}
	id, err := positionID(after.ID, after.StartedAt)
	if err != nil {
		return ActivityPosition{}, err
	}
	after.ID = id
	return after, nil
}

// positionID checks a decoded position and returns its id normalized:
// uuid.Parse also accepts forms PostgreSQL refuses (urn:uuid:..., {...}).
// A position without its date (forged cursor) would restart the list.
func positionID(id string, at time.Time) (string, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return "", apperr.InvalidFields(err, "cursor")
	}
	if at.IsZero() {
		return "", apperr.InvalidFields(nil, "cursor")
	}
	return parsed.String(), nil
}

// Users returns a page of at most q.Limit (at least 1) accounts of q, q.After
// set here from cursor ("" for the first page), deleted ones included, with
// their e-mail. Admin only (RequireAdmin), checked before anything is read,
// the cursor included.
func (s *Service) Users(ctx context.Context, actor auth.Actor, q UserQuery, cursor string) (UserPage, error) {
	if err := RequireAdmin(actor); err != nil {
		return UserPage{}, err
	}
	if cursor != "" {
		after, err := decodePosition(cursor)
		if err != nil {
			return UserPage{}, err
		}
		q.After = &after
	}
	limit := pageLimit(q.Limit)
	q.Limit = limit + 1
	rows, err := s.repo.ListUsers(ctx, q)
	if err != nil {
		return UserPage{}, fmt.Errorf("list users: %w", err)
	}
	items, last, more := cut(rows, limit)
	page := UserPage{Items: items}
	if more {
		page.Next = &Position{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

// User returns account id, deleted or not, with its e-mail. Admin only
// (RequireAdmin), checked before anything is read; 404 user_not_found when
// there is no such account.
func (s *Service) User(ctx context.Context, actor auth.Actor, id string) (UserRow, error) {
	if err := RequireAdmin(actor); err != nil {
		return UserRow{}, err
	}
	u, err := s.repo.User(ctx, id)
	if err != nil {
		return UserRow{}, userError("read user", err)
	}
	return u, nil
}

// UserChallenges returns a page of at most limit (at least 1) challenges
// account id joined or creates, deleted ones included, each with its role
// and its status at now. Admin only (RequireAdmin), checked before anything
// is read, the cursor included; 404 user_not_found when there is no such
// account.
func (s *Service) UserChallenges(ctx context.Context, actor auth.Actor, id string, limit int, cursor string) (UserChallengePage, error) {
	if err := RequireAdmin(actor); err != nil {
		return UserChallengePage{}, err
	}
	var q PageQuery
	if cursor != "" {
		after, err := decodePosition(cursor)
		if err != nil {
			return UserChallengePage{}, err
		}
		q.After = &after
	}
	limit = pageLimit(limit)
	q.Limit = limit + 1
	rows, err := s.repo.ListUserChallenges(ctx, id, q)
	if err != nil {
		return UserChallengePage{}, userError("list user challenges", err)
	}
	now := s.clock.Now()
	var page UserChallengePage
	rows, last, more := cut(rows, limit)
	if more {
		page.Next = &Position{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	page.Items = make([]UserChallenge, len(rows))
	for i, r := range rows {
		page.Items[i] = UserChallenge{UserChallengeRow: r, Status: statusAt(now, r.StartsAt, r.EndsAt)}
	}
	return page, nil
}

// UserActivities returns a page of at most limit (at least 1) activities of
// account id, deleted ones excluded, the most recent session first. Admin
// only (RequireAdmin), checked before anything is read, the cursor included;
// 404 user_not_found when there is no such account.
func (s *Service) UserActivities(ctx context.Context, actor auth.Actor, id string, limit int, cursor string) (ActivityPage, error) {
	if err := RequireAdmin(actor); err != nil {
		return ActivityPage{}, err
	}
	var q ActivityQuery
	if cursor != "" {
		after, err := decodeActivityPosition(cursor)
		if err != nil {
			return ActivityPage{}, err
		}
		q.After = &after
	}
	limit = pageLimit(limit)
	q.Limit = limit + 1
	rows, err := s.repo.ListUserActivities(ctx, id, q)
	if err != nil {
		return ActivityPage{}, userError("list user activities", err)
	}
	items, last, more := cut(rows, limit)
	page := ActivityPage{Items: items}
	if more {
		page.Next = &ActivityPosition{StartedAt: last.StartedAt, ID: last.ID}
	}
	return page, nil
}

// userError gives errUserNotFound its 404 user_not_found, and wraps any
// other error with what failed.
func userError(what string, err error) error {
	if errors.Is(err, errUserNotFound) {
		return apperr.New(apperr.CodeUserNotFound, err)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// Actions returns a page of at most q.Limit (at least 1) actions of the
// journal of q, q.After set here from cursor ("" for the first page), most
// recent first. Admin only (RequireAdmin), checked before anything is read,
// the cursor included.
func (s *Service) Actions(ctx context.Context, actor auth.Actor, q ActionQuery, cursor string) (ActionPage, error) {
	if err := RequireAdmin(actor); err != nil {
		return ActionPage{}, err
	}
	if cursor != "" {
		after, err := decodePosition(cursor)
		if err != nil {
			return ActionPage{}, err
		}
		q.After = &after
	}
	limit := pageLimit(q.Limit)
	q.Limit = limit + 1
	rows, err := s.repo.ListActions(ctx, q)
	if err != nil {
		return ActionPage{}, fmt.Errorf("list admin actions: %w", err)
	}
	items, last, more := cut(rows, limit)
	page := ActionPage{Items: items}
	if more {
		page.Next = &Position{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}
