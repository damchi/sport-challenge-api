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
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// repository is what the service needs from the storage.
type repository interface {
	Create(ctx context.Context, c Challenge, joinedAt time.Time) (Challenge, error)
	List(ctx context.Context, q ListQuery) ([]Summary, error)
	// Get, Join, Abandon, Exclude, AddCreator and Delete judge the
	// invitations at now (Membership.Invited).
	Get(ctx context.Context, id, actorID string, now time.Time) (Challenge, Membership, error)
	Categories(ctx context.Context, id string) ([]Category, error)
	UpdateText(ctx context.Context, id string, changes TextChanges) error
	Join(ctx context.Context, id, actorID string, now time.Time, decide func(Challenge, Membership) (time.Time, error)) error
	Abandon(ctx context.Context, id, actorID string, now time.Time, decide func(Challenge, Membership) (bool, time.Time, error)) error
	Exclude(ctx context.Context, id, actorID, targetID string, now time.Time, decide func(c Challenge, actor, target Membership) (bool, time.Time, error)) error
	AddCreator(ctx context.Context, id, actorID, targetID string, now time.Time, decide func(c Challenge, actor, target Membership) (bool, error)) error
	Delete(ctx context.Context, id, actorID string, now time.Time, decide func(Challenge, Membership) (time.Time, error)) error
	Participants(ctx context.Context, id string, limit int, after *ParticipantPosition) ([]Participant, error)
	ActiveParticipations(ctx context.Context, userID string, now time.Time) ([]string, error)
	LastCreatorChallenges(ctx context.Context, userID string, now time.Time) ([]string, error)
	Memberships(ctx context.Context, userID string) ([]UserChallenge, error)
}

// Profiles tells whether a user's profile is complete, and gives the pseudos
// of users, none for an anonymized account (module user).
type Profiles interface {
	ProfileComplete(ctx context.Context, userID string) (bool, error)
	Pseudos(ctx context.Context, userIDs []string) (map[string]string, error)
}

// Catalogue gives the suggested factor of every sport with a distance
// (module sport).
type Catalogue interface {
	SuggestedFactors(ctx context.Context) (map[string]float64, error)
}

// Service holds the use cases of the challenges.
type Service struct {
	repo      repository
	profiles  Profiles
	catalogue Catalogue
	clock     clock.Clock
}

// NewService returns a Service.
func NewService(repo repository, profiles Profiles, catalogue Catalogue, c clock.Clock) *Service {
	return &Service{repo: repo, profiles: profiles, catalogue: catalogue, clock: c}
}

// Create creates the challenge described by draft, with actorID as its
// creator and first participant. The actor needs a complete profile (422
// profile_incomplete). The distance factors are the catalogue's suggestions,
// replaced by those of the draft, then frozen in the category.
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

	now := s.clock.Now()
	window, err := Validate(draft, now, distanceSports)
	if err != nil {
		return Challenge{}, err
	}

	factors := maps.Clone(suggested)
	maps.Copy(factors, draft.DistanceFactors)
	// Running is the reference of the distance category, whatever the
	// catalogue says (README §2.4).
	factors[referenceSport] = 1

	// An empty description is no description, as on edit.
	description := draft.Description
	if description != nil && *description == "" {
		description = nil
	}
	c, err := s.repo.Create(ctx, Challenge{
		Title:            strings.TrimSpace(draft.Title),
		Description:      description,
		Visibility:       draft.Visibility,
		MembersCanInvite: draft.MembersCanInvite,
		Timezone:         draft.Timezone,
		StartsAt:         window.StartsAt,
		EndsAt:           window.EndsAt,
		CreatedBy:        actorID,
		Categories: []Category{
			{Type: CategoryCumulativeTime, Position: 1, Params: Params{}},
			{Type: CategoryCumulativeDistance, Position: 2, Params: Params{Factors: factors}},
			{Type: CategoryRunElimination, Position: 3, Params: Params{Sport: referenceSport, StepMeters: draft.EliminationStepMeters}},
		},
	}, now)
	if errors.Is(err, errAccountDeleted) {
		return Challenge{}, apperr.New(apperr.CodeAccountDeleted, nil)
	}
	if err != nil {
		return Challenge{}, fmt.Errorf("store challenge: %w", err)
	}
	return c, nil
}

// List returns a page of challenges for actorID: public ones, or those the
// actor created or joined. The status filter and the status of each
// challenge use the same instant of the clock. The visibility rules are
// applied by the query itself, never by filtering a loaded list.
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
	for i := range items {
		it := &items[i]
		m := Membership{Creator: it.Creator, Invited: it.Invited}
		if it.MyStatus != nil {
			m.Participant = ParticipantStatus(*it.MyStatus)
		}
		it.Role = RoleOf(m)
		it.Status = StatusAt(now, it.StartsAt, it.EndsAt)
	}
	page := Page{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		page.Next = &Position{StartsAt: last.StartsAt, ID: last.ID}
	}
	return page, nil
}

// Participants returns a page of the participants of challenge id, visible
// to whoever sees the challenge (ParticipantsRefusal): anyone else gets the
// same 404 challenge_not_found as for a missing challenge. An anonymized
// account has no pseudo.
func (s *Service) Participants(ctx context.Context, actorID, id string, limit int, after *ParticipantPosition) (ParticipantPage, error) {
	if limit < 1 {
		return ParticipantPage{}, apperr.InvalidFields(nil, "limit")
	}
	c, m, err := s.repo.Get(ctx, id, actorID, s.clock.Now())
	if errors.Is(err, errChallengeNotFound) {
		return ParticipantPage{}, apperr.New(apperr.CodeChallengeNotFound, nil)
	}
	if err != nil {
		return ParticipantPage{}, fmt.Errorf("list participants: %w", err)
	}
	if err := ParticipantsRefusal(m, c.Visibility); err != nil {
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
	pseudos, err := s.profiles.Pseudos(ctx, ids)
	if err != nil {
		return ParticipantPage{}, fmt.Errorf("list participants: %w", err)
	}
	for i := range page.Items {
		if pseudo, ok := pseudos[page.Items[i].UserID]; ok {
			page.Items[i].Pseudo = &pseudo
		}
	}
	return page, nil
}

// Get returns challenge id as actorID sees it. A missing or deleted
// challenge, and a private one the actor may not see, all answer the same
// 404 challenge_not_found: nothing tells that a private challenge exists.
func (s *Service) Get(ctx context.Context, actorID, id string) (Detail, error) {
	now := s.clock.Now()
	c, m, err := s.repo.Get(ctx, id, actorID, now)
	if errors.Is(err, errChallengeNotFound) {
		return Detail{}, apperr.New(apperr.CodeChallengeNotFound, nil)
	}
	if err != nil {
		return Detail{}, fmt.Errorf("get challenge: %w", err)
	}
	role := RoleOf(m)
	if err := RequireView(role, c.Visibility); err != nil {
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
	return Detail{
		Challenge:            c,
		Role:                 role,
		Participant:          m.Participant,
		Status:               StatusAt(now, c.StartsAt, c.EndsAt),
		CanJoin:              CanJoin(m, c.Visibility, now, deadline),
		RegistrationDeadline: deadline,
		CurrentWeek:          week,
		WeekEndsAt:           weekEnd,
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
	return s.Get(ctx, actorID, id)
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
	return s.Get(ctx, actorID, id)
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
	return s.Get(ctx, actorID, id)
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

// ActivitiesScope returns challenge id with the membership of userID in it,
// for actorID to list the activities of userID there, once the rule
// (ActivitiesRefusal) allows it. A missing or deleted challenge answers 404
// challenge_not_found.
func (s *Service) ActivitiesScope(ctx context.Context, actorID, id, userID string) (UserChallenge, error) {
	now := s.clock.Now()
	c, actor, err := s.repo.Get(ctx, id, actorID, now)
	target := actor
	if err == nil && userID != actorID {
		_, target, err = s.repo.Get(ctx, id, userID, now)
	}
	if errors.Is(err, errChallengeNotFound) {
		return UserChallenge{}, apperr.New(apperr.CodeChallengeNotFound, nil)
	}
	if err != nil {
		return UserChallenge{}, fmt.Errorf("activities scope: %w", err)
	}
	if err := ActivitiesRefusal(actor, target, c.Visibility); err != nil {
		return UserChallenge{}, err
	}
	return UserChallenge{ID: c.ID, Timezone: c.Timezone, StartsAt: c.StartsAt, EndsAt: c.EndsAt, Membership: target}, nil
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
	return s.Get(ctx, actorID, id)
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
