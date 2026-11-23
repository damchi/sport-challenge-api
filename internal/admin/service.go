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
	limit := q.Limit
	// One more than asked tells whether a next page exists.
	q.Limit = limit + 1
	rows, err := s.repo.ListChallenges(ctx, q)
	if err != nil {
		return ChallengePage{}, fmt.Errorf("list challenges: %w", err)
	}
	var page ChallengePage
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		page.Next = &ChallengePosition{CreatedAt: last.CreatedAt, ID: last.ID}
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
// its status, weeks and invitation links at now. Admin only (RequireAdmin),
// checked before anything is read; 404 challenge_not_found when there is
// no such challenge.
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
	}, nil
}

// decodePosition reads a cursor of ListAdminChallenges. It comes from the
// client: a position that is not one (bad id, no date) answers 400
// validation_failed on the field cursor, as a cursor that does not decode.
func decodePosition(cursor string) (ChallengePosition, error) {
	var after ChallengePosition
	if err := httpx.DecodeCursor(cursor, &after); err != nil {
		return ChallengePosition{}, err
	}
	// Normalized: uuid.Parse also accepts forms PostgreSQL refuses
	// (urn:uuid:..., {...}).
	id, err := uuid.Parse(after.ID)
	if err != nil {
		return ChallengePosition{}, apperr.InvalidFields(err, "cursor")
	}
	after.ID = id.String()
	// A position without its date (forged cursor) would restart the list.
	if after.CreatedAt.IsZero() {
		return ChallengePosition{}, apperr.InvalidFields(nil, "cursor")
	}
	return after, nil
}
