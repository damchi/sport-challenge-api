package admin

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/buildinfo"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/httpx"
)

const (
	challengeID = "5b1c3c2e-2d0f-4f8e-9a51-6f1f0c7a3d11"
	annID       = "0b9d4a5e-6a43-4c8e-9d1f-7a2b3c4d5e6f"
	bobID       = "7f3e2a1b-4c5d-4e6f-8a9b-0c1d2e3f4a5b"
	inviteID    = "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"
)

// sameJSON fails unless got and want marshal to the same JSON.
func sameJSON(t *testing.T, got, want any) {
	t.Helper()
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("body =\n%s\nwant\n%s", gotJSON, wantJSON)
	}
}

func TestAdminChallengeRoutesWithoutThePermission(t *testing.T) {
	for _, target := range []string{"/api/v1/admin/challenges", "/api/v1/admin/challenges/" + challengeID} {
		t.Run(target, func(t *testing.T) {
			repo := &fakeRepo{}
			rec, body := serve(t, newChallengeService(repo), "some.access.token", target)
			e, _ := body["error"].(map[string]any)
			if rec.Code != http.StatusNotFound || e["code"] != "not_found" {
				t.Fatalf("got %d %v, want 404 not_found", rec.Code, body)
			}
			if repo.calls != 0 {
				t.Fatalf("repository called %d times for a non-admin", repo.calls)
			}
		})
	}
}

func TestListAdminChallenges(t *testing.T) {
	deleted := listAt.Add(-time.Hour)
	repo := &fakeRepo{
		rows: []ChallengeRow{
			{ID: challengeID, Title: "Défi", Visibility: "private", Timezone: "America/Toronto", StartsAt: listAt, EndsAt: listAt.AddDate(0, 0, 14), CreatedAt: listAt.Add(-time.Minute), DeletedAt: &deleted, ParticipantCount: 3},
			{ID: bobID, Title: "Next", Visibility: "public", Timezone: "UTC", StartsAt: listAt, EndsAt: listAt.AddDate(0, 0, 14), CreatedAt: listAt.Add(-2 * time.Minute)},
		},
		creators: map[string][]UserRef{challengeID: {{ID: annID, Pseudo: ptr("Ann"), FirstName: ptr("Ann"), LastName: ptr("Lee")}, {ID: bobID}}},
	}
	after, err := httpx.EncodeCursor(ChallengePosition{CreatedAt: listAt, ID: "{" + inviteID + "}"})
	if err != nil {
		t.Fatal(err)
	}
	params := url.Values{
		"q": {"dé"}, "status": {"ongoing"}, "visibility": {"private"}, "deleted": {"true"},
		"limit": {"1"}, "cursor": {after},
	}
	rec, got := serve(t, newChallengeService(repo), adminToken, "/api/v1/admin/challenges?"+params.Encode())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	q := repo.query
	if q.Search != "dé" || q.Status != StatusOngoing || q.Visibility != "private" || q.Deleted == nil || !*q.Deleted || q.Limit != 2 {
		t.Errorf("query = %+v, want every filter of the request", q)
	}
	if q.After == nil || q.After.ID != inviteID || !q.After.CreatedAt.Equal(listAt) {
		t.Errorf("after = %+v, want the normalized position of the cursor", q.After)
	}
	next, err := httpx.EncodeCursor(ChallengePosition{CreatedAt: listAt.Add(-time.Minute), ID: challengeID})
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, got, map[string]any{
		"items": []any{map[string]any{
			"id": challengeID, "title": "Défi", "visibility": "private", "status": "ongoing",
			"timezone": "America/Toronto", "starts_at": "2026-11-23T15:30:00Z", "ends_at": "2026-12-07T15:30:00Z",
			"created_at": "2026-11-23T15:29:00Z", "deleted_at": "2026-11-23T14:30:00Z", "participant_count": 3,
			"creators": []any{
				map[string]any{"first_name": "Ann", "id": annID, "last_name": "Lee", "pseudo": "Ann"},
				map[string]any{"first_name": nil, "id": bobID, "last_name": nil, "pseudo": nil},
			},
		}},
		"next_cursor": next,
	})
}

func TestListAdminChallengesDefaults(t *testing.T) {
	repo := &fakeRepo{}
	rec, got := serve(t, newChallengeService(repo), adminToken, "/api/v1/admin/challenges")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	q := repo.query
	if q.Search != "" || q.Status != "" || q.Visibility != "" || q.Deleted != nil || q.After != nil || q.Limit != defaultLimit+1 {
		t.Errorf("query = %+v, want no filter and the default limit", q)
	}
	sameJSON(t, got, map[string]any{"items": []any{}, "next_cursor": nil})
}

func TestListAdminChallengesBadCursor(t *testing.T) {
	noDate, _ := httpx.EncodeCursor(ChallengePosition{ID: challengeID})
	badID, _ := httpx.EncodeCursor(ChallengePosition{CreatedAt: listAt, ID: "x"})
	for name, cursor := range map[string]string{"not base64": "!!", "no date": noDate, "bad id": badID} {
		t.Run(name, func(t *testing.T) {
			repo := &fakeRepo{}
			rec, body := serve(t, newChallengeService(repo), adminToken, "/api/v1/admin/challenges?cursor="+url.QueryEscape(cursor))
			e, _ := body["error"].(map[string]any)
			if rec.Code != http.StatusBadRequest || e["code"] != "validation_failed" {
				t.Fatalf("got %d %v, want 400 validation_failed", rec.Code, body)
			}
			if repo.calls != 0 {
				t.Fatalf("repository called %d times for a bad cursor", repo.calls)
			}
		})
	}
}

func TestGetAdminChallenge(t *testing.T) {
	closed := time.Date(2026, 11, 16, 12, 5, 0, 0, time.UTC)
	frozen := time.Date(2026, 11, 23, 12, 6, 0, 0, time.UTC)
	deleted := statsAt.Add(-time.Minute)
	ann := UserRef{ID: annID, Pseudo: ptr("Ann")}
	repo := &fakeRepo{data: ChallengeData{
		Challenge: ChallengeRecord{
			ID: challengeID, Title: "Défi", Description: ptr("Go"), Visibility: "private", MembersCanInvite: true,
			Timezone: "America/Toronto", ReferenceSport: "run", StartsAt: detailStart, EndsAt: detailEnd,
			CreatedAt: detailStart.Add(-time.Hour), DeletedAt: &deleted, ParticipantCount: 2,
		},
		Categories: []CategoryRow{
			{Type: "cumulative_time", Position: 1, Params: []byte(`{}`)},
			{Type: "elimination", Position: 3, Params: []byte(`{"sport": "run", "step_meters": 1000}`)},
			{Type: "points", Position: 4, Params: []byte(`{"criteria": ["longest_distance"], "podium": 5, "factors": {"run": 1, "ride": 0.3}}`)},
		},
		Creators:        []UserRef{ann},
		ClosedWeeks:     map[int]time.Time{1: closed},
		Eliminations:    []EliminationRow{{User: UserRef{ID: bobID, Pseudo: ptr("Bob")}, WeekNumber: 2, DistanceMeters: 1000, RequiredMeters: 6000}},
		ResultsFrozenAt: &frozen,
		Invitations:     []InvitationRow{{ID: inviteID, InvitedBy: ann, CreatedAt: detailStart, ExpiresAt: statsAt}},
	}}
	svc := NewService(repo, clock.NewFake(statsAt), buildinfo.Info{})

	rec, got := serve(t, svc, adminToken, "/api/v1/admin/challenges/"+challengeID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if repo.id != challengeID {
		t.Errorf("read challenge %q, want %s", repo.id, challengeID)
	}
	annJSON := map[string]any{"first_name": nil, "id": annID, "last_name": nil, "pseudo": "Ann"}
	sameJSON(t, got, map[string]any{
		"categories": []any{
			map[string]any{"type": "cumulative_time", "position": 1, "params": map[string]any{}},
			map[string]any{"type": "elimination", "position": 3, "params": map[string]any{"sport": "run", "step_meters": 1000}},
			map[string]any{"type": "points", "position": 4, "params": map[string]any{"criteria": []any{"longest_distance"}, "factors": map[string]any{"ride": 0.3, "run": 1}, "podium": 5}},
		},
		"created_at":  "2026-11-09T04:00:00Z",
		"creators":    []any{annJSON},
		"deleted_at":  "2026-11-23T15:29:00Z",
		"description": "Go",
		"eliminations": []any{map[string]any{
			"distance_meters": 1000, "required_meters": 6000,
			"user": map[string]any{"first_name": nil, "id": bobID, "last_name": nil, "pseudo": "Bob"}, "week_number": 2,
		}},
		"ends_at": "2026-11-23T05:00:00Z",
		"id":      challengeID,
		"invitations": []any{map[string]any{
			"created_at": "2026-11-09T05:00:00Z", "expires_at": "2026-11-23T15:30:00Z", "id": inviteID,
			"invited_by": annJSON, "revoked_at": nil, "status": "expired",
		}},
		"members_can_invite": true,
		"participant_count":  2,
		"reference_sport":    "run",
		"results_frozen_at":  "2026-11-23T12:06:00Z",
		"starts_at":          "2026-11-09T05:00:00Z",
		"status":             "finished",
		"timezone":           "America/Toronto",
		"title":              "Défi",
		"visibility":         "private",
		"weeks": []any{
			map[string]any{"closed_at": "2026-11-16T12:05:00Z", "ends_at": "2026-11-16T05:00:00Z", "locked": true, "locks_at": "2026-11-16T12:00:00Z", "number": 1, "starts_at": "2026-11-09T05:00:00Z"},
			map[string]any{"closed_at": nil, "ends_at": "2026-11-23T05:00:00Z", "locked": true, "locks_at": "2026-11-23T12:00:00Z", "number": 2, "starts_at": "2026-11-16T05:00:00Z"},
		},
	})
}

func TestGetAdminChallengeNotFound(t *testing.T) {
	repo := &fakeRepo{challengeErr: errChallengeNotFound}
	rec, body := serve(t, newChallengeService(repo), adminToken, "/api/v1/admin/challenges/"+challengeID)
	e, _ := body["error"].(map[string]any)
	if rec.Code != http.StatusNotFound || e["code"] != "challenge_not_found" {
		t.Fatalf("got %d %v, want 404 challenge_not_found", rec.Code, body)
	}
}
