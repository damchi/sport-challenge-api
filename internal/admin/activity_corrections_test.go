package admin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/activity"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/buildinfo"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

// fakeActivities is module activity for the corrections of an activity: it
// logs what it is asked, prepares a write touching touched that logs
// itself and answers change or writeErr, or refuses with err.
type fakeActivities struct {
	log      *correctionLog
	touched  []string
	change   activity.AdminChange
	err      error
	writeErr error
	// key is the idempotency key AdminRecord got.
	key *string
}

func (f *fakeActivities) prepare(step string, actor auth.Actor) (activity.AdminWrite, error) {
	f.log.add(step)
	if !actor.Admin {
		return activity.AdminWrite{}, errors.New("prepared for a non-admin")
	}
	if f.err != nil {
		return activity.AdminWrite{}, f.err
	}
	return activity.AdminWrite{Touched: f.touched, Write: func(context.Context) (activity.AdminChange, error) {
		f.log.add("write")
		return f.change, f.writeErr
	}}, nil
}

func (f *fakeActivities) AdminRecord(_ context.Context, actor auth.Actor, userID string, key *string, _ activity.Draft) (activity.AdminWrite, error) {
	f.key = key
	return f.prepare("record "+userID, actor)
}

func (f *fakeActivities) AdminUpdate(_ context.Context, actor auth.Actor, id string, _ activity.Draft) (activity.AdminWrite, error) {
	return f.prepare("update "+id, actor)
}

func (f *fakeActivities) AdminDelete(_ context.Context, actor auth.Actor, id string) (activity.AdminWrite, error) {
	return f.prepare("delete "+id, actor)
}

// activityCorrections are the methods of ActivityCorrections, as one shape,
// with the step their preparation logs and their journal code.
var activityCorrections = []struct {
	name   string
	run    func(s *ActivityCorrections, ctx context.Context, actor auth.Actor, reason string, dryRun bool) ([]ChallengePreview, error)
	step   string
	action string
}{
	{name: "record", step: "record " + bobID, action: ActionActivityCreate, run: func(s *ActivityCorrections, ctx context.Context, actor auth.Actor, reason string, dryRun bool) ([]ChallengePreview, error) {
		return s.Record(ctx, actor, bobID, nil, activity.Draft{}, reason, dryRun)
	}},
	{name: "update", step: "update " + actionID, action: ActionActivityUpdate, run: func(s *ActivityCorrections, ctx context.Context, actor auth.Actor, reason string, dryRun bool) ([]ChallengePreview, error) {
		return s.Update(ctx, actor, actionID, activity.Draft{}, reason, dryRun)
	}},
	{name: "delete", step: "delete " + actionID, action: ActionActivityDelete, run: func(s *ActivityCorrections, ctx context.Context, actor auth.Actor, reason string, dryRun bool) ([]ChallengePreview, error) {
		return s.Delete(ctx, actor, actionID, reason, dryRun)
	}},
}

// TestActivityCorrections: the change is prepared in the transaction,
// before the rankings of the challenges it touches are read, then written
// before its journal line, which names the activity and every challenge
// touched, each then judged again.
func TestActivityCorrections(t *testing.T) {
	change := activity.AdminChange{ActivityID: actionID, Old: map[string]any{"duration_seconds": 1500}, New: map[string]any{"duration_seconds": 1600}}
	for _, ac := range activityCorrections {
		t.Run(ac.name, func(t *testing.T) {
			log := &correctionLog{}
			corrections, journal := newCorrections(log)
			s := NewActivityCorrections(corrections, &fakeActivities{log: log, touched: []string{challengeID, inviteID}, change: change})
			previews, err := ac.run(s, t.Context(), admin, "Montre mal réglée", false)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{
				"lock", ac.step, "lock participants " + challengeID + "," + inviteID,
				"rank " + challengeID, "weeks " + challengeID, "state " + challengeID, "rank " + inviteID, "weeks " + inviteID, "state " + inviteID,
				"write", "journal",
				"recompute " + challengeID, "rank " + challengeID, "weeks " + challengeID, "state " + challengeID,
				"recompute " + inviteID, "rank " + inviteID, "weeks " + inviteID, "state " + inviteID,
			}
			if !reflect.DeepEqual(log.steps, want) {
				t.Fatalf("steps %v, want %v", log.steps, want)
			}
			if len(previews) != 2 || previews[0].ChallengeID != challengeID || previews[1].ChallengeID != inviteID {
				t.Fatalf("previews %+v, want both challenges", previews)
			}
			a := journal.records[0]
			if a.Action != ac.action || a.TargetType != TargetActivity || a.TargetID != actionID ||
				!reflect.DeepEqual(a.ChallengeIDs, []string{challengeID, inviteID}) || string(a.OldValue) != `{"duration_seconds":1500}` ||
				string(a.NewValue) != `{"duration_seconds":1600}` || a.Reason != "Montre mal réglée" || a.AdminID != annID {
				t.Fatalf("journal line %+v %s %s", a, a.OldValue, a.NewValue)
			}
		})
	}
}

// TestActivityCorrectionsCountingNowhere: an activity that counts nowhere
// is changed and recorded, no challenge judged nor previewed.
func TestActivityCorrectionsCountingNowhere(t *testing.T) {
	log := &correctionLog{}
	corrections, journal := newCorrections(log)
	s := NewActivityCorrections(corrections, &fakeActivities{log: log, change: activity.AdminChange{ActivityID: actionID}})
	previews, err := s.Delete(t.Context(), admin, actionID, "Doublon", false)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"lock", "delete " + actionID, "write", "journal"}; !reflect.DeepEqual(log.steps, want) || len(previews) != 0 {
		t.Fatalf("steps %v, previews %+v; want %v, none", log.steps, previews, want)
	}
	if len(journal.records) != 1 || len(journal.records[0].ChallengeIDs) != 0 {
		t.Fatalf("journal %+v, want one line touching no challenge", journal.records)
	}
}

// TestActivityCorrectionsRecordedBefore: a key that gave an activity
// recorded before writes nothing, no journal line, judges no challenge
// again and previews none, a dry run included.
func TestActivityCorrectionsRecordedBefore(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		log := &correctionLog{}
		corrections, journal := newCorrections(log)
		fake := &fakeActivities{log: log, touched: []string{challengeID}, writeErr: activity.ErrRecordedBefore}
		s := NewActivityCorrections(corrections, fake)
		key := "k1"
		previews, err := s.Record(t.Context(), admin, bobID, &key, activity.Draft{}, "Saisie oubliée", dryRun)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"lock", "record " + bobID, "lock participants " + challengeID, "rank " + challengeID, "weeks " + challengeID, "state " + challengeID, "write"}
		if !reflect.DeepEqual(log.steps, want) || previews == nil || len(previews) != 0 || len(journal.records) != 0 {
			t.Fatalf("dry run %v: steps %v, previews %#v, journal %+v; want %v, an empty list, no line", dryRun, log.steps, previews, journal.records, want)
		}
		if fake.key != &key {
			t.Fatalf("key %v, want that of the request", fake.key)
		}
	}
}

// TestActivityCorrectionsRefusals: a non-admin reaches nothing; a refused
// change or reason rolls everything back, its journal line included.
func TestActivityCorrectionsRefusals(t *testing.T) {
	for _, ac := range activityCorrections {
		tests := []struct {
			name      string
			actor     auth.Actor
			reason    string
			err       error
			writeErr  error
			wantCode  apperr.Code
			wantSteps []string
		}{
			{name: "not an admin", actor: auth.Actor{ID: annID}, reason: "Doublon", wantCode: apperr.CodeNotFound},
			{name: "no reason", actor: admin, reason: "  ", wantCode: apperr.CodeValidationFailed},
			{
				name: "refused before anything is read", actor: admin, reason: "Doublon", err: apperr.New(apperr.CodeActivityNotFound, nil), wantCode: apperr.CodeActivityNotFound,
				wantSteps: []string{"lock", ac.step},
			},
			{
				name: "write refused", actor: admin, reason: "Doublon", writeErr: apperr.New(apperr.CodeActivityOverlap, nil), wantCode: apperr.CodeActivityOverlap,
				wantSteps: []string{"lock", ac.step, "lock participants " + challengeID, "rank " + challengeID, "weeks " + challengeID, "state " + challengeID, "write"},
			},
		}
		for _, tt := range tests {
			t.Run(ac.name+": "+tt.name, func(t *testing.T) {
				log := &correctionLog{}
				corrections, journal := newCorrections(log)
				s := NewActivityCorrections(corrections, &fakeActivities{log: log, touched: []string{challengeID}, err: tt.err, writeErr: tt.writeErr})
				_, err := ac.run(s, t.Context(), tt.actor, tt.reason, false)
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

// activityUpdaterFunc is an activityUpdater whose every method calls f
// with the name of the method and the id it targets.
type activityUpdaterFunc func(method string, actor auth.Actor, id string, draft activity.Draft, reason string, dryRun bool) ([]ChallengePreview, error)

func (f activityUpdaterFunc) Record(_ context.Context, actor auth.Actor, userID string, key *string, draft activity.Draft, reason string, dryRun bool) ([]ChallengePreview, error) {
	if key != nil {
		return f("record "+*key, actor, userID, draft, reason, dryRun)
	}
	return f("record", actor, userID, draft, reason, dryRun)
}

func (f activityUpdaterFunc) Update(_ context.Context, actor auth.Actor, id string, draft activity.Draft, reason string, dryRun bool) ([]ChallengePreview, error) {
	return f("update", actor, id, draft, reason, dryRun)
}

func (f activityUpdaterFunc) Delete(_ context.Context, actor auth.Actor, id, reason string, dryRun bool) ([]ChallengePreview, error) {
	return f("delete", actor, id, activity.Draft{}, reason, dryRun)
}

// TestAdminActivityRoutes: each route passes the target, the activity, the
// reason and dry_run to its change (the repetitions in the draft) and answers
// its preview; without the permission, 404 not_found; a refusal keeps its
// code (422 unknown_exercise of a record or an update); a body without its
// reason is refused.
func TestAdminActivityRoutes(t *testing.T) {
	type call struct {
		method, id, reason string
		draft              activity.Draft
		dryRun             bool
	}
	var got call
	activities := activityUpdaterFunc(func(method string, actor auth.Actor, id string, draft activity.Draft, reason string, dryRun bool) ([]ChallengePreview, error) {
		if !actor.Admin {
			return nil, apperr.New(apperr.CodeNotFound, nil)
		}
		got = call{method, id, reason, draft, dryRun}
		if reason == "refused" {
			return nil, apperr.New(apperr.CodeActivityNotFound, nil)
		}
		if reason == "unknown exercise" {
			return nil, apperr.New(apperr.CodeUnknownExercise, nil)
		}
		if reason == "" {
			return nil, apperr.InvalidFields(nil, "reason")
		}
		return []ChallengePreview{{ChallengeID: challengeID, Status: StatusOngoing}}, nil
	})
	h := NewHandler(NewService(&fakeRepo{}, nil, clock.NewFake(statsAt), buildinfo.Info{}), nil, nil, activities, nil)
	started := time.Date(2026, 6, 3, 11, 0, 0, 0, time.UTC)
	distance := 5000
	run := activity.Draft{SportCode: "run", StartedAt: started, DurationSeconds: 1500, DistanceMeters: &distance, Repetitions: []activity.Repetitions{{Exercise: "burpee", Count: 30}}}
	fields := `"sport_code": "run", "started_at": "2026-06-03T11:00:00Z", "duration_seconds": 1500, "distance_meters": 5000, "repetitions": [{"exercise": "burpee", "repetitions": 30}], `
	routes := []struct {
		name, method, target, body string
		want                       call
	}{
		{name: "record", method: http.MethodPost, target: "/api/v1/admin/users/" + bobID + "/activities", body: `{` + fields, want: call{method: "record", id: bobID, draft: run}},
		{name: "update", method: http.MethodPatch, target: "/api/v1/admin/activities/" + actionID, body: `{` + fields, want: call{method: "update", id: actionID, draft: run}},
		{name: "delete", method: http.MethodDelete, target: "/api/v1/admin/activities/" + actionID, body: `{`, want: call{method: "delete", id: actionID}},
	}
	for _, r := range routes {
		t.Run(r.name, func(t *testing.T) {
			got = call{}
			rec, body := serveWith(t, h, adminToken, r.method, r.target, r.body+`"reason": "Montre mal réglée", "dry_run": true}`, io.Discard)
			if rec.Code != http.StatusOK {
				t.Fatalf("got %d %v, want 200", rec.Code, body)
			}
			want := r.want
			want.reason, want.dryRun = "Montre mal réglée", true
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("asked %+v, want %+v", got, want)
			}
			if previews, _ := body["challenges"].([]any); body["dry_run"] != true || len(previews) != 1 {
				t.Fatalf("body %v, want the preview of the challenge", body)
			}

			rec, body = serveWith(t, h, "some.access.token", r.method, r.target, r.body+`"reason": "Montre mal réglée"}`, io.Discard)
			if e, _ := body["error"].(map[string]any); rec.Code != http.StatusNotFound || e["code"] != "not_found" {
				t.Fatalf("without the permission: got %d %v, want 404 not_found", rec.Code, body)
			}

			rec, body = serveWith(t, h, adminToken, r.method, r.target, r.body+`"reason": "refused"}`, io.Discard)
			if e, _ := body["error"].(map[string]any); rec.Code != http.StatusNotFound || e["code"] != "activity_not_found" {
				t.Fatalf("refused: got %d %v, want 404 activity_not_found", rec.Code, body)
			}

			if r.method != http.MethodDelete {
				rec, body = serveWith(t, h, adminToken, r.method, r.target, r.body+`"reason": "unknown exercise"}`, io.Discard)
				if e, _ := body["error"].(map[string]any); rec.Code != http.StatusUnprocessableEntity || e["code"] != "unknown_exercise" {
					t.Fatalf("unknown exercise: got %d %v, want 422 unknown_exercise", rec.Code, body)
				}
			}

			rec, body = serveWith(t, h, adminToken, r.method, r.target, r.body+`"reason": "", "dry_run": true}`, io.Discard)
			if e, _ := body["error"].(map[string]any); rec.Code != http.StatusBadRequest || e["code"] != "validation_failed" {
				t.Fatalf("empty reason: got %d %v, want 400 validation_failed", rec.Code, body)
			}
		})
	}
}

// TestCreateAdminActivityIdempotencyKey: the Idempotency-Key header reaches
// the change; one that gave an activity recorded before answers 200 with no
// challenge; a malformed one is refused, as not_found without the
// permission.
func TestCreateAdminActivityIdempotencyKey(t *testing.T) {
	var method string
	activities := activityUpdaterFunc(func(m string, _ auth.Actor, _ string, _ activity.Draft, _ string, _ bool) ([]ChallengePreview, error) {
		method = m
		return []ChallengePreview{}, nil
	})
	h := NewHandler(NewService(&fakeRepo{}, nil, clock.NewFake(statsAt), buildinfo.Info{}), nil, nil, activities, nil)
	target := "/api/v1/admin/users/" + bobID + "/activities"
	body := `{"sport_code": "run", "started_at": "2026-06-03T11:00:00Z", "duration_seconds": 1500, "distance_meters": 5000, "reason": "Saisie oubliée"}`

	rec, got := serveWith(t, h, adminToken, http.MethodPost, target, body, io.Discard, http.Header{"Idempotency-Key": {"k1"}})
	if previews, ok := got["challenges"].([]any); rec.Code != http.StatusOK || method != "record k1" || !ok || len(previews) != 0 || got["dry_run"] != false {
		t.Fatalf("got %d %v, asked %q; want 200 with no challenge, the key passed", rec.Code, got, method)
	}

	method = ""
	rec, got = serveWith(t, h, adminToken, http.MethodPost, target, body, io.Discard, http.Header{"Idempotency-Key": {"a key"}})
	if e, _ := got["error"].(map[string]any); rec.Code != http.StatusBadRequest || e["code"] != "validation_failed" || method != "" {
		t.Fatalf("malformed key: got %d %v, asked %q; want 400 validation_failed, nothing asked", rec.Code, got, method)
	}

	// Without the permission, a malformed key tells nothing of the route.
	rec, got = serveWith(t, h, "some.access.token", http.MethodPost, target, body, io.Discard, http.Header{"Idempotency-Key": {"a key"}})
	if e, _ := got["error"].(map[string]any); rec.Code != http.StatusNotFound || e["code"] != "not_found" || method != "" {
		t.Fatalf("malformed key without the permission: got %d %v, asked %q; want 404 not_found, nothing asked", rec.Code, got, method)
	}
}
