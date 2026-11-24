package challenge

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
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// repository is what the service needs from the storage.
type repository interface {
	Create(ctx context.Context, c Challenge, joinedAt time.Time) (Challenge, error)
	List(ctx context.Context, q ListQuery) ([]Summary, error)
	Counts(ctx context.Context, q ListQuery) (Counts, error)
	// Get, Join, Abandon, Exclude, AddCreator and Delete judge the
	// invitations at now (Membership.Invited).
	Get(ctx context.Context, id, actorID string, now time.Time) (Challenge, Membership, error)
	Categories(ctx context.Context, id string) ([]Category, error)
	CategoriesOf(ctx context.Context, ids []string) (map[string][]Category, error)
	UpdateText(ctx context.Context, id string, changes TextChanges) error
	// UpdateSettings writes the challenge decide returns, columns and
	// categories, under the lock of challenge id.
	UpdateSettings(ctx context.Context, id, actorID string, now time.Time, decide func(Challenge, []Category) (Challenge, error)) error
	Join(ctx context.Context, id, actorID string, now time.Time, decide func(Challenge, Membership) (time.Time, error)) error
	Abandon(ctx context.Context, id, actorID string, now time.Time, decide func(Challenge, Membership) (bool, time.Time, error)) error
	Exclude(ctx context.Context, id, actorID, targetID string, now time.Time, decide func(c Challenge, actor, target Membership) (bool, time.Time, error)) error
	AddCreator(ctx context.Context, id, actorID, targetID string, now time.Time, decide func(c Challenge, actor, target Membership) (bool, error)) error
	Delete(ctx context.Context, id, actorID string, now time.Time, decide func(Challenge, Membership) (time.Time, error)) error
	// Restore serves the admin restore of a deleted challenge, in the
	// transaction ctx carries.
	Restore(ctx context.Context, id string) (time.Time, error)
	// LockAccount, Reinstate and RemoveCreator serve the admin changes of
	// the members, in the transaction ctx carries.
	LockAccount(ctx context.Context, id string) error
	Reinstate(ctx context.Context, id, userID string, now time.Time, decide func(Challenge, Membership) (bool, error)) error
	RemoveCreator(ctx context.Context, id, userID string, now time.Time, decide func(c Challenge, m Membership, otherCreators int) (bool, error)) error
	Participants(ctx context.Context, id string, limit int, after *ParticipantPosition) ([]Participant, error)
	ParticipantMemberships(ctx context.Context, id string) (map[string]Membership, error)
	ActiveParticipations(ctx context.Context, userID string, now time.Time) ([]string, error)
	LastCreatorChallenges(ctx context.Context, userID string, now time.Time) ([]string, error)
	Memberships(ctx context.Context, userID string) ([]UserChallenge, error)
	EraseUser(ctx context.Context, userID string) error
}

// Profiles tells whether a user's profile is complete, and gives the public
// names of users, none for a deleted (anonymized) account (module user).
type Profiles interface {
	ProfileComplete(ctx context.Context, userID string) (bool, error)
	Names(ctx context.Context, userIDs []string) (map[string]Name, error)
}

// Catalogue gives the suggested factor of every sport with a distance, and
// the distance mode of a sport, false when there is no such sport (module
// sport).
type Catalogue interface {
	SuggestedFactors(ctx context.Context) (map[string]float64, error)
	DistanceMode(ctx context.Context, code string) (string, bool, error)
}

// Standings gives where actorID stands in each of challenges that is in its
// weeks at now and where they are an active participant, by challenge ID;
// the others are left out. Results gives actorID's line in each category of
// the frozen results of challenges, in category order, by challenge ID; those
// not frozen yet are left out (module leaderboard). challenges carry their
// categories.
type Standings interface {
	Standings(ctx context.Context, actorID string, challenges []Challenge, now time.Time) (map[string]Standing, error)
	Results(ctx context.Context, actorID string, challenges []Challenge) (map[string][]Ranking, error)
}

// Service holds the use cases of the challenges.
type Service struct {
	repo      repository
	profiles  Profiles
	catalogue Catalogue
	standings Standings
	clock     clock.Clock
}

// NewService returns a Service.
func NewService(repo repository, profiles Profiles, catalogue Catalogue, standings Standings, c clock.Clock) *Service {
	return &Service{repo: repo, profiles: profiles, catalogue: catalogue, standings: standings, clock: c}
}

// Create creates the challenge described by draft, with actorID as its
// creator and first participant. The actor needs a complete profile (422
// profile_incomplete). The distance factors are the catalogue's suggestions
// brought back to the reference sport (referenceFactors), replaced by those
// of the draft, then frozen in the categories that use them (the cumulative
// distance, the points with the longest distance); the elimination is run in
// the reference sport. The minimum session of the points is stored in
// seconds.
func (s *Service) Create(ctx context.Context, actorID string, draft Draft) (Challenge, error) {
	complete, err := s.profiles.ProfileComplete(ctx, actorID)
	if err != nil {
		return Challenge{}, fmt.Errorf("check profile: %w", err)
	}
	if !complete {
		return Challenge{}, apperr.New(apperr.CodeProfileIncomplete, nil)
	}

	suggested, err := s.catalogue.SuggestedFactors(ctx)
	if err != nil {
		return Challenge{}, fmt.Errorf("read catalogue: %w", err)
	}
	distanceSports := make(map[string]bool, len(suggested))
	for code := range suggested {
		distanceSports[code] = true
	}

	reference := draft.reference()
	// An unknown sport has no mode: Validate refuses it.
	referenceMode, _, err := s.catalogue.DistanceMode(ctx, reference)
	if err != nil {
		return Challenge{}, fmt.Errorf("read catalogue: %w", err)
	}

	now := s.clock.Now()
	window, err := Validate(draft, now, distanceSports, referenceMode)
	if err != nil {
		return Challenge{}, err
	}

	var factors map[string]float64
	if draft.usesFactors() {
		factors = referenceFactors(suggested, reference)
		maps.Copy(factors, draft.DistanceFactors)
	}

	c, err := s.repo.Create(ctx, Challenge{
		Title:            strings.TrimSpace(draft.Title),
		Description:      noEmpty(draft.Description),
		Visibility:       draft.Visibility,
		MembersCanInvite: draft.MembersCanInvite,
		Timezone:         draft.Timezone,
		ReferenceSport:   reference,
		StartsAt:         window.StartsAt,
		EndsAt:           window.EndsAt,
		CreatedBy:        actorID,
		Categories:       categoriesOf(draft, factors),
	}, now)
	if errors.Is(err, errAccountDeleted) {
		return Challenge{}, apperr.New(apperr.CodeAccountDeleted, nil)
	}
	if err != nil {
		return Challenge{}, fmt.Errorf("store challenge: %w", err)
	}
	return c, nil
}

// categoriesOf builds the categories of draft, in position order: the
// cumulative time always, then those chosen. factors go to the categories
// that use them (the cumulative distance, the points with the longest
// distance); the elimination is run in the reference sport; the minimum
// session of the points is stored in seconds.
func categoriesOf(draft Draft, factors map[string]float64) []Category {
	categories := []Category{{Type: CategoryCumulativeTime, Position: positionTime, Params: Params{}}}
	if draft.Distance {
		categories = append(categories, Category{Type: CategoryCumulativeDistance, Position: positionDistance, Params: Params{Factors: factors}})
	}
	if e := draft.Elimination; e != nil {
		categories = append(categories, Category{Type: CategoryElimination, Position: positionElimination, Params: Params{Sport: draft.reference(), StepMeters: e.StepMeters}})
	}
	if p := draft.Points; p != nil {
		params := Params{Criteria: p.Criteria, Podium: p.Podium, MinSessionSeconds: p.minSessionMinutes() * 60}
		if p.longestDistance() {
			params.Factors = factors
		}
		categories = append(categories, Category{Type: CategoryPoints, Position: positionPoints, Params: params})
	}
	if draft.Elevation {
		categories = append(categories, Category{Type: CategoryElevation, Position: positionElevation, Params: Params{}})
	}
	return categories
}

// noEmpty is description, none when empty: an empty description is no
// description, at creation as on edit.
func noEmpty(description *string) *string {
	if description != nil && *description == "" {
		return nil
	}
	return description
}

// List returns a page of challenges for actorID: public ones, or those the
// actor created or joined. The status filter, the status of each challenge
// and the counts by status use the same instant of the clock; the counts are
// those of the whole scope, whatever the status filter and the page. The
// visibility rules are applied by the queries themselves, never by filtering
// a loaded list. Each item has its number of weeks and its current week, as
// the detail computes them, for every actor. In the actor's own list, each
// ongoing challenge they are an active participant of has their standing
// (MyWeek), and each finished one their best frozen result (BestResult),
// read for the whole page at once.
func (s *Service) List(ctx context.Context, actorID, scope, status string, limit int, after *Position) (Page, error) {
	if limit < 1 {
		return Page{}, apperr.InvalidFields(nil, "limit")
	}
	now := s.clock.Now()
	// One more than asked tells whether a next page exists.
	items, err := s.repo.List(ctx, ListQuery{
		ActorID: actorID, Scope: scope, Status: status,
		Now: now, Limit: limit + 1, After: after,
	})
	if err != nil {
		return Page{}, fmt.Errorf("list challenges: %w", err)
	}
	counts, err := s.repo.Counts(ctx, ListQuery{ActorID: actorID, Scope: scope, Now: now})
	if err != nil {
		return Page{}, fmt.Errorf("list challenges: %w", err)
	}
	for i := range items {
		it := &items[i]
		m := Membership{Creator: it.Creator, Invited: it.Invited}
		if it.MyStatus != nil {
			m.Participant = ParticipantStatus(*it.MyStatus)
		}
		it.Role = RoleOf(m)
		it.Status = StatusAt(now, it.StartsAt, it.EndsAt)
		loc, err := location(it.Timezone)
		if err != nil {
			return Page{}, fmt.Errorf("list challenges: challenge time zone %q: %w", it.Timezone, err)
		}
		w := scoring.Window{Start: it.StartsAt, End: it.EndsAt, Location: loc, Now: now}
		it.WeekCount = w.Weeks()
		it.CurrentWeek, it.WeekEndsAt = w.CurrentWeek()
	}
	page := Page{Items: items, Counts: counts}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		page.Next = &Position{StartsAt: last.StartsAt, ID: last.ID}
	}
	if scope == ScopeMine {
		if err := s.addRankings(ctx, actorID, page.Items, now); err != nil {
			return Page{}, fmt.Errorf("list challenges: %w", err)
		}
	}
	return page, nil
}

// addRankings sets, on the challenges of items where actorID is an active
// participant, MyWeek on the ongoing ones, at now, the instant of their
// status, and BestResult on the finished ones: the categories, the standings
// and the results each read once for all of them.
func (s *Service) addRankings(ctx context.Context, actorID string, items []Summary, now time.Time) error {
	var running, finished []Challenge
	var ids []string
	for _, it := range items {
		if it.MyStatus == nil || ParticipantStatus(*it.MyStatus) != ParticipantActive {
			continue
		}
		c := Challenge{ID: it.ID, Timezone: it.Timezone, StartsAt: it.StartsAt, EndsAt: it.EndsAt}
		switch it.Status {
		case StatusOngoing:
			running = append(running, c)
		case StatusFinished:
			finished = append(finished, c)
		default:
			continue
		}
		ids = append(ids, it.ID)
	}
	if len(ids) == 0 {
		return nil
	}
	categories, err := s.repo.CategoriesOf(ctx, ids)
	if err != nil {
		return err
	}
	for _, cs := range [][]Challenge{running, finished} {
		for i := range cs {
			cs[i].Categories = categories[cs[i].ID]
		}
	}
	standings := map[string]Standing{}
	if len(running) > 0 {
		if standings, err = s.standings.Standings(ctx, actorID, running, now); err != nil {
			return err
		}
	}
	results := map[string][]Ranking{}
	if len(finished) > 0 {
		if results, err = s.standings.Results(ctx, actorID, finished); err != nil {
			return err
		}
	}
	for i := range items {
		if st, ok := standings[items[i].ID]; ok {
			items[i].MyWeek = &st
		}
		if r, ok := results[items[i].ID]; ok {
			items[i].BestResult = BestResult(r)
		}
	}
	return nil
}

// Participants returns a page of the participants of challenge id, visible
// to whoever sees the challenge, admins included (ParticipantsRefusal):
// anyone else gets the same 404 challenge_not_found as for a missing
// challenge. A deleted (anonymized) account is not listed.
func (s *Service) Participants(ctx context.Context, actor auth.Actor, id string, limit int, after *ParticipantPosition) (ParticipantPage, error) {
	if limit < 1 {
		return ParticipantPage{}, apperr.InvalidFields(nil, "limit")
	}
	c, m, err := s.repo.Get(ctx, id, actor.ID, s.clock.Now())
	if errors.Is(err, errChallengeNotFound) {
		return ParticipantPage{}, apperr.New(apperr.CodeChallengeNotFound, nil)
	}
	if err != nil {
		return ParticipantPage{}, fmt.Errorf("list participants: %w", err)
	}
	if err := ParticipantsRefusal(m, actor.Admin, c.Visibility); err != nil {
		return ParticipantPage{}, err
	}
	// One more than asked tells whether a next page exists.
	items, err := s.repo.Participants(ctx, id, limit+1, after)
	if err != nil {
		return ParticipantPage{}, fmt.Errorf("list participants: %w", err)
	}
	page := ParticipantPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		page.Next = &ParticipantPosition{JoinedAt: last.JoinedAt, UserID: last.UserID}
	}
	ids := make([]string, len(page.Items))
	for i, p := range page.Items {
		ids[i] = p.UserID
	}
	names, err := s.profiles.Names(ctx, ids)
	if err != nil {
		return ParticipantPage{}, fmt.Errorf("list participants: %w", err)
	}
	for i := range page.Items {
		page.Items[i].Name = names[page.Items[i].UserID]
	}
	return page, nil
}

// Get returns challenge id as actor sees it. A missing or deleted
// challenge, and a private one the actor may not see, all answer the same
// 404 challenge_not_found: nothing tells that a private challenge exists.
// An admin sees every challenge not deleted (ReaderRole); the role and the
// rights shown remain those of their membership.
func (s *Service) Get(ctx context.Context, actor auth.Actor, id string) (Detail, error) {
	now := s.clock.Now()
	c, m, err := s.repo.Get(ctx, id, actor.ID, now)
	if errors.Is(err, errChallengeNotFound) {
		return Detail{}, apperr.New(apperr.CodeChallengeNotFound, nil)
	}
	if err != nil {
		return Detail{}, fmt.Errorf("get challenge: %w", err)
	}
	// The admin permission only opens the reading (ReaderRole): the role
	// shown, and the rights derived from it, stay those of the membership.
	role := RoleOf(m)
	if err := RequireView(ReaderRole(m, actor.Admin), c.Visibility); err != nil {
		return Detail{}, err
	}
	// Only a visible challenge reaches the next reads.
	if c.Categories, err = s.repo.Categories(ctx, c.ID); err != nil {
		return Detail{}, fmt.Errorf("get challenge: %w", err)
	}
	loc, err := location(c.Timezone)
	if err != nil {
		return Detail{}, fmt.Errorf("challenge time zone %q: %w", c.Timezone, err)
	}

	w := scoring.Window{Start: c.StartsAt, End: c.EndsAt, Location: loc, Now: now}
	deadline := w.RegistrationDeadline()
	week, weekEnd := w.CurrentWeek()
	count := w.Weeks()
	weeks := make([]Week, count)
	for n := 1; n <= count; n++ {
		start, end := w.WeekBounds(n)
		weeks[n-1] = Week{Number: n, StartsAt: start, EndsAt: end}
	}
	return Detail{
		Challenge:            c,
		Role:                 role,
		Participant:          m.Participant,
		Status:               StatusAt(now, c.StartsAt, c.EndsAt),
		CanJoin:              CanJoin(m, c.Visibility, now, deadline),
		RegistrationDeadline: deadline,
		CurrentWeek:          week,
		WeekEndsAt:           weekEnd,
		WeekCount:            count,
		Weeks:                weeks,
	}, nil
}

// UpdateText changes the title and the description of challenge id, the
// only fields its creators may edit, at any time. Checks, in this order: the
// challenge must be visible (404, like the detail), the actor a creator
// (403), no frozen field sent (422 field_not_editable on frozen, the names
// of the contract's fields), the texts valid (400). It returns the
// challenge after the change, as the actor sees it.
func (s *Service) UpdateText(ctx context.Context, actorID, id string, frozen []string, changes TextChanges) (Detail, error) {
	c, m, err := s.repo.Get(ctx, id, actorID, s.clock.Now())
	if errors.Is(err, errChallengeNotFound) {
		return Detail{}, apperr.New(apperr.CodeChallengeNotFound, nil)
	}
	if err != nil {
		return Detail{}, fmt.Errorf("update challenge: %w", err)
	}
	role := RoleOf(m)
	if err := RequireView(role, c.Visibility); err != nil {
		return Detail{}, err
	}
	if err := RequireEdit(role); err != nil {
		return Detail{}, err
	}
	if len(frozen) > 0 {
		e := apperr.New(apperr.CodeFieldNotEditable, nil)
		sorted := slices.Sorted(slices.Values(frozen))
		e.Params = map[string]string{apperr.FieldsParam: strings.Join(sorted, ",")}
		return Detail{}, e
	}
	changes, err = ValidateText(changes)
	if err != nil {
		return Detail{}, err
	}
	err = s.repo.UpdateText(ctx, id, changes)
	if errors.Is(err, errChallengeNotFound) {
		// Deleted meanwhile: gone for everyone.
		return Detail{}, apperr.New(apperr.CodeChallengeNotFound, nil)
	}
	if err != nil {
		return Detail{}, fmt.Errorf("update challenge: %w", err)
	}
	return s.Get(ctx, auth.Actor{ID: actorID}, id)
}

// Join makes actorID an active participant of challenge id, joined at the
// clock's now once the challenge is locked. The join rule (JoinRefusal:
// visibility, already in, abandoned, end of week 1), then the complete
// profile (422 profile_incomplete), are checked on the challenge locked by
// the repository, in the transaction that writes the participation. The
// profile is read before the transaction: reading it inside would take a
// second pool connection while the lock is held, and concurrent joins could
// exhaust the pool. It returns the challenge as the actor now sees it.
func (s *Service) Join(ctx context.Context, actorID, id string) (Detail, error) {
	complete, err := s.profiles.ProfileComplete(ctx, actorID)
	if err != nil {
		return Detail{}, fmt.Errorf("check profile: %w", err)
	}
	// The invitations are judged before the lock: one expires at the end of
	// week 1, which decide checks again once locked.
	err = s.repo.Join(ctx, id, actorID, s.clock.Now(), func(c Challenge, m Membership) (time.Time, error) {
		// Read once the lock is held: a join waiting for it is judged at
		// the instant it may write, never before the end of week 1.
		now := s.clock.Now()
		// The time zone was validated at creation: it cannot fail here.
		loc, err := location(c.Timezone)
		if err != nil {
			return time.Time{}, fmt.Errorf("challenge time zone %q: %w", c.Timezone, err)
		}
		w := scoring.Window{Start: c.StartsAt, End: c.EndsAt, Location: loc, Now: now}
		if err := JoinRefusal(m, c.Visibility, now, w.RegistrationDeadline()); err != nil {
			return time.Time{}, err
		}
		if !complete {
			return time.Time{}, apperr.New(apperr.CodeProfileIncomplete, nil)
		}
		return now, nil
	})
	switch {
	case errors.Is(err, errChallengeNotFound):
		return Detail{}, apperr.New(apperr.CodeChallengeNotFound, nil)
	case errors.Is(err, errAlreadyParticipant):
		return Detail{}, apperr.New(apperr.CodeAlreadyParticipant, nil)
	case errors.Is(err, errAccountDeleted):
		return Detail{}, apperr.New(apperr.CodeAccountDeleted, nil)
	case err != nil:
		return Detail{}, err
	}
	return s.Get(ctx, auth.Actor{ID: actorID}, id)
}

// InvitationDeadline returns the end of week 1 of challenge id, when an
// invitation to it expires, once actorID may invite to it (InviteRefusal:
// visible, creator or participant allowed by members_can_invite, before the
// end of week 1). A missing or deleted challenge answers 404
// challenge_not_found. It writes nothing: module invitation stores the
// invitation.
func (s *Service) InvitationDeadline(ctx context.Context, actorID, id string) (time.Time, error) {
	now := s.clock.Now()
	c, m, err := s.repo.Get(ctx, id, actorID, now)
	if errors.Is(err, errChallengeNotFound) {
		return time.Time{}, apperr.New(apperr.CodeChallengeNotFound, nil)
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("invitation deadline: %w", err)
	}
	// Checked before the time zone, like every rule: a hidden challenge
	// reveals nothing.
	if err := RequireView(RoleOf(m), c.Visibility); err != nil {
		return time.Time{}, err
	}
	loc, err := location(c.Timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("challenge time zone %q: %w", c.Timezone, err)
	}
	deadline := scoring.Window{Start: c.StartsAt, End: c.EndsAt, Location: loc, Now: now}.RegistrationDeadline()
	if err := InviteRefusal(m, c.Visibility, c.MembersCanInvite, now, deadline); err != nil {
		return time.Time{}, err
	}
	return deadline, nil
}

// Abandon ends the participation of actorID in challenge id, for good: the
// participant is eliminated from every category and shown as abandoned. A
// creator keeps the creator rights but no longer competes. Abandoning again
// changes nothing (200, idempotent); a finished challenge answers 422
// challenge_ended. The rule (AbandonRefusal) is checked on the challenge
// locked by the repository, at the clock's now once locked. It returns the
// challenge as the actor now sees it.
func (s *Service) Abandon(ctx context.Context, actorID, id string) (Detail, error) {
	err := s.repo.Abandon(ctx, id, actorID, s.clock.Now(), func(c Challenge, m Membership) (bool, time.Time, error) {
		now := s.clock.Now()
		noop, err := AbandonRefusal(m, c.Visibility, now, c.EndsAt)
		return noop, now, err
	})
	if errors.Is(err, errChallengeNotFound) {
		return Detail{}, apperr.New(apperr.CodeChallengeNotFound, nil)
	}
	if err != nil {
		return Detail{}, err
	}
	return s.Get(ctx, auth.Actor{ID: actorID}, id)
}

// AbandonAll makes userID abandon every challenge not finished where they
// are an active participant, for the deletion of their account
// (DeletionAbandons): each under the lock of its challenge, like an abandon,
// at the clock's now once locked. It first refuses (422 last_creator,
// DeletionRefusal) while userID is the last creator with an account of a
// challenge not finished. Called in the transaction of the account deletion
// (ctx carries it), so everything commits or nothing does. It checks no
// actor: never expose it on a route, only user.Service.Delete calls it.
func (s *Service) AbandonAll(ctx context.Context, userID string) error {
	now := s.clock.Now()
	last, err := s.repo.LastCreatorChallenges(ctx, userID, now)
	if err != nil {
		return fmt.Errorf("abandon all: %w", err)
	}
	if err := DeletionRefusal(last); err != nil {
		return err
	}
	ids, err := s.repo.ActiveParticipations(ctx, userID, now)
	if err != nil {
		return fmt.Errorf("abandon all: %w", err)
	}
	for _, id := range ids {
		err := s.repo.Abandon(ctx, id, userID, s.clock.Now(), func(c Challenge, m Membership) (bool, time.Time, error) {
			now := s.clock.Now()
			return !DeletionAbandons(m, now, c.EndsAt), now, nil
		})
		// Deleted meanwhile: nothing left to abandon there.
		if err != nil && !errors.Is(err, errChallengeNotFound) {
			return fmt.Errorf("abandon all: %w", err)
		}
	}
	return nil
}

// EraseUser deletes every row of userID in the challenges, finished ones
// included, for the deletion of their account (README §6): participations,
// creator roles, eliminations and entries in the frozen results. The
// challenges they created stay. Called in the transaction of the account
// deletion, after AbandonAll. It checks no actor: never expose it on a
// route, only user.Service.Delete calls it.
func (s *Service) EraseUser(ctx context.Context, userID string) error {
	if err := s.repo.EraseUser(ctx, userID); err != nil {
		return fmt.Errorf("erase user: %w", err)
	}
	return nil
}

// Memberships returns the challenges, not deleted, that userID joined, with
// their membership: where an activity of theirs may count, read in the
// transaction of ctx, if any. It checks no actor: never expose it on a
// route (activity.Service reads the actor's own, to tell where an activity
// counts and whether it is locked).
func (s *Service) Memberships(ctx context.Context, userID string) ([]UserChallenge, error) {
	ms, err := s.repo.Memberships(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("memberships: %w", err)
	}
	return ms, nil
}

// ActivitiesScope returns challenge id with the membership of userID in it
// and its distance factors, for actor to list the activities of userID
// there, once the rule (ActivitiesRefusal) allows it. A missing or deleted
// challenge answers 404 challenge_not_found.
func (s *Service) ActivitiesScope(ctx context.Context, actor auth.Actor, id, userID string) (UserChallenge, error) {
	now := s.clock.Now()
	c, m, err := s.repo.Get(ctx, id, actor.ID, now)
	target := m
	if err == nil && userID != actor.ID {
		_, target, err = s.repo.Get(ctx, id, userID, now)
	}
	if errors.Is(err, errChallengeNotFound) {
		return UserChallenge{}, apperr.New(apperr.CodeChallengeNotFound, nil)
	}
	if err != nil {
		return UserChallenge{}, fmt.Errorf("activities scope: %w", err)
	}
	if err := ActivitiesRefusal(m, actor.Admin, target, c.Visibility); err != nil {
		return UserChallenge{}, err
	}
	out, err := s.userChallenge(ctx, c)
	if err != nil {
		return UserChallenge{}, fmt.Errorf("activities scope: %w", err)
	}
	out.Membership = target
	return out, nil
}

// AllActivitiesScope returns challenge id with its distance factors, no
// membership set, and the membership of each of its participants, abandoned
// ones included and deleted accounts left out, by user id, for actor to
// list the activities of all the participants there, once the rule
// (AllActivitiesRefusal) allows it. A missing or deleted challenge answers
// 404 challenge_not_found.
func (s *Service) AllActivitiesScope(ctx context.Context, actor auth.Actor, id string) (UserChallenge, map[string]Membership, error) {
	c, m, err := s.repo.Get(ctx, id, actor.ID, s.clock.Now())
	if errors.Is(err, errChallengeNotFound) {
		return UserChallenge{}, nil, apperr.New(apperr.CodeChallengeNotFound, nil)
	}
	if err != nil {
		return UserChallenge{}, nil, fmt.Errorf("all activities scope: %w", err)
	}
	if err := AllActivitiesRefusal(m, actor.Admin, c.Visibility); err != nil {
		return UserChallenge{}, nil, err
	}
	out, err := s.userChallenge(ctx, c)
	if err != nil {
		return UserChallenge{}, nil, fmt.Errorf("all activities scope: %w", err)
	}
	members, err := s.repo.ParticipantMemberships(ctx, c.ID)
	if err != nil {
		return UserChallenge{}, nil, fmt.Errorf("all activities scope: %w", err)
	}
	return out, members, nil
}

// userChallenge returns c as a UserChallenge with the factors of its
// cumulative distance category, if any, and no membership set.
func (s *Service) userChallenge(ctx context.Context, c Challenge) (UserChallenge, error) {
	cats, err := s.repo.Categories(ctx, c.ID)
	if err != nil {
		return UserChallenge{}, err
	}
	out := UserChallenge{ID: c.ID, Title: c.Title, Timezone: c.Timezone, StartsAt: c.StartsAt, EndsAt: c.EndsAt}
	for _, cat := range cats {
		if cat.Type == CategoryCumulativeDistance {
			out.Factors = cat.Params.Factors
			break
		}
	}
	return out, nil
}

// Exclude makes targetID abandon challenge id on behalf of actorID, one of
// its creators: the target is eliminated from every category for good and
// shown as abandoned, with no mention of the exclusion. The rule
// (ExcludeRefusal: visible, creator, target not a creator, target in, not
// finished) is checked on the challenge locked by the repository, at the
// clock's now once locked. Excluding someone already out changes nothing.
func (s *Service) Exclude(ctx context.Context, actorID, id, targetID string) error {
	err := s.repo.Exclude(ctx, id, actorID, targetID, s.clock.Now(), func(c Challenge, actor, target Membership) (bool, time.Time, error) {
		now := s.clock.Now()
		noop, err := ExcludeRefusal(actor, target, c.Visibility, now, c.EndsAt)
		return noop, now, err
	})
	if errors.Is(err, errChallengeNotFound) {
		return apperr.New(apperr.CodeChallengeNotFound, nil)
	}
	return err
}

// AddCreator makes targetID a creator of challenge id on behalf of actorID,
// one of its creators: the new creator gets every creator right. The rule
// (AddCreatorRefusal: visible, creator, target an active participant, not
// finished) is checked on the challenge locked by the repository, at the
// clock's now once locked. Adding an existing creator changes nothing. It
// returns the challenge as the actor now sees it.
func (s *Service) AddCreator(ctx context.Context, actorID, id, targetID string) (Detail, error) {
	err := s.repo.AddCreator(ctx, id, actorID, targetID, s.clock.Now(), func(c Challenge, actor, target Membership) (bool, error) {
		return AddCreatorRefusal(actor, target, c.Visibility, s.clock.Now(), c.EndsAt)
	})
	if errors.Is(err, errChallengeNotFound) {
		return Detail{}, apperr.New(apperr.CodeChallengeNotFound, nil)
	}
	if err != nil {
		return Detail{}, err
	}
	return s.Get(ctx, auth.Actor{ID: actorID}, id)
}

// Delete soft-deletes challenge id on behalf of actorID, one of its
// creators, at any time: from then on it is not found for everyone, its
// creators included, and every write on it is refused. The rule
// (DeleteRefusal) is checked on the challenge locked by the repository; the
// deletion is stamped with the clock's now once locked. Deleting a deleted
// challenge answers 404 like a missing one.
func (s *Service) Delete(ctx context.Context, actorID, id string) error {
	err := s.repo.Delete(ctx, id, actorID, s.clock.Now(), func(c Challenge, m Membership) (time.Time, error) {
		if err := DeleteRefusal(m, c.Visibility); err != nil {
			return time.Time{}, err
		}
		return s.clock.Now(), nil
	})
	if errors.Is(err, errChallengeNotFound) {
		return apperr.New(apperr.CodeChallengeNotFound, nil)
	}
	return err
}
