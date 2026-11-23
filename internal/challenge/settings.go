package challenge

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// SettingsChanges are the settings of a challenge an admin changes (README
// §2.12), those frozen at creation included; nil leaves a field as is.
type SettingsChanges struct {
	Title *string
	// Description: an empty one removes it.
	Description      *string
	Visibility       *string
	MembersCanInvite *bool
	Timezone         *string
	// StartDate is the first local day, as YYYY-MM-DD, in the new time zone
	// when it changes too.
	StartDate *string
	Weeks     *int
	// Categories replaces the optional categories: those it leaves out are
	// removed. The cumulative time is always there.
	Categories     *CategoryChoice
	ReferenceSport *string
	// DistanceFactors replaces the factors it names, the others kept (or
	// suggested again for a new reference sport).
	DistanceFactors map[string]float64
}

// CategoryChoice is the choice of the optional categories, as at creation
// (Draft).
type CategoryChoice struct {
	Distance    bool
	Elimination *EliminationDraft
	Points      *PointsDraft
	Elevation   bool
}

// SettingsChange is what AdminUpdate changed.
type SettingsChange struct {
	// Old and New hold the settings changed only, by their contract name,
	// for the journal of the admin changes.
	Old map[string]any
	New map[string]any
	// Deadline is the new end of week 1 when the change moved it (first
	// day or time zone), nil otherwise: the invitation links expire then.
	Deadline *time.Time
}

// errReferenceChanged means the reference sport changed between the read of
// its catalogue entry and the lock of the challenge: only an admin changes
// it, under the week lock, so it cannot happen.
var errReferenceChanged = errors.New("reference sport changed meanwhile")

// AdminUpdate changes the settings of challenge id as actor, an admin
// (AdminRefusal, 404 not_found otherwise), those frozen at creation
// included, in progress or finished: the challenge with changes applied is
// checked as a whole by the rules of the creation, its first day allowed in
// the past (ValidateSettings). A new reference sport starts again from the
// suggested factors brought back to it; otherwise the factors of the
// challenge stay; changes.DistanceFactors then replaces those it names.
// The categories are written again (categoriesOf), the cumulative time
// always among them. A missing or deleted challenge answers 404
// challenge_not_found.
//
// It writes in the transaction ctx carries, under the lock of the
// challenge, and judges nothing: the caller (admin.ChallengeCorrections)
// records the change and recomputes the challenge in the same transaction.
func (s *Service) AdminUpdate(ctx context.Context, actor auth.Actor, id string, changes SettingsChanges) (SettingsChange, error) {
	if err := AdminRefusal(actor.Admin); err != nil {
		return SettingsChange{}, err
	}
	current, _, err := s.repo.Get(ctx, id, actor.ID, s.clock.Now())
	if errors.Is(err, errChallengeNotFound) {
		return SettingsChange{}, apperr.New(apperr.CodeChallengeNotFound, nil)
	}
	if err != nil {
		return SettingsChange{}, fmt.Errorf("update settings: %w", err)
	}
	reference := current.ReferenceSport
	if changes.ReferenceSport != nil {
		reference = *changes.ReferenceSport
	}
	// The catalogue is read before the lock, as at creation.
	suggested, err := s.catalogue.SuggestedFactors(ctx)
	if err != nil {
		return SettingsChange{}, fmt.Errorf("read catalogue: %w", err)
	}
	distanceSports := make(map[string]bool, len(suggested))
	for code := range suggested {
		distanceSports[code] = true
	}
	referenceMode, _, err := s.catalogue.DistanceMode(ctx, reference)
	if err != nil {
		return SettingsChange{}, fmt.Errorf("read catalogue: %w", err)
	}

	var out SettingsChange
	err = s.repo.UpdateSettings(ctx, id, actor.ID, s.clock.Now(), func(c Challenge, cats []Category) (Challenge, error) {
		before, err := draftOf(c, cats)
		if err != nil {
			return Challenge{}, err
		}
		draft := changes.apply(before)
		if draft.reference() != reference {
			return Challenge{}, errReferenceChanged
		}
		window, err := ValidateSettings(draft, s.clock.Now(), distanceSports, referenceMode)
		if err != nil {
			return Challenge{}, err
		}
		var factors map[string]float64
		if draft.usesFactors() {
			factors = maps.Clone(factorsOf(cats))
			if factors == nil || reference != c.ReferenceSport {
				factors = referenceFactors(suggested, reference)
			}
			maps.Copy(factors, draft.DistanceFactors)
		}
		next := c
		next.Title = strings.TrimSpace(draft.Title)
		next.Description = noEmpty(draft.Description)
		next.Visibility = draft.Visibility
		next.MembersCanInvite = draft.MembersCanInvite
		next.Timezone = draft.Timezone
		next.ReferenceSport = reference
		next.StartsAt, next.EndsAt = window.StartsAt, window.EndsAt
		next.Categories = categoriesOf(draft, factors)

		after, err := draftOf(next, next.Categories)
		if err != nil {
			return Challenge{}, err
		}
		out.Old, out.New = changedSettings(settingsOf(before, cats), settingsOf(after, next.Categories))
		oldDeadline, err := deadlineOf(c)
		if err != nil {
			return Challenge{}, err
		}
		newDeadline, err := deadlineOf(next)
		if err != nil {
			return Challenge{}, err
		}
		if !newDeadline.Equal(oldDeadline) {
			out.Deadline = &newDeadline
		}
		return next, nil
	})
	if errors.Is(err, errChallengeNotFound) {
		return SettingsChange{}, apperr.New(apperr.CodeChallengeNotFound, nil)
	}
	if err != nil {
		return SettingsChange{}, err
	}
	return out, nil
}

// apply returns d with changes made.
func (changes SettingsChanges) apply(d Draft) Draft {
	set := func(field *string, value *string) {
		if value != nil {
			*field = *value
		}
	}
	set(&d.Title, changes.Title)
	set(&d.Visibility, changes.Visibility)
	set(&d.Timezone, changes.Timezone)
	set(&d.StartDate, changes.StartDate)
	if changes.Description != nil {
		d.Description = changes.Description
	}
	if changes.MembersCanInvite != nil {
		d.MembersCanInvite = *changes.MembersCanInvite
	}
	if changes.Weeks != nil {
		d.Weeks = *changes.Weeks
	}
	if changes.ReferenceSport != nil {
		d.ReferenceSport = changes.ReferenceSport
	}
	if cc := changes.Categories; cc != nil {
		d.Distance, d.Elimination, d.Points, d.Elevation = cc.Distance, cc.Elimination, cc.Points, cc.Elevation
	}
	d.DistanceFactors = changes.DistanceFactors
	return d
}

// draftOf describes c, with its categories cats, as a draft: its first local
// day and its number of weeks in its time zone, its categories with their
// settings; no distance factors (they are those of cats).
func draftOf(c Challenge, cats []Category) (Draft, error) {
	loc, err := location(c.Timezone)
	if err != nil {
		return Draft{}, fmt.Errorf("challenge time zone %q: %w", c.Timezone, err)
	}
	reference := c.ReferenceSport
	d := Draft{
		Title:            c.Title,
		Description:      c.Description,
		Visibility:       c.Visibility,
		MembersCanInvite: c.MembersCanInvite,
		Timezone:         c.Timezone,
		StartDate:        c.StartsAt.In(loc).Format(startDateLayout),
		Weeks:            scoring.Window{Start: c.StartsAt, End: c.EndsAt, Location: loc}.Weeks(),
		ReferenceSport:   &reference,
	}
	for _, cat := range cats {
		switch cat.Type {
		case CategoryCumulativeDistance:
			d.Distance = true
		case CategoryElimination:
			d.Elimination = &EliminationDraft{StepMeters: cat.Params.StepMeters}
		case CategoryPoints:
			minimum := cat.Params.MinSessionSeconds / 60
			d.Points = &PointsDraft{Criteria: cat.Params.Criteria, Podium: cat.Params.Podium, MinSessionMinutes: &minimum}
		case CategoryElevation:
			d.Elevation = true
		}
	}
	return d, nil
}

// factorsOf returns the distance factors frozen in cats: those of the
// cumulative distance, else those of the points; nil when no category has
// any.
func factorsOf(cats []Category) map[string]float64 {
	var out map[string]float64
	for _, cat := range cats {
		if cat.Params.Factors == nil {
			continue
		}
		if cat.Type == CategoryCumulativeDistance {
			return cat.Params.Factors
		}
		out = cat.Params.Factors
	}
	return out
}

// deadlineOf returns the end of week 1 of c.
func deadlineOf(c Challenge) (time.Time, error) {
	loc, err := location(c.Timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("challenge time zone %q: %w", c.Timezone, err)
	}
	return scoring.Window{Start: c.StartsAt, End: c.EndsAt, Location: loc}.RegistrationDeadline(), nil
}

// settingsOf gives the settings of draft d, its categories cats, by their
// contract name, as the journal keeps them; the categories with their
// settings by type.
func settingsOf(d Draft, cats []Category) map[string]any {
	var description any
	if d.Description != nil {
		description = *d.Description
	}
	categories := make(map[string]Params, len(cats))
	for _, cat := range cats {
		categories[cat.Type] = cat.Params
	}
	return map[string]any{
		"title":              d.Title,
		"description":        description,
		"visibility":         d.Visibility,
		"members_can_invite": d.MembersCanInvite,
		"timezone":           d.Timezone,
		"start_date":         d.StartDate,
		"weeks":              d.Weeks,
		"reference_sport":    d.reference(),
		"categories":         categories,
	}
}

// changedSettings keeps, of before and after, the settings that differ.
func changedSettings(before, after map[string]any) (map[string]any, map[string]any) {
	oldValue, newValue := map[string]any{}, map[string]any{}
	for name, value := range after {
		if !reflect.DeepEqual(before[name], value) {
			oldValue[name], newValue[name] = before[name], value
		}
	}
	return oldValue, newValue
}
