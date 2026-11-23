package admin

import (
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
)

const actionsPath = "/api/v1/admin/actions"

func TestActionsAreHiddenFromANonAdmin(t *testing.T) {
	for name, cursor := range map[string]string{"first page": "", "bad cursor": "!!"} {
		t.Run(name, func(t *testing.T) {
			repo := &fakeRepo{}
			_, err := newChallengeService(repo).Actions(t.Context(), auth.Actor{ID: "u1"}, ActionQuery{Limit: 20}, cursor)
			if !errors.Is(err, apperr.New(apperr.CodeNotFound, nil)) {
				t.Fatalf("got %v, want not_found", err)
			}
			if repo.calls != 0 {
				t.Fatalf("repository called %d times before the policy refused", repo.calls)
			}
		})
	}
}

func TestActions(t *testing.T) {
	rows := []Action{
		{ID: "a3", CreatedAt: listAt.Add(-time.Minute)},
		{ID: "a2", CreatedAt: listAt.Add(-2 * time.Minute)},
		{ID: "a1", CreatedAt: listAt.Add(-3 * time.Minute)},
	}
	after := Position{CreatedAt: listAt, ID: "{" + someUUID + "}"}
	tests := []struct {
		name      string
		q         ActionQuery
		cursor    string
		rows      []Action
		wantQuery ActionQuery
		wantIDs   []string
		wantNext  *Position
	}{
		{
			name: "a last page", q: ActionQuery{Limit: 3}, rows: rows,
			wantQuery: ActionQuery{Limit: 4}, wantIDs: []string{"a3", "a2", "a1"},
		},
		{
			name: "a page with a next one", q: ActionQuery{Limit: 2}, rows: rows,
			wantQuery: ActionQuery{Limit: 3}, wantIDs: []string{"a3", "a2"},
			wantNext: &Position{CreatedAt: rows[1].CreatedAt, ID: "a2"},
		},
		{
			name: "filters and cursor", q: ActionQuery{TargetType: TargetActivity, TargetID: otherUUID, AdminID: annID, ChallengeID: challengeID, Limit: 20},
			cursor: encoded(t, after),
			wantQuery: ActionQuery{
				TargetType: TargetActivity, TargetID: otherUUID, AdminID: annID, ChallengeID: challengeID, Limit: 21,
				After: &Position{CreatedAt: listAt, ID: someUUID},
			},
		},
		{
			name: "a limit below 1 gives one per page", q: ActionQuery{Limit: 0}, rows: rows,
			wantQuery: ActionQuery{Limit: 2}, wantIDs: []string{"a3"},
			wantNext: &Position{CreatedAt: rows[0].CreatedAt, ID: "a3"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{actions: tt.rows}
			page, err := newChallengeService(repo).Actions(t.Context(), auth.Actor{ID: "u1", Admin: true}, tt.q, tt.cursor)
			if err != nil {
				t.Fatal(err)
			}
			if q := repo.actionQuery; !reflect.DeepEqual(q, tt.wantQuery) {
				t.Errorf("query = %+v, want %+v", q, tt.wantQuery)
			}
			var gotIDs []string
			for _, a := range page.Items {
				gotIDs = append(gotIDs, a.ID)
			}
			if !reflect.DeepEqual(gotIDs, tt.wantIDs) {
				t.Errorf("items = %v, want %v", gotIDs, tt.wantIDs)
			}
			if !reflect.DeepEqual(page.Next, tt.wantNext) {
				t.Errorf("next = %+v, want %+v", page.Next, tt.wantNext)
			}
		})
	}
}

func TestActionsRefusesABadCursor(t *testing.T) {
	for name, cursor := range map[string]string{
		"not a cursor": "!!",
		"bad id":       encoded(t, Position{CreatedAt: listAt, ID: "nope"}),
		"no date":      encoded(t, Position{ID: someUUID}),
	} {
		t.Run(name, func(t *testing.T) {
			repo := &fakeRepo{}
			_, err := newChallengeService(repo).Actions(t.Context(), auth.Actor{ID: "u1", Admin: true}, ActionQuery{Limit: 20}, cursor)
			if !errors.Is(err, apperr.New(apperr.CodeValidationFailed, nil)) || fieldsOf(err) != "cursor" {
				t.Fatalf("got %v, want validation_failed on cursor", err)
			}
			if repo.calls != 0 {
				t.Fatalf("repository called %d times", repo.calls)
			}
		})
	}
}

func TestActionsRepositoryError(t *testing.T) {
	boom := errors.New("boom")
	_, err := newChallengeService(&fakeRepo{actionsErr: boom}).Actions(t.Context(), auth.Actor{ID: "u1", Admin: true}, ActionQuery{Limit: 20}, "")
	if !errors.Is(err, boom) {
		t.Fatalf("got %v, want %v", err, boom)
	}
}

func TestListAdminActionsWithoutThePermission(t *testing.T) {
	repo := &fakeRepo{}
	rec, body := serve(t, newChallengeService(repo), "some.access.token", actionsPath+"?admin_id="+annID)
	e, _ := body["error"].(map[string]any)
	if rec.Code != http.StatusNotFound || e["code"] != "not_found" {
		t.Fatalf("got %d %v, want 404 not_found", rec.Code, body)
	}
	if repo.calls != 0 {
		t.Fatalf("repository called %d times for a non-admin", repo.calls)
	}
}

func TestListAdminActions(t *testing.T) {
	ann := UserRef{ID: annID, Pseudo: ptr("Ann")}
	repo := &fakeRepo{actions: []Action{
		{
			ID: actionID, Admin: ann, CreatedAt: listAt, Action: "activity.update", TargetType: TargetActivity,
			TargetID: otherUUID, ChallengeIDs: []string{challengeID, inviteID},
			OldValue: []byte(`{"duration_seconds": 1800}`), NewValue: []byte(`{"duration_seconds": 3600}`), Reason: "Oubli",
		},
		{
			ID: inviteID, Admin: UserRef{ID: bobID}, CreatedAt: listAt.Add(-time.Minute), Action: "user.block",
			TargetType: TargetUser, TargetID: annID, ChallengeIDs: []string{}, Reason: "Abus",
		},
	}}
	params := url.Values{
		"target_type": {"activity"}, "target_id": {otherUUID}, "admin_id": {annID}, "challenge_id": {challengeID},
		"limit": {"1"}, "cursor": {encoded(t, Position{CreatedAt: listAt.Add(time.Hour), ID: someUUID})},
	}
	rec, got := serve(t, newChallengeService(repo), adminToken, actionsPath+"?"+params.Encode())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	q := repo.actionQuery
	if q.TargetType != TargetActivity || q.TargetID != otherUUID || q.AdminID != annID || q.ChallengeID != challengeID ||
		q.Limit != 2 || q.After == nil || q.After.ID != someUUID {
		t.Errorf("query = %+v, want the filters, limit and cursor of the request", q)
	}
	next, _ := got["next_cursor"].(string)
	if next != encoded(t, Position{CreatedAt: listAt, ID: actionID}) {
		t.Errorf("next_cursor = %q, want the position of the last item", next)
	}
	delete(got, "next_cursor")
	sameJSON(t, got, map[string]any{"items": []any{map[string]any{
		"action":        "activity.update",
		"admin":         map[string]any{"first_name": nil, "id": annID, "last_name": nil, "pseudo": "Ann"},
		"challenge_ids": []any{challengeID, inviteID},
		"created_at":    listAt.UTC().Format(time.RFC3339),
		"id":            actionID,
		"new_value":     map[string]any{"duration_seconds": 3600},
		"old_value":     map[string]any{"duration_seconds": 1800},
		"reason":        "Oubli",
		"target_id":     otherUUID,
		"target_type":   "activity",
	}}})
}

func TestListAdminActionsWithoutValues(t *testing.T) {
	repo := &fakeRepo{actions: []Action{{
		ID: actionID, Admin: UserRef{ID: bobID}, CreatedAt: listAt, Action: "user.block",
		TargetType: TargetUser, TargetID: annID, ChallengeIDs: []string{}, Reason: "Abus",
	}}}
	rec, got := serve(t, newChallengeService(repo), adminToken, actionsPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if q := repo.actionQuery; !reflect.DeepEqual(q, ActionQuery{Limit: defaultLimit + 1}) {
		t.Errorf("query = %+v, want no filter and the default limit", q)
	}
	items, _ := got["items"].([]any)
	item, _ := items[0].(map[string]any)
	if item["old_value"] != nil || item["new_value"] != nil || got["next_cursor"] != nil {
		t.Fatalf("body = %v, want null values and no next page", got)
	}
	if ids, _ := item["challenge_ids"].([]any); ids == nil || len(ids) != 0 {
		t.Fatalf("challenge_ids = %v, want []", item["challenge_ids"])
	}
}

func TestListAdminActionsRefusesABadFilter(t *testing.T) {
	for _, query := range []string{"target_type=team", "target_id=nope", "admin_id=nope", "challenge_id=nope", "limit=0", "limit=101"} {
		t.Run(query, func(t *testing.T) {
			repo := &fakeRepo{}
			rec, body := serve(t, newChallengeService(repo), adminToken, actionsPath+"?"+query)
			e, _ := body["error"].(map[string]any)
			if rec.Code != http.StatusBadRequest || e["code"] != "validation_failed" {
				t.Fatalf("got %d %v, want 400 validation_failed", rec.Code, body)
			}
			if repo.calls != 0 {
				t.Fatalf("repository called %d times", repo.calls)
			}
		})
	}
}
