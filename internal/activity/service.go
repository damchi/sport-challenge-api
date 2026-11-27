package activity

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

// repository is what the service needs from the storage.
type repository interface {
	Record(ctx context.Context, a Activity, decide func(ctx context.Context) error) (Activity, bool, error)
	Update(ctx context.Context, actorID, id string, a Activity, decide func(ctx context.Context, current Activity) error) (Activity, error)
	Delete(ctx context.Context, actorID, id string, at time.Time, decide func(ctx context.Context, current Activity) error) error
	List(ctx context.Context, q ListQuery) ([]Activity, error)
	DayTotals(ctx context.Context, userID string, bounds []time.Time) ([]Totals, error)
	SportCounts(ctx context.Context, userID string) ([]SportCount, int, error)
	Weeks(ctx context.Context, q WeekQuery) ([]WeekTotals, error)
	EraseUser(ctx context.Context, userID string) error
	// Get and LockAccount serve the admin changes, in the transaction of
	// ctx.
	Get(ctx context.Context, id string) (Activity, error)
	LockAccount(ctx context.Context, userID string) error
}

// Profiles tells whether a user's profile is complete, and gives the public
// name of each user of ids, an anonymized account having none (module user).
type Profiles interface {
	ProfileComplete(ctx context.Context, userID string) (bool, error)
	Names(ctx context.Context, ids []string) (map[string]Name, error)
}

// Catalogue gives the rules of a sport (its distance mode and whether it
// takes an elevation gain), and false for a sport it does not have (module
// sport).
type Catalogue interface {
	SportRules(ctx context.Context, code string) (SportRules, bool, error)
}

// Participations gives the challenges a user joined, deleted ones left out,
// read in the transaction of ctx, if any (module challenge).
type Participations interface {
	Participations(ctx context.Context, userID string) ([]Participation, error)
}

// Challenges gives challenge challengeID, as a participation of userID, for
// actor to read the activities of userID there; it answers 404
// challenge_not_found or not_found when the rights matrix forbids it
// (module challenge).
//
// AllActivitiesScope gives challenge challengeID, its membership left unset,
// and the participation of each of its participants, abandoned ones
// included and deleted accounts left out, by user id, for actor to read
// the activities of all of them there; it answers 404 challenge_not_found
// or not_found when the rights matrix forbids it.
type Challenges interface {
	ActivitiesScope(ctx context.Context, actor auth.Actor, challengeID, userID string) (Participation, error)
	AllActivitiesScope(ctx context.Context, actor auth.Actor, challengeID string) (Participation, map[string]Participation, error)
}

// Service holds the use cases of the activities.
type Service struct {
	repo           repository
	profiles       Profiles
	catalogue      Catalogue
	participations Participations
	challenges     Challenges
	clock          clock.Clock
}

// NewService returns a Service.
func NewService(repo repository, profiles Profiles, catalogue Catalogue, participations Participations, challenges Challenges, c clock.Clock) *Service {
	return &Service{repo: repo, profiles: profiles, catalogue: catalogue, participations: participations, challenges: challenges, clock: c}
}

// Record records draft for actorID, always for themselves: the single entry
// point of activity creation (manual entry now, imports later). The actor
// needs a complete profile (422 profile_incomplete); the draft is validated
// (Validate), an unknown sport giving 400 validation_failed on sport_code.
// The activity is refused with 422 week_locked when its week is locked in a
// challenge where it counts, or was when the actor left one (IsLocked),
// checked in the transaction of the write, and its repetitions with 422
// unknown_exercise for an exercise none of those challenges offers
// (UnknownExerciseRefusal); it is recorded even if it counts nowhere (ADR
// 0001). With idempotencyKey, an
// activity of the actor recorded before with the same key is returned
// instead, nothing being written, or 404 activity_not_found once deleted.
func (s *Service) Record(ctx context.Context, actorID string, idempotencyKey *string, draft Draft) (Recorded, error) {
	complete, err := s.profiles.ProfileComplete(ctx, actorID)
	if err != nil {
		return Recorded{}, fmt.Errorf("check profile: %w", err)
	}
	if !complete {
		return Recorded{}, apperr.New(apperr.CodeProfileIncomplete, nil)
	}

	draft, err = s.validate(ctx, draft)
	if err != nil {
		return Recorded{}, err
	}

	a := Activity{
		UserID:              actorID,
		SportCode:           draft.SportCode,
		SportLabel:          draft.SportLabel,
		StartedAt:           draft.StartedAt.UTC(),
		DurationSeconds:     draft.DurationSeconds,
		DistanceMeters:      draft.DistanceMeters,
		ElevationGainMeters: draft.ElevationGainMeters,
		// Sorted by exercise, as they are read back.
		Repetitions: slices.SortedFunc(slices.Values(draft.Repetitions), func(a, b Repetitions) int {
			return strings.Compare(a.Exercise, b.Exercise)
		}),
		Source:         SourceManual,
		IdempotencyKey: idempotencyKey,
	}
	var challenges []Participation
	stored, created, err := s.repo.Record(ctx, a, func(ctx context.Context) error {
		ps, err := s.participations.Participations(ctx, actorID)
		if err != nil {
			return fmt.Errorf("read participations: %w", err)
		}
		if IsLocked(s.clock.Now(), a.StartedAt, ps) {
			return apperr.New(apperr.CodeWeekLocked, nil)
		}
		if err := UnknownExerciseRefusal(a.SportCode, a.StartedAt, a.Repetitions, ps); err != nil {
			return err
		}
		challenges = ps
		return nil
	})
	if err != nil {
		return Recorded{}, storageError(err)
	}
	if !created {
		// Recorded before: where it counts now, outside the transaction
		// that wrote nothing.
		if challenges, err = s.participations.Participations(ctx, actorID); err != nil {
			return Recorded{}, fmt.Errorf("read participations: %w", err)
		}
	}
	return Recorded{
		Activity:      stored,
		Contributions: Contributions(stored, challenges),
		Created:       created,
		Locked:        IsLocked(s.clock.Now(), stored.StartedAt, challenges),
	}, nil
}

// Update replaces activity id of actorID with draft, validated as at the
// creation (Record): the body replaces every field entered. The activity of
// someone else, a deleted or a missing one gives 404 activity_not_found. It
// is refused with 422 week_locked when the week of its current start, or of
// the new one, is locked (IsLocked), checked in the
// transaction of the write. The profile is not checked again: the activity
// was recorded with a complete one. A badge won stays won (README §2.7,
// V1.1).
func (s *Service) Update(ctx context.Context, actorID, id string, draft Draft) (Recorded, error) {
	// The change of the repetitions comes with SC-API-221.
	if draft.Repetitions != nil {
		return Recorded{}, apperr.InvalidFields(nil, "repetitions")
	}
	draft, err := s.validate(ctx, draft)
	if err != nil {
		return Recorded{}, err
	}
	a := Activity{
		SportCode:       draft.SportCode,
		SportLabel:      draft.SportLabel,
		StartedAt:       draft.StartedAt.UTC(),
		DurationSeconds: draft.DurationSeconds,
		DistanceMeters:  draft.DistanceMeters,
		// Left out of the body, the elevation gain is removed: the body
		// replaces every field entered.
		ElevationGainMeters: draft.ElevationGainMeters,
	}
	var challenges []Participation
	stored, err := s.repo.Update(ctx, actorID, id, a, func(ctx context.Context, current Activity) error {
		// The current start is checked by changeable, the new one here,
		// on the same participations.
		ps, err := s.changeable(ctx, actorID, current)
		if err != nil {
			return err
		}
		if IsLocked(s.clock.Now(), a.StartedAt, ps) {
			return apperr.New(apperr.CodeWeekLocked, nil)
		}
		challenges = ps
		return nil
	})
	if err != nil {
		return Recorded{}, storageError(err)
	}
	return Recorded{
		Activity:      stored,
		Contributions: Contributions(stored, challenges),
		Locked:        IsLocked(s.clock.Now(), stored.StartedAt, challenges),
	}, nil
}

// EraseUser deletes every activity of userID, for the deletion of their
// account (README §6), in its transaction (ctx carries it). No week lock
// applies: the account leaves every challenge, finished ones included. It
// checks no actor: never expose it on a route, only user.Service.Delete
// calls it.
func (s *Service) EraseUser(ctx context.Context, userID string) error {
	if err := s.repo.EraseUser(ctx, userID); err != nil {
		return fmt.Errorf("erase user: %w", err)
	}
	return nil
}

// Delete marks activity id of actorID deleted: it counts nowhere from then
// on. As for Update, the activity of someone else gives 404
// activity_not_found and a locked week (IsLocked) 422 week_locked.
func (s *Service) Delete(ctx context.Context, actorID, id string) error {
	// This instant is only the deleted_at stored: the lock is checked against
	// the clock read in the transaction, once the week lock is held
	// (changeable).
	err := s.repo.Delete(ctx, actorID, id, s.clock.Now(), func(ctx context.Context, current Activity) error {
		_, err := s.changeable(ctx, actorID, current)
		return err
	})
	if err != nil {
		return storageError(err)
	}
	return nil
}

// Filter keeps the activities started in [From, To), a nil bound being
// open, and those of sport Sport only when set (an unknown sport keeps
// none).
type Filter struct {
	From  *time.Time
	To    *time.Time
	Sport *string
}

// Mine returns a page of the activities of actorID that filter keeps, most
// recent first, each with the challenges where it counts now and whether it
// is locked. A filter whose To does not come after its From gives 400
// validation_failed on to.
func (s *Service) Mine(ctx context.Context, actorID string, filter Filter, limit int, after *Position) (Page, error) {
	if limit < 1 {
		return Page{}, apperr.InvalidFields(nil, "limit")
	}
	if filter.From != nil && filter.To != nil && !filter.From.Before(*filter.To) {
		return Page{}, apperr.InvalidFields(nil, "to")
	}
	q := ListQuery{UserIDs: []string{actorID}, From: filter.From, To: filter.To, SportCode: filter.Sport, Limit: limit + 1, After: after}
	return s.page(ctx, q, limit, func(ctx context.Context) (func(string) []Participation, error) {
		ps, err := s.participations.Participations(ctx, actorID)
		if err != nil {
			return nil, fmt.Errorf("read participations: %w", err)
		}
		return func(string) []Participation { return ps }, nil
	})
}

// MySummary sums the activities of actorID, deleted ones left out, whether
// they count in a challenge or not: the week of the clock's now in the IANA
// zone tz (localWeek), day by day, the number of activities of each sport
// and their total duration; and counts the challenges actorID joined,
// whatever their status, abandoned ones included and deleted ones left out
// (README §2.8). An unknown zone gives 400 validation_failed on tz.
func (s *Service) MySummary(ctx context.Context, actorID, tz string) (Summary, error) {
	loc, err := loadZone(tz)
	if err != nil {
		return Summary{}, apperr.InvalidFields(err, "tz")
	}
	w := weekOf(s.clock.Now(), loc)
	days, err := s.repo.DayTotals(ctx, actorID, w.Bounds[:])
	if err != nil {
		return Summary{}, fmt.Errorf("read day totals: %w", err)
	}
	if len(days) != 7 {
		return Summary{}, fmt.Errorf("read day totals: %d days, want 7", len(days))
	}
	sports, duration, err := s.repo.SportCounts(ctx, actorID)
	if err != nil {
		return Summary{}, fmt.Errorf("read sport counts: %w", err)
	}
	ps, err := s.participations.Participations(ctx, actorID)
	if err != nil {
		return Summary{}, fmt.Errorf("read participations: %w", err)
	}
	out := Summary{
		CurrentWeek:          CurrentWeek{Week: Week{StartsAt: w.Start(), EndsAt: w.End()}, Days: make([]Day, 0, len(days))},
		Sports:               sports,
		TotalDurationSeconds: duration,
		ChallengeCount:       len(ps),
	}
	for i, d := range days {
		out.CurrentWeek.Days = append(out.CurrentWeek.Days, Day{Date: w.Day(i), Sessions: d.Sessions, DurationSeconds: d.DurationSeconds})
		out.CurrentWeek.Sessions += d.Sessions
		out.CurrentWeek.DurationSeconds += d.DurationSeconds
		out.CurrentWeek.DistanceMeters += d.DistanceMeters
		out.CurrentWeek.ElevationGainMeters += d.ElevationGainMeters
	}
	for _, c := range sports {
		out.Total += c.Count
	}
	return out, nil
}

// MyWeeks returns a page of the weeks of the activities of actorID in the
// IANA zone tz (localWeek) that have at least one activity, deleted ones
// left out, of sport when set (an unknown sport keeps none), most recent
// first, after the week after when set, each with its totals. An unknown
// zone gives 400 validation_failed on tz.
func (s *Service) MyWeeks(ctx context.Context, actorID, tz string, sport *string, limit int, after *WeekPosition) (WeekPage, error) {
	if limit < 1 {
		return WeekPage{}, apperr.InvalidFields(nil, "limit")
	}
	loc, err := loadZone(tz)
	if err != nil {
		return WeekPage{}, apperr.InvalidFields(err, "tz")
	}
	q := WeekQuery{UserID: actorID, Zone: tz, SportCode: sport, Limit: limit + 1}
	if after != nil {
		// The weeks before that one: the activities started before it.
		before := weekStarting(after.Monday, loc).Start()
		q.Before = &before
	}
	rows, err := s.repo.Weeks(ctx, q)
	if errors.Is(err, errUnknownZone) {
		return WeekPage{}, apperr.InvalidFields(err, "tz")
	}
	if err != nil {
		return WeekPage{}, fmt.Errorf("list weeks: %w", err)
	}
	var page WeekPage
	if len(rows) > limit {
		rows = rows[:limit]
		page.Next = &WeekPosition{Monday: rows[limit-1].Monday}
	}
	page.Items = make([]Week, 0, len(rows))
	for _, row := range rows {
		w := weekStarting(row.Monday, loc)
		page.Items = append(page.Items, Week{StartsAt: w.Start(), EndsAt: w.End(), Totals: row.Totals})
	}
	return page, nil
}

// OfParticipant returns a page of the activities of userID started in the
// window of challenge challengeID, most recent first, for actor: the
// participants of the challenge (abandoned ones included), its creators and
// the admins only, as module challenge decides (Challenges). Where each one counts and
// whether it is locked are read from this challenge alone: the other
// challenges of userID, private ones maybe, never show through.
func (s *Service) OfParticipant(ctx context.Context, actor auth.Actor, challengeID, userID string, limit int, after *Position) (Page, error) {
	if limit < 1 {
		return Page{}, apperr.InvalidFields(nil, "limit")
	}
	c, err := s.challenges.ActivitiesScope(ctx, actor, challengeID, userID)
	if err != nil {
		return Page{}, fmt.Errorf("activities scope: %w", err)
	}
	q := ListQuery{UserIDs: []string{userID}, From: &c.StartsAt, To: &c.EndsAt, Limit: limit + 1, After: after}
	return s.page(ctx, q, limit, func(context.Context) (func(string) []Participation, error) {
		return func(string) []Participation { return []Participation{c} }, nil
	})
}

// OfChallenge returns a page of the activities of all the participants of
// challenge challengeID (abandoned ones included, deleted accounts left
// out) started in its window, most recent first, each with its author, for
// actor: the participants of the challenge, its creators and the admins
// only, as module challenge decides (Challenges). Where each one counts and whether
// it is locked are read from this challenge alone, with the participation
// of its author, as OfParticipant reads them.
func (s *Service) OfChallenge(ctx context.Context, actor auth.Actor, challengeID string, limit int, after *Position) (AuthoredPage, error) {
	if limit < 1 {
		return AuthoredPage{}, apperr.InvalidFields(nil, "limit")
	}
	c, members, err := s.challenges.AllActivitiesScope(ctx, actor, challengeID)
	if err != nil {
		return AuthoredPage{}, fmt.Errorf("all activities scope: %w", err)
	}
	if len(members) == 0 {
		return AuthoredPage{Items: []Authored{}}, nil
	}
	ids := slices.Sorted(maps.Keys(members))
	q := ListQuery{UserIDs: ids, From: &c.StartsAt, To: &c.EndsAt, Limit: limit + 1, After: after}
	page, err := s.page(ctx, q, limit, func(context.Context) (func(string) []Participation, error) {
		return func(userID string) []Participation { return []Participation{members[userID]} }, nil
	})
	if err != nil {
		return AuthoredPage{}, err
	}
	out := AuthoredPage{Items: make([]Authored, 0, len(page.Items)), Next: page.Next}
	if len(page.Items) == 0 {
		return out, nil
	}
	authors := make(map[string]struct{}, len(page.Items))
	for _, rec := range page.Items {
		authors[rec.UserID] = struct{}{}
	}
	names, err := s.profiles.Names(ctx, slices.Sorted(maps.Keys(authors)))
	if err != nil {
		return AuthoredPage{}, fmt.Errorf("read names: %w", err)
	}
	for _, rec := range page.Items {
		// An account deleted since the participants were read: listed
		// nowhere.
		name, ok := names[rec.UserID]
		if !ok {
			continue
		}
		out.Items = append(out.Items, Authored{Recorded: rec, Author: name})
	}
	return out, nil
}

// page reads the activities of q, limit asked plus one to tell whether a
// next page exists, with where each counts and whether it is locked, among
// the challenges that scope gives for its author (scope read once, and only
// for a page with activities).
func (s *Service) page(ctx context.Context, q ListQuery, limit int, scope func(ctx context.Context) (func(userID string) []Participation, error)) (Page, error) {
	rows, err := s.repo.List(ctx, q)
	if err != nil {
		return Page{}, fmt.Errorf("list activities: %w", err)
	}
	var page Page
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		page.Next = &Position{StartedAt: last.StartedAt, ID: last.ID}
	}
	page.Items = make([]Recorded, 0, len(rows))
	if len(rows) == 0 {
		return page, nil
	}
	of, err := scope(ctx)
	if err != nil {
		return Page{}, err
	}
	now := s.clock.Now()
	for _, a := range rows {
		ps := of(a.UserID)
		page.Items = append(page.Items, Recorded{
			Activity:      a,
			Contributions: Contributions(a, ps),
			Locked:        IsLocked(now, a.StartedAt, ps),
		})
	}
	return page, nil
}

// validate checks draft against the catalogue at the instant of the clock
// (Validate), an unknown sport giving 400 validation_failed on sport_code.
func (s *Service) validate(ctx context.Context, draft Draft) (Draft, error) {
	rules, found, err := s.catalogue.SportRules(ctx, draft.SportCode)
	if err != nil {
		return Draft{}, fmt.Errorf("read catalogue: %w", err)
	}
	if !found {
		return Draft{}, apperr.InvalidFields(nil, "sport_code")
	}
	return Validate(draft, rules, s.clock.Now())
}

// changeable checks that actorID may change current (CanChange, else 404
// activity_not_found) and that its week is not locked (IsLocked, 422
// week_locked). It returns the participations of the author,
// read in the transaction of ctx.
func (s *Service) changeable(ctx context.Context, actorID string, current Activity) ([]Participation, error) {
	if !CanChange(actorID, current.UserID) {
		return nil, apperr.New(apperr.CodeActivityNotFound, nil)
	}
	ps, err := s.participations.Participations(ctx, actorID)
	if err != nil {
		return nil, fmt.Errorf("read participations: %w", err)
	}
	if IsLocked(s.clock.Now(), current.StartedAt, ps) {
		return nil, apperr.New(apperr.CodeWeekLocked, nil)
	}
	return ps, nil
}

// storageError turns the errors of the storage into errors of the API.
func storageError(err error) error {
	switch {
	case errors.Is(err, errActivityNotFound):
		return apperr.New(apperr.CodeActivityNotFound, nil)
	case errors.Is(err, errAccountDeleted):
		return apperr.New(apperr.CodeAccountDeleted, nil)
	case errors.Is(err, errActivityOverlap):
		return apperr.New(apperr.CodeActivityOverlap, nil)
	}
	return err
}
