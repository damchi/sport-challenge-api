package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/challenge"
	"github.com/damchi/sport-challenge-api/internal/leaderboard"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/buildinfo"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

// restoreNamingNone is Restore naming no creator, in the shape of Delete.
func (s *ChallengeCorrections) restoreNamingNone(ctx context.Context, actor auth.Actor, id, reason string, dryRun bool) ([]ChallengePreview, error) {
	return s.Restore(ctx, actor, id, nil, reason, dryRun)
}

// TestChallengeCorrectionsDeleteAndRestore: a deletion is ranked before the
// change only and never recomputed; a restore is ranked after it only, once
// recomputed, its eliminations compared with those kept at the deletion;
// both write their journal line with the instant of the deletion.
func TestChallengeCorrectionsDeleteAndRestore(t *testing.T) {
	deletedAt := time.Date(2026, 3, 2, 14, 0, 0, 0, time.UTC)
	bob := UserRef{ID: bobID}
	tests := []struct {
		name         string
		run          func(s *ChallengeCorrections, ctx context.Context, actor auth.Actor, id, reason string, dryRun bool) ([]ChallengePreview, error)
		wantSteps    []string
		wantAction   string
		wantOld      map[string]any
		wantNew      map[string]any
		wantBefore   bool
		wantAfter    bool
		wantChanges  int
		eliminations [][]EliminationRow
	}{
		{
			name:       "delete",
			run:        (*ChallengeCorrections).Delete,
			wantSteps:  []string{"lock", "lock participants " + challengeID, "rank " + challengeID, "weeks " + challengeID, "state " + challengeID, "delete " + challengeID, "journal", "state " + challengeID},
			wantAction: ActionChallengeDelete,
			wantOld:    map[string]any{"deleted_at": nil},
			wantNew:    map[string]any{"deleted_at": deletedAt},
			wantBefore: true,
		},
		{
			name: "restore",
			run:  (*ChallengeCorrections).restoreNamingNone,
			wantSteps: []string{
				"lock", "lock participants " + challengeID, "state " + challengeID, "restore " + challengeID, "journal",
				"recompute " + challengeID, "rank " + challengeID, "weeks " + challengeID, "state " + challengeID,
			},
			wantAction: ActionChallengeRestore,
			wantOld:    map[string]any{"deleted_at": deletedAt},
			wantNew:    map[string]any{"deleted_at": nil},
			wantAfter:  true,
			// Bob is eliminated in week 2, a week locked while the challenge
			// was deleted, judged by the recompute only.
			eliminations: [][]EliminationRow{nil, {{User: bob, WeekNumber: 2, DistanceMeters: 6999, RequiredMeters: 7000}}},
			wantChanges:  1,
		},
	}
	for _, tt := range tests {
		for _, dryRun := range []bool{false, true} {
			t.Run(tt.name+map[bool]string{false: "", true: ", dry run"}[dryRun], func(t *testing.T) {
				log := &correctionLog{eliminations: map[string][][]EliminationRow{challengeID: tt.eliminations}}
				corrections, journal := newCorrections(log)
				settings := &fakeSettings{log: log, deletedAt: deletedAt}
				s := NewChallengeCorrections(corrections, settings, settings)
				previews, err := tt.run(s, t.Context(), admin, challengeID, "Supprimé par erreur", dryRun)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(log.steps, tt.wantSteps) || log.rolledBack != dryRun {
					t.Fatalf("steps %v (rolled back %v), want %v (rolled back %v)", log.steps, log.rolledBack, tt.wantSteps, dryRun)
				}
				if len(previews) != 1 || previews[0].ChallengeID != challengeID ||
					(previews[0].Before != nil) != tt.wantBefore || (previews[0].After != nil) != tt.wantAfter ||
					len(previews[0].Eliminations) != tt.wantChanges {
					t.Fatalf("previews %+v, want before %v, after %v, %d elimination changes", previews, tt.wantBefore, tt.wantAfter, tt.wantChanges)
				}
				a := journal.records[0]
				oldValue, _ := json.Marshal(tt.wantOld)
				newValue, _ := json.Marshal(tt.wantNew)
				if a.Action != tt.wantAction || a.TargetType != TargetChallenge || a.TargetID != challengeID ||
					!reflect.DeepEqual(a.ChallengeIDs, []string{challengeID}) || string(a.OldValue) != string(oldValue) ||
					string(a.NewValue) != string(newValue) || a.Reason != "Supprimé par erreur" || a.AdminID != annID {
					t.Fatalf("journal line %+v", a)
				}
			})
		}
	}
}

// TestChallengeCorrectionsDeleteAndRestoreRefusals: a non-admin reaches
// nothing; a refused change rolls everything back, its journal line
// included.
func TestChallengeCorrectionsDeleteAndRestoreRefusals(t *testing.T) {
	missing := apperr.New(apperr.CodeChallengeNotFound, nil)
	tests := []struct {
		name      string
		run       func(s *ChallengeCorrections, ctx context.Context, actor auth.Actor, id, reason string, dryRun bool) ([]ChallengePreview, error)
		actor     auth.Actor
		reason    string
		err       error
		wantCode  apperr.Code
		wantSteps []string
	}{
		{name: "delete, not an admin", run: (*ChallengeCorrections).Delete, actor: auth.Actor{ID: annID}, reason: "Doublon", wantCode: apperr.CodeNotFound},
		{name: "restore, not an admin", run: (*ChallengeCorrections).restoreNamingNone, actor: auth.Actor{ID: annID}, reason: "Doublon", wantCode: apperr.CodeNotFound},
		{name: "delete, no reason", run: (*ChallengeCorrections).Delete, actor: admin, reason: "", wantCode: apperr.CodeValidationFailed},
		{name: "restore, no reason", run: (*ChallengeCorrections).restoreNamingNone, actor: admin, reason: "", wantCode: apperr.CodeValidationFailed},
		{
			name: "restore, not deleted", run: (*ChallengeCorrections).restoreNamingNone, actor: admin, reason: "Doublon", err: missing, wantCode: apperr.CodeChallengeNotFound,
			wantSteps: []string{"lock", "lock participants " + challengeID, "state " + challengeID, "restore " + challengeID},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := &correctionLog{}
			corrections, journal := newCorrections(log)
			settings := &fakeSettings{log: log, err: tt.err}
			s := NewChallengeCorrections(corrections, settings, settings)
			_, err := tt.run(s, t.Context(), tt.actor, challengeID, tt.reason, false)
			var appErr *apperr.Error
			if !errors.As(err, &appErr) || appErr.Code != tt.wantCode {
				t.Fatalf("error = %v, want %s", err, tt.wantCode)
			}
			if !reflect.DeepEqual(log.steps, tt.wantSteps) || len(journal.records) != 0 {
				t.Fatalf("steps %v, journal %+v, want steps %v and no journal line", log.steps, journal.records, tt.wantSteps)
			}
		})
	}
}

// fakeLifecycle is a challengeUpdater that deletes and restores only: it
// records what it is asked and answers a preview like the envelope's.
type fakeLifecycle struct {
	settingsUpdaterFunc
	asked string
}

func (f *fakeLifecycle) Delete(_ context.Context, actor auth.Actor, id, reason string, dryRun bool) ([]ChallengePreview, error) {
	return f.answer("delete", actor, id, reason, dryRun, &leaderboard.Leaderboard{}, nil)
}

func (f *fakeLifecycle) Restore(_ context.Context, actor auth.Actor, id string, creatorID *string, reason string, dryRun bool) ([]ChallengePreview, error) {
	what := "restore"
	if creatorID != nil {
		what += " naming " + *creatorID
	}
	return f.answer(what, actor, id, reason, dryRun, nil, &leaderboard.Leaderboard{})
}

func (f *fakeLifecycle) answer(what string, actor auth.Actor, id, reason string, dryRun bool, before, after *leaderboard.Leaderboard) ([]ChallengePreview, error) {
	if !actor.Admin {
		return nil, apperr.New(apperr.CodeNotFound, nil)
	}
	f.asked = what + " " + id + " " + reason + map[bool]string{false: "", true: " dry run"}[dryRun]
	return []ChallengePreview{{ChallengeID: id, Status: StatusFinished, Before: before, After: after}}, nil
}

func TestDeleteAndRestoreAdminChallenge(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		path      string
		body      string
		wantAsked string
		wantNull  string
		wantBoard string
		wantDry   bool
	}{
		{
			name: "delete", method: http.MethodDelete, path: "/api/v1/admin/challenges/" + challengeID,
			body: `{"reason": "Doublon"}`, wantAsked: "delete " + challengeID + " Doublon", wantNull: "after", wantBoard: "before",
		},
		{
			name: "restore, dry run", method: http.MethodPost, path: "/api/v1/admin/challenges/" + challengeID + "/restore",
			body: `{"reason": "Supprimé par erreur", "dry_run": true}`, wantAsked: "restore " + challengeID + " Supprimé par erreur dry run",
			wantNull: "before", wantBoard: "after", wantDry: true,
		},
		{
			name: "restore naming a creator", method: http.MethodPost, path: "/api/v1/admin/challenges/" + challengeID + "/restore",
			body:      `{"reason": "Supprimé par erreur", "creator_user_id": "` + bobID + `"}`,
			wantAsked: "restore naming " + bobID + " " + challengeID + " Supprimé par erreur", wantNull: "before", wantBoard: "after",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lifecycle := &fakeLifecycle{}
			h := NewHandler(NewService(&fakeRepo{}, nil, clock.NewFake(statsAt), buildinfo.Info{}), lifecycle, nil, nil, nil)
			rec, got := serveWith(t, h, adminToken, tt.method, tt.path, tt.body, io.Discard)
			if rec.Code != http.StatusOK || lifecycle.asked != tt.wantAsked {
				t.Fatalf("got %d %v, asked %q, want 200 and %q", rec.Code, got, lifecycle.asked, tt.wantAsked)
			}
			previews, _ := got["challenges"].([]any)
			if got["dry_run"] != tt.wantDry || len(previews) != 1 {
				t.Fatalf("body %v, want the preview of the challenge", got)
			}
			p := previews[0].(map[string]any)
			if v, ok := p[tt.wantNull]; !ok || v != nil {
				t.Fatalf("preview %v, want %s null", p, tt.wantNull)
			}
			if _, ok := p[tt.wantBoard].(map[string]any); !ok {
				t.Fatalf("preview %v, want the rankings %s", p, tt.wantBoard)
			}
		})
		t.Run(tt.name+", without the permission", func(t *testing.T) {
			lifecycle := &fakeLifecycle{}
			h := NewHandler(NewService(&fakeRepo{}, nil, clock.NewFake(statsAt), buildinfo.Info{}), lifecycle, nil, nil, nil)
			rec, got := serveWith(t, h, "some.access.token", tt.method, tt.path, tt.body, io.Discard)
			if e, _ := got["error"].(map[string]any); rec.Code != http.StatusNotFound || e["code"] != "not_found" || lifecycle.asked != "" {
				t.Fatalf("got %d %v, asked %q, want 404 not_found and nothing asked", rec.Code, got, lifecycle.asked)
			}
		})
	}
}

// TestChallengeCorrectionsRestoreNamingACreator: the creator a restore
// names is passed on, and its journal line names the account by its id
// with what the restore made of it, before and after; a refusal of the
// restore (no creator, an incomplete profile, an unknown account) rolls
// everything back, journal line included, preview included.
func TestChallengeCorrectionsRestoreNamingACreator(t *testing.T) {
	deletedAt := time.Date(2026, 3, 2, 14, 0, 0, 0, time.UTC)
	t.Run("named", func(t *testing.T) {
		log := &correctionLog{}
		corrections, journal := newCorrections(log)
		settings := &fakeSettings{log: log, deletedAt: deletedAt, creator: challenge.MemberChange{
			Old: map[string]any{"creator": false},
			New: map[string]any{"status": challenge.ParticipantActive, "creator": true},
		}}
		s := NewChallengeCorrections(corrections, settings, settings)
		creator := bobID
		if _, err := s.Restore(t.Context(), admin, challengeID, &creator, "Supprimé par erreur", false); err != nil {
			t.Fatal(err)
		}
		if settings.creatorID == nil || *settings.creatorID != bobID {
			t.Fatalf("creator asked %v, want %s", settings.creatorID, bobID)
		}
		a := journal.records[0]
		wantOld, _ := json.Marshal(map[string]any{"deleted_at": deletedAt, "named_creator": map[string]any{"user_id": bobID, "creator": false}})
		wantNew, _ := json.Marshal(map[string]any{"deleted_at": nil, "named_creator": map[string]any{"user_id": bobID, "status": "active", "creator": true}})
		if a.Action != ActionChallengeRestore || string(a.OldValue) != string(wantOld) || string(a.NewValue) != string(wantNew) {
			t.Fatalf("journal line %s -> %s, want %s -> %s", a.OldValue, a.NewValue, wantOld, wantNew)
		}
	})
	for _, code := range []apperr.Code{apperr.CodeNoCreator, apperr.CodeProfileIncomplete, apperr.CodeUserNotFound} {
		for _, dryRun := range []bool{false, true} {
			t.Run(string(code)+map[bool]string{false: "", true: ", dry run"}[dryRun], func(t *testing.T) {
				log := &correctionLog{}
				corrections, journal := newCorrections(log)
				settings := &fakeSettings{log: log, err: apperr.New(code, nil)}
				s := NewChallengeCorrections(corrections, settings, settings)
				creator := bobID
				_, err := s.Restore(t.Context(), admin, challengeID, &creator, "Supprimé par erreur", dryRun)
				var appErr *apperr.Error
				if !errors.As(err, &appErr) || appErr.Code != code || appErr.Status == http.StatusOK {
					t.Fatalf("error = %v, want %s", err, code)
				}
				if len(journal.records) != 0 || slices.Contains(log.steps, "recompute "+challengeID) {
					t.Fatalf("journal %+v, steps %v, want no journal line and no recompute", journal.records, log.steps)
				}
			})
		}
	}
}

// TestRestoreAdminChallengeInvalidBody: a restore without a body, or with a
// creator that is not a UUID, or an unknown field, answers 400
// validation_failed to an admin, nothing asked, and 404 not_found to
// anyone else.
func TestRestoreAdminChallengeInvalidBody(t *testing.T) {
	path := "/api/v1/admin/challenges/" + challengeID + "/restore"
	for name, body := range map[string]string{
		"no body":            "",
		"creator not a uuid": `{"reason": "Supprimé par erreur", "creator_user_id": "bob"}`,
		"unknown field":      `{"reason": "Supprimé par erreur", "creator": "` + bobID + `"}`,
	} {
		for _, tt := range []struct {
			who, token string
			status     int
			code       string
		}{
			{who: "admin", token: adminToken, status: http.StatusBadRequest, code: "validation_failed"},
			{who: "without the permission", token: "some.access.token", status: http.StatusNotFound, code: "not_found"},
		} {
			t.Run(name+", "+tt.who, func(t *testing.T) {
				lifecycle := &fakeLifecycle{}
				h := NewHandler(NewService(&fakeRepo{}, nil, clock.NewFake(statsAt), buildinfo.Info{}), lifecycle, nil, nil, nil)
				rec, got := serveWith(t, h, tt.token, http.MethodPost, path, body, io.Discard)
				if e, _ := got["error"].(map[string]any); rec.Code != tt.status || e["code"] != tt.code || lifecycle.asked != "" {
					t.Fatalf("got %d %v, asked %q, want %d %s and nothing asked", rec.Code, got, lifecycle.asked, tt.status, tt.code)
				}
			})
		}
	}
}
