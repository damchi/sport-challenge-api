package admin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"testing"

	"github.com/damchi/sport-challenge-api/internal/challenge"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/buildinfo"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

// fakeMembers is module challenge for the corrections of the members of a
// challenge: it logs what it is asked and answers change or err.
type fakeMembers struct {
	log    *correctionLog
	change challenge.MemberChange
	err    error
}

func (f *fakeMembers) do(name string, actor auth.Actor, id, userID string) (challenge.MemberChange, error) {
	f.log.add(name + " " + id + " " + userID)
	if !actor.Admin {
		return challenge.MemberChange{}, errors.New("changed for a non-admin")
	}
	return f.change, f.err
}

func (f *fakeMembers) AdminAddParticipant(_ context.Context, actor auth.Actor, id, userID string) (challenge.MemberChange, error) {
	return f.do("add", actor, id, userID)
}

func (f *fakeMembers) AdminReinstateParticipant(_ context.Context, actor auth.Actor, id, userID string) (challenge.MemberChange, error) {
	return f.do("reinstate", actor, id, userID)
}

func (f *fakeMembers) AdminExcludeParticipant(_ context.Context, actor auth.Actor, id, userID string) (challenge.MemberChange, error) {
	return f.do("exclude", actor, id, userID)
}

func (f *fakeMembers) AdminAddCreator(_ context.Context, actor auth.Actor, id, userID string) (challenge.MemberChange, error) {
	return f.do("add creator", actor, id, userID)
}

func (f *fakeMembers) AdminRemoveCreator(_ context.Context, actor auth.Actor, id, userID string) (challenge.MemberChange, error) {
	return f.do("remove creator", actor, id, userID)
}

// memberCorrectionFunc is one of the methods of MemberCorrections.
type memberCorrectionFunc func(s *MemberCorrections, ctx context.Context, actor auth.Actor, id, userID, reason string, dryRun bool) ([]ChallengePreview, error)

// memberCorrections are the methods of MemberCorrections, with the step
// their use case logs and their journal code.
var memberCorrections = []struct {
	name   string
	run    memberCorrectionFunc
	step   string
	action string
}{
	{name: "add", run: (*MemberCorrections).AddParticipant, step: "add", action: ActionParticipantAdd},
	{name: "reinstate", run: (*MemberCorrections).ReinstateParticipant, step: "reinstate", action: ActionParticipantReinstate},
	{name: "exclude", run: (*MemberCorrections).ExcludeParticipant, step: "exclude", action: ActionParticipantExclude},
	{name: "add creator", run: (*MemberCorrections).AddCreator, step: "add creator", action: ActionCreatorAdd},
	{name: "remove creator", run: (*MemberCorrections).RemoveCreator, step: "remove creator", action: ActionCreatorRemove},
}

// TestMemberCorrections: each change of a member runs in the transaction
// of the corrections, before its journal line, which names the account as
// a participant and the challenge, then the challenge is judged again.
func TestMemberCorrections(t *testing.T) {
	change := challenge.MemberChange{Old: map[string]any{"status": "abandoned"}, New: map[string]any{"status": "active"}}
	for _, mc := range memberCorrections {
		t.Run(mc.name, func(t *testing.T) {
			log := &correctionLog{}
			corrections, journal := newCorrections(log)
			s := NewMemberCorrections(corrections, &fakeMembers{log: log, change: change})
			previews, err := mc.run(s, t.Context(), admin, challengeID, bobID, "Oubli d'inscription", false)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{
				"lock", "rank " + challengeID, "eliminations " + challengeID, mc.step + " " + challengeID + " " + bobID, "journal",
				"recompute " + challengeID, "rank " + challengeID, "eliminations " + challengeID,
			}
			if !reflect.DeepEqual(log.steps, want) {
				t.Fatalf("steps %v, want %v", log.steps, want)
			}
			if len(previews) != 1 || previews[0].ChallengeID != challengeID {
				t.Fatalf("previews %+v, want the challenge", previews)
			}
			a := journal.records[0]
			if a.Action != mc.action || a.TargetType != TargetParticipant || a.TargetID != bobID ||
				!reflect.DeepEqual(a.ChallengeIDs, []string{challengeID}) || string(a.OldValue) != `{"status":"abandoned"}` ||
				string(a.NewValue) != `{"status":"active"}` || a.Reason != "Oubli d'inscription" || a.AdminID != annID {
				t.Fatalf("journal line %+v %s %s", a, a.OldValue, a.NewValue)
			}
		})
	}
}

// TestMemberCorrectionsRefusals: a non-admin reaches nothing; a refused
// change or reason rolls everything back, its journal line included.
func TestMemberCorrectionsRefusals(t *testing.T) {
	for _, mc := range memberCorrections {
		tests := []struct {
			name      string
			actor     auth.Actor
			reason    string
			err       error
			wantCode  apperr.Code
			wantSteps []string
		}{
			{name: "not an admin", actor: auth.Actor{ID: annID}, reason: "Oubli d'inscription", wantCode: apperr.CodeNotFound},
			{name: "no reason", actor: admin, reason: "  ", wantCode: apperr.CodeValidationFailed},
			{
				name: "change refused", actor: admin, reason: "Oubli d'inscription", err: apperr.New(apperr.CodeLastCreator, nil), wantCode: apperr.CodeLastCreator,
				wantSteps: []string{"lock", "rank " + challengeID, "eliminations " + challengeID, mc.step + " " + challengeID + " " + bobID},
			},
		}
		for _, tt := range tests {
			t.Run(mc.name+": "+tt.name, func(t *testing.T) {
				log := &correctionLog{}
				corrections, journal := newCorrections(log)
				s := NewMemberCorrections(corrections, &fakeMembers{log: log, err: tt.err})
				_, err := mc.run(s, t.Context(), tt.actor, challengeID, bobID, tt.reason, false)
				var appErr *apperr.Error
				if !errors.As(err, &appErr) || appErr.Code != tt.wantCode {
					t.Fatalf("got %v, want %s", err, tt.wantCode)
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
}

// memberUpdaterFunc is a memberUpdater whose every method calls f with the
// name of the method.
type memberUpdaterFunc func(method string, actor auth.Actor, id, userID, reason string, dryRun bool) ([]ChallengePreview, error)

func (f memberUpdaterFunc) AddParticipant(_ context.Context, actor auth.Actor, id, userID, reason string, dryRun bool) ([]ChallengePreview, error) {
	return f("add", actor, id, userID, reason, dryRun)
}

func (f memberUpdaterFunc) ReinstateParticipant(_ context.Context, actor auth.Actor, id, userID, reason string, dryRun bool) ([]ChallengePreview, error) {
	return f("reinstate", actor, id, userID, reason, dryRun)
}

func (f memberUpdaterFunc) ExcludeParticipant(_ context.Context, actor auth.Actor, id, userID, reason string, dryRun bool) ([]ChallengePreview, error) {
	return f("exclude", actor, id, userID, reason, dryRun)
}

func (f memberUpdaterFunc) AddCreator(_ context.Context, actor auth.Actor, id, userID, reason string, dryRun bool) ([]ChallengePreview, error) {
	return f("add creator", actor, id, userID, reason, dryRun)
}

func (f memberUpdaterFunc) RemoveCreator(_ context.Context, actor auth.Actor, id, userID, reason string, dryRun bool) ([]ChallengePreview, error) {
	return f("remove creator", actor, id, userID, reason, dryRun)
}

// TestAdminMemberRoutes: each route passes the challenge, the account, the
// reason and dry_run to its change and answers its preview; without the
// permission, 404 not_found; a refusal keeps its code.
func TestAdminMemberRoutes(t *testing.T) {
	type call struct {
		method, id, userID, reason string
		dryRun                     bool
	}
	var got call
	members := memberUpdaterFunc(func(method string, actor auth.Actor, id, userID, reason string, dryRun bool) ([]ChallengePreview, error) {
		if !actor.Admin {
			return nil, apperr.New(apperr.CodeNotFound, nil)
		}
		got = call{method, id, userID, reason, dryRun}
		if reason == "refused" {
			return nil, apperr.New(apperr.CodeChallengeNotFound, nil)
		}
		return []ChallengePreview{{ChallengeID: id}}, nil
	})
	h := NewHandler(NewService(&fakeRepo{}, clock.NewFake(statsAt), buildinfo.Info{}), nil, members, nil, nil)
	member := "/api/v1/admin/challenges/" + challengeID + "/participants/" + bobID
	creator := "/api/v1/admin/challenges/" + challengeID + "/creators/" + bobID
	routes := []struct {
		name, method, target, body, wantMethod string
	}{
		{name: "add", method: http.MethodPost, target: "/api/v1/admin/challenges/" + challengeID + "/participants", body: `{"user_id": "` + bobID + `", `, wantMethod: "add"},
		{name: "reinstate", method: http.MethodPost, target: member + "/reinstate", body: `{`, wantMethod: "reinstate"},
		{name: "exclude", method: http.MethodDelete, target: member, body: `{`, wantMethod: "exclude"},
		{name: "add creator", method: http.MethodPut, target: creator, body: `{`, wantMethod: "add creator"},
		{name: "remove creator", method: http.MethodDelete, target: creator, body: `{`, wantMethod: "remove creator"},
	}
	for _, r := range routes {
		t.Run(r.name, func(t *testing.T) {
			got = call{}
			rec, body := serveWith(t, h, adminToken, r.method, r.target, r.body+`"reason": "Oubli d'inscription", "dry_run": true}`, io.Discard)
			if rec.Code != http.StatusOK {
				t.Fatalf("got %d %v, want 200", rec.Code, body)
			}
			if want := (call{r.wantMethod, challengeID, bobID, "Oubli d'inscription", true}); got != want {
				t.Fatalf("asked %+v, want %+v", got, want)
			}
			if previews, _ := body["challenges"].([]any); body["dry_run"] != true || len(previews) != 1 {
				t.Fatalf("body %v, want the preview of the challenge", body)
			}

			rec, body = serveWith(t, h, "some.access.token", r.method, r.target, r.body+`"reason": "Oubli d'inscription"}`, io.Discard)
			if e, _ := body["error"].(map[string]any); rec.Code != http.StatusNotFound || e["code"] != "not_found" {
				t.Fatalf("without the permission: got %d %v, want 404 not_found", rec.Code, body)
			}

			rec, body = serveWith(t, h, adminToken, r.method, r.target, r.body+`"reason": "refused"}`, io.Discard)
			if e, _ := body["error"].(map[string]any); rec.Code != http.StatusNotFound || e["code"] != "challenge_not_found" {
				t.Fatalf("refused: got %d %v, want 404 challenge_not_found", rec.Code, body)
			}

			rec, body = serveWith(t, h, adminToken, r.method, r.target, r.body+`"dry_run": true}`, io.Discard)
			if e, _ := body["error"].(map[string]any); rec.Code != http.StatusBadRequest || e["code"] != "validation_failed" {
				t.Fatalf("no reason: got %d %v, want 400 validation_failed", rec.Code, body)
			}
		})
	}
}
