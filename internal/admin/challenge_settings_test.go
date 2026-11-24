package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/challenge"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/buildinfo"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

// fakeSettings is module challenge and module invitation for the
// corrections of a challenge: it logs what it is asked.
type fakeSettings struct {
	log          *correctionLog
	change       challenge.SettingsChange
	err          error
	changes      challenge.SettingsChanges
	rescheduled  *time.Time
	rescheduleOf string
	// deletedAt is the instant AdminDelete and AdminRestore answer.
	deletedAt time.Time
}

func (f *fakeSettings) AdminDelete(_ context.Context, actor auth.Actor, id string) (time.Time, error) {
	f.log.add("delete " + id)
	if !actor.Admin {
		return time.Time{}, errors.New("deleted for a non-admin")
	}
	return f.deletedAt, f.err
}

func (f *fakeSettings) AdminRestore(_ context.Context, actor auth.Actor, id string) (time.Time, error) {
	f.log.add("restore " + id)
	if !actor.Admin {
		return time.Time{}, errors.New("restored for a non-admin")
	}
	return f.deletedAt, f.err
}

func (f *fakeSettings) AdminUpdate(_ context.Context, actor auth.Actor, id string, changes challenge.SettingsChanges) (challenge.SettingsChange, error) {
	f.log.add("update " + id)
	if !actor.Admin {
		return challenge.SettingsChange{}, errors.New("updated for a non-admin")
	}
	f.changes = changes
	return f.change, f.err
}

func (f *fakeSettings) Reschedule(_ context.Context, id string, at time.Time) error {
	f.log.add("reschedule " + id)
	f.rescheduleOf, f.rescheduled = id, &at
	return nil
}

func TestChallengeCorrectionsUpdateSettings(t *testing.T) {
	deadline := time.Date(2026, 2, 9, 5, 0, 0, 0, time.UTC)
	weeks := 6
	tests := []struct {
		name      string
		change    challenge.SettingsChange
		wantSteps []string
	}{
		{
			name:   "week 1 kept: the links are left as they are",
			change: challenge.SettingsChange{Old: map[string]any{"weeks": 4}, New: map[string]any{"weeks": 6}},
			wantSteps: []string{
				"lock", "rank " + challengeID, "eliminations " + challengeID, "update " + challengeID, "journal",
				"recompute " + challengeID, "rank " + challengeID, "eliminations " + challengeID,
			},
		},
		{
			name:   "week 1 moved: the links follow, in the transaction",
			change: challenge.SettingsChange{Old: map[string]any{"start_date": "2026-02-02"}, New: map[string]any{"start_date": "2026-01-26"}, Deadline: &deadline},
			wantSteps: []string{
				"lock", "rank " + challengeID, "eliminations " + challengeID, "update " + challengeID, "reschedule " + challengeID, "journal",
				"recompute " + challengeID, "rank " + challengeID, "eliminations " + challengeID,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := &correctionLog{}
			corrections, journal := newCorrections(log)
			settings := &fakeSettings{log: log, change: tt.change}
			s := NewChallengeCorrections(corrections, settings, settings)
			previews, err := s.UpdateSettings(t.Context(), admin, challengeID, challenge.SettingsChanges{Weeks: &weeks}, "Durée mal saisie", false)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(log.steps, tt.wantSteps) {
				t.Fatalf("steps %v, want %v", log.steps, tt.wantSteps)
			}
			if len(previews) != 1 || previews[0].ChallengeID != challengeID {
				t.Fatalf("previews %+v, want the challenge", previews)
			}
			if settings.changes.Weeks == nil || *settings.changes.Weeks != 6 {
				t.Fatalf("changes %+v, want those given", settings.changes)
			}
			if tt.change.Deadline != nil && (settings.rescheduleOf != challengeID || !settings.rescheduled.Equal(deadline)) {
				t.Fatalf("rescheduled %s to %v, want %s to %v", settings.rescheduleOf, settings.rescheduled, challengeID, deadline)
			}
			a := journal.records[0]
			oldValue, _ := json.Marshal(tt.change.Old)
			newValue, _ := json.Marshal(tt.change.New)
			if a.Action != ActionChallengeUpdate || a.TargetType != TargetChallenge || a.TargetID != challengeID ||
				!reflect.DeepEqual(a.ChallengeIDs, []string{challengeID}) || string(a.OldValue) != string(oldValue) ||
				string(a.NewValue) != string(newValue) || a.Reason != "Durée mal saisie" || a.AdminID != annID {
				t.Fatalf("journal line %+v", a)
			}
		})
	}
}

// TestChallengeCorrectionsUpdateSettingsRefusals: a non-admin reaches
// nothing; a refused change or reason rolls everything back, its journal
// line included.
func TestChallengeCorrectionsUpdateSettingsRefusals(t *testing.T) {
	tests := []struct {
		name      string
		actor     auth.Actor
		reason    string
		updateErr error
		wantCode  apperr.Code
		wantSteps []string
	}{
		{name: "not an admin", actor: auth.Actor{ID: annID}, reason: "Durée mal saisie", wantCode: apperr.CodeNotFound},
		{name: "no reason", actor: admin, reason: "  ", wantCode: apperr.CodeValidationFailed},
		{
			name: "settings refused", actor: admin, reason: "Durée mal saisie", updateErr: apperr.InvalidFields(nil, "weeks"), wantCode: apperr.CodeValidationFailed,
			wantSteps: []string{"lock", "rank " + challengeID, "eliminations " + challengeID, "update " + challengeID},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := &correctionLog{}
			corrections, journal := newCorrections(log)
			settings := &fakeSettings{log: log, err: tt.updateErr}
			s := NewChallengeCorrections(corrections, settings, settings)
			_, err := s.UpdateSettings(t.Context(), tt.actor, challengeID, challenge.SettingsChanges{}, tt.reason, false)
			var appErr *apperr.Error
			if !errors.As(err, &appErr) || appErr.Code != tt.wantCode {
				t.Fatalf("UpdateSettings() = %v, want %s", err, tt.wantCode)
			}
			if !reflect.DeepEqual(log.steps, tt.wantSteps) {
				t.Fatalf("steps %v, want %v", log.steps, tt.wantSteps)
			}
			if len(journal.records) != 0 {
				t.Fatalf("journal %+v, want nothing", journal.records)
			}
		})
	}
}

// settingsUpdaterFunc is a challengeUpdater that changes the settings only.
type settingsUpdaterFunc func(ctx context.Context, actor auth.Actor, id string, changes challenge.SettingsChanges, reason string, dryRun bool) ([]ChallengePreview, error)

func (f settingsUpdaterFunc) UpdateSettings(ctx context.Context, actor auth.Actor, id string, changes challenge.SettingsChanges, reason string, dryRun bool) ([]ChallengePreview, error) {
	return f(ctx, actor, id, changes, reason, dryRun)
}

func (settingsUpdaterFunc) Delete(context.Context, auth.Actor, string, string, bool) ([]ChallengePreview, error) {
	return nil, errors.New("delete not planned in this test")
}

func (settingsUpdaterFunc) Restore(context.Context, auth.Actor, string, string, bool) ([]ChallengePreview, error) {
	return nil, errors.New("restore not planned in this test")
}

func TestUpdateAdminChallenge(t *testing.T) {
	var gotChanges challenge.SettingsChanges
	var gotReason, gotID string
	var gotDryRun bool
	settings := settingsUpdaterFunc(func(_ context.Context, actor auth.Actor, id string, changes challenge.SettingsChanges, reason string, dryRun bool) ([]ChallengePreview, error) {
		if !actor.Admin {
			return nil, apperr.New(apperr.CodeNotFound, nil)
		}
		gotChanges, gotReason, gotID, gotDryRun = changes, reason, id, dryRun
		bob := UserRef{ID: bobID, Pseudo: new("bob")}
		return []ChallengePreview{{
			ChallengeID:  id,
			Eliminations: []EliminationChange{{User: bob, Before: &EliminationRow{User: bob, WeekNumber: 2, DistanceMeters: 6999, RequiredMeters: 7000}}},
		}}, nil
	})
	h := NewHandler(NewService(&fakeRepo{}, clock.NewFake(statsAt), buildinfo.Info{}), settings, nil, nil)
	body := `{"reason": "Durée mal saisie", "dry_run": true, "weeks": 6, "start_date": "2026-01-26", "visibility": "public",
		"categories": {"elimination": {"step_meters": 1500}, "points": {"criteria": ["session_count"], "podium": 10}},
		"distance_factors": {"ride": 0.5}}`
	rec, got := serveWith(t, h, adminToken, http.MethodPatch, "/api/v1/admin/challenges/"+challengeID, body, io.Discard)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %v, want 200", rec.Code, got)
	}
	want := challenge.SettingsChanges{
		Weeks: new(6), StartDate: new("2026-01-26"), Visibility: new("public"),
		Categories: &challenge.CategoryChoice{
			Elimination: &challenge.EliminationDraft{StepMeters: 1500},
			// The default of the contract, filled in as at creation.
			Points: &challenge.PointsDraft{Criteria: []string{"session_count"}, Podium: 10, MinSessionMinutes: new(20)},
		},
		DistanceFactors: map[string]float64{"ride": 0.5},
	}
	if !reflect.DeepEqual(gotChanges, want) || gotReason != "Durée mal saisie" || !gotDryRun || gotID != challengeID {
		t.Fatalf("asked %+v %q %v %s, want %+v", gotChanges, gotReason, gotDryRun, gotID, want)
	}
	previews, _ := got["challenges"].([]any)
	if got["dry_run"] != true || len(previews) != 1 {
		t.Fatalf("body %v, want the preview of the challenge", got)
	}
	change := previews[0].(map[string]any)["eliminations"].([]any)[0].(map[string]any)
	if change["after"] != nil || change["before"].(map[string]any)["week_number"] != float64(2) {
		t.Fatalf("elimination change %v, want bob no longer eliminated in week 2", change)
	}

	t.Run("without the permission", func(t *testing.T) {
		rec, got := serveWith(t, h, "some.access.token", http.MethodPatch, "/api/v1/admin/challenges/"+challengeID, `{"reason": "Durée mal saisie"}`, io.Discard)
		if e, _ := got["error"].(map[string]any); rec.Code != http.StatusNotFound || e["code"] != "not_found" {
			t.Fatalf("got %d %v, want 404 not_found", rec.Code, got)
		}
	})
	t.Run("busy", func(t *testing.T) {
		busy := settingsUpdaterFunc(func(context.Context, auth.Actor, string, challenge.SettingsChanges, string, bool) ([]ChallengePreview, error) {
			return nil, apperr.New(apperr.CodeBusy, errors.New("lock timeout"))
		})
		h := NewHandler(NewService(&fakeRepo{}, clock.NewFake(statsAt), buildinfo.Info{}), busy, nil, nil)
		rec, got := serveWith(t, h, adminToken, http.MethodPatch, "/api/v1/admin/challenges/"+challengeID, `{"reason": "Durée mal saisie"}`, io.Discard)
		if e, _ := got["error"].(map[string]any); rec.Code != http.StatusServiceUnavailable || e["code"] != "busy" {
			t.Fatalf("got %d %v, want 503 busy", rec.Code, got)
		}
	})
	t.Run("null is refused by the contract", func(t *testing.T) {
		rec, got := serveWith(t, h, adminToken, http.MethodPatch, "/api/v1/admin/challenges/"+challengeID, `{"reason": "Durée mal saisie", "weeks": null}`, io.Discard)
		if e, _ := got["error"].(map[string]any); rec.Code != http.StatusBadRequest || e["code"] != "validation_failed" {
			t.Fatalf("got %d %v, want 400 validation_failed", rec.Code, got)
		}
	})
}
