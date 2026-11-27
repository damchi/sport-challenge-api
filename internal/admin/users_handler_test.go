package admin

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/activity"
	"github.com/damchi/sport-challenge-api/internal/platform/buildinfo"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

const (
	annEmail = "ann.lee@example.com"
	userPath = "/api/v1/admin/users/" + annID
)

const searchPath = "/api/v1/admin/users/search"

// post serves POST target with body as serve does.
func post(t *testing.T, svc *Service, token, target, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	return serveLogged(t, svc, token, http.MethodPost, target, body, io.Discard)
}

func TestAdminUserRoutesWithoutThePermission(t *testing.T) {
	for _, r := range []struct{ method, target, body string }{
		{http.MethodGet, "/api/v1/admin/users", ""},
		{http.MethodPost, searchPath, `{"q": "ann"}`},
		{http.MethodPost, searchPath, ""},
		{http.MethodGet, userPath, ""},
		{http.MethodGet, userPath + "/challenges", ""},
		{http.MethodGet, userPath + "/activities", ""},
	} {
		t.Run(r.method+" "+r.target+" "+r.body, func(t *testing.T) {
			repo := &fakeRepo{}
			rec, body := serveLogged(t, newChallengeService(repo), "some.access.token", r.method, r.target, r.body, io.Discard)
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

func TestListAdminUsers(t *testing.T) {
	repo := &fakeRepo{users: []UserRow{{ID: annID, State: UserActive, CreatedAt: listAt}}}
	params := url.Values{"limit": {"5"}, "cursor": {encoded(t, Position{CreatedAt: listAt, ID: inviteID})}}
	rec, got := serve(t, newChallengeService(repo), adminToken, "/api/v1/admin/users?"+params.Encode())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	q := repo.userQuery
	if q.Search != "" || q.Limit != 6 || q.After == nil || q.After.ID != inviteID {
		t.Errorf("query = %+v, want no search, the limit and cursor of the request", q)
	}
	if items, _ := got["items"].([]any); len(items) != 1 || got["next_cursor"] != nil {
		t.Fatalf("body = %v, want one account and no next page", got)
	}
}

func TestSearchAdminUsers(t *testing.T) {
	deleted := listAt.Add(-time.Hour)
	lastActivity := listAt.Add(-2 * time.Hour)
	repo := &fakeRepo{users: []UserRow{
		{
			ID: annID, Pseudo: ptr("ann_1"), FirstName: ptr("Ann"), LastName: ptr("Lee"), Email: ptr(annEmail),
			State: UserActive, CreatedAt: listAt.Add(-time.Minute), LastActivityAt: &lastActivity, ChallengeCount: 2,
		},
		{ID: bobID, State: UserAuth0DeletionPending, CreatedAt: listAt.Add(-2 * time.Minute), DeletedAt: &deleted},
	}}
	body := `{"q": "` + annEmail + `", "limit": 1, "cursor": "` + encoded(t, Position{CreatedAt: listAt, ID: inviteID}) + `"}`
	rec, got := post(t, newChallengeService(repo), adminToken, searchPath, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	q := repo.userQuery
	if q.Search != annEmail || q.Limit != 2 || q.After == nil || q.After.ID != inviteID {
		t.Errorf("query = %+v, want the search, limit and cursor of the body", q)
	}
	sameJSON(t, got, map[string]any{
		"items": []any{map[string]any{
			"id": annID, "pseudo": "ann_1", "first_name": "Ann", "last_name": "Lee", "email": annEmail,
			"state": "active", "created_at": "2026-11-23T15:29:00Z", "deleted_at": nil,
			"last_activity_at": "2026-11-23T13:30:00Z", "challenge_count": 2.0,
		}},
		"next_cursor": encoded(t, Position{CreatedAt: listAt.Add(-time.Minute), ID: annID}),
	})
}

func TestSearchAdminUsersWithoutSearch(t *testing.T) {
	for name, body := range map[string]string{"no body": "", "empty body": `{}`} {
		t.Run(name, func(t *testing.T) {
			repo := &fakeRepo{}
			rec, _ := post(t, newChallengeService(repo), adminToken, searchPath, body)
			if rec.Code != http.StatusOK || repo.userQuery.Search != "" || repo.userQuery.Limit != defaultLimit+1 || repo.userQuery.After != nil {
				t.Fatalf("status = %d, query %+v, want every account, a default page", rec.Code, repo.userQuery)
			}
		})
	}
}

func TestSearchAdminUsersInvalidBody(t *testing.T) {
	for name, body := range map[string]string{
		"empty search":  `{"q": ""}`,
		"unknown field": `{"email": "x@y.z"}`,
		"limit too big": `{"limit": 101}`,
	} {
		t.Run(name, func(t *testing.T) {
			repo := &fakeRepo{}
			rec, got := post(t, newChallengeService(repo), adminToken, searchPath, body)
			e, _ := got["error"].(map[string]any)
			if rec.Code != http.StatusBadRequest || e["code"] != "validation_failed" || repo.calls != 0 {
				t.Fatalf("got %d %v (%d reads), want 400 validation_failed before any read", rec.Code, got, repo.calls)
			}
		})
	}
}

func TestGetAdminUser(t *testing.T) {
	deleted := listAt.Add(-time.Hour)
	repo := &fakeRepo{user: UserRow{ID: annID, State: UserDeleted, CreatedAt: listAt.Add(-time.Minute), DeletedAt: &deleted}}
	rec, got := serve(t, newChallengeService(repo), adminToken, userPath)
	if rec.Code != http.StatusOK || repo.id != annID {
		t.Fatalf("status = %d (asked %q), body %s", rec.Code, repo.id, rec.Body.String())
	}
	sameJSON(t, got, map[string]any{
		"id": annID, "pseudo": nil, "first_name": nil, "last_name": nil, "email": nil,
		"state": "deleted", "created_at": "2026-11-23T15:29:00Z", "deleted_at": "2026-11-23T14:30:00Z",
		"last_activity_at": nil, "challenge_count": 0.0,
	})
}

func TestAdminUserRoutesOfAnUnknownAccount(t *testing.T) {
	for _, target := range []string{userPath, userPath + "/challenges", userPath + "/activities"} {
		t.Run(target, func(t *testing.T) {
			repo := &fakeRepo{userErr: errUserNotFound, userChallengesErr: errUserNotFound, activitiesErr: errUserNotFound}
			rec, body := serve(t, newChallengeService(repo), adminToken, target)
			e, _ := body["error"].(map[string]any)
			if rec.Code != http.StatusNotFound || e["code"] != "user_not_found" {
				t.Fatalf("got %d %v, want 404 user_not_found", rec.Code, body)
			}
		})
	}
}

func TestListAdminUserChallenges(t *testing.T) {
	joined, abandoned, deleted := listAt.AddDate(0, 0, -14), listAt.AddDate(0, 0, -7), listAt.Add(-time.Hour)
	repo := &fakeRepo{userChallenges: []UserChallengeRow{
		{
			ID: challengeID, Title: "Défi", Visibility: "private", Timezone: "America/Toronto", StartsAt: joined, EndsAt: listAt, CreatedAt: listAt.Add(-time.Minute),
			DeletedAt: &deleted, IsCreator: true, ParticipantStatus: ptr("abandoned"), JoinedAt: &joined, AbandonedAt: &abandoned,
		},
		{ID: bobID, Title: "Créé", Visibility: "public", Timezone: "Europe/Paris", StartsAt: listAt.Add(time.Hour), EndsAt: listAt.AddDate(0, 0, 14), CreatedAt: listAt.Add(-2 * time.Minute), IsCreator: true},
	}}
	rec, got := serve(t, newChallengeService(repo), adminToken, userPath+"/challenges?limit=5")
	if rec.Code != http.StatusOK || repo.id != annID || repo.pageQuery.Limit != 6 {
		t.Fatalf("status = %d (asked %q, %+v), body %s", rec.Code, repo.id, repo.pageQuery, rec.Body.String())
	}
	sameJSON(t, got, map[string]any{
		"items": []any{
			map[string]any{
				"id": challengeID, "title": "Défi", "visibility": "private", "timezone": "America/Toronto", "status": "finished",
				"starts_at": "2026-11-09T15:30:00Z", "ends_at": "2026-11-23T15:30:00Z", "created_at": "2026-11-23T15:29:00Z",
				"deleted_at": "2026-11-23T14:30:00Z", "is_creator": true, "participant_status": "abandoned",
				"joined_at": "2026-11-09T15:30:00Z", "abandoned_at": "2026-11-16T15:30:00Z",
			},
			map[string]any{
				"id": bobID, "title": "Créé", "visibility": "public", "timezone": "Europe/Paris", "status": "upcoming",
				"starts_at": "2026-11-23T16:30:00Z", "ends_at": "2026-12-07T15:30:00Z", "created_at": "2026-11-23T15:28:00Z",
				"deleted_at": nil, "is_creator": true, "participant_status": nil, "joined_at": nil, "abandoned_at": nil,
			},
		},
		"next_cursor": nil,
	})
}

func TestListAdminUserActivities(t *testing.T) {
	started := listAt.Add(-3 * time.Hour)
	repo := &fakeRepo{activities: []ActivityRow{
		{
			ID: inviteID, SportCode: "run", StartedAt: started, DurationSeconds: 1800, DistanceMeters: ptr(5000), ElevationGainMeters: ptr(40), Source: "manual", CreatedAt: listAt.Add(-time.Hour),
			AdminChange: &ActivityChange{Action: ActivityCreated, At: listAt.Add(-time.Hour), AdminID: bobID},
		},
		{ID: bobID, SportCode: "other", SportLabel: ptr("Escalade"), StartedAt: started.Add(-time.Hour), DurationSeconds: 600, Source: "manual", CreatedAt: listAt},
		{
			ID: challengeID, SportCode: "run", StartedAt: started.Add(-2 * time.Hour), DurationSeconds: 600, Source: "manual", CreatedAt: listAt,
			AdminChange: &ActivityChange{Action: ActivityUpdated, At: listAt, AdminID: bobID},
		},
		{ID: annID, SportCode: "run", StartedAt: started.Add(-3 * time.Hour), DurationSeconds: 600, Source: "manual", CreatedAt: listAt},
	}}
	timeOnly := []activity.Contribution{
		{ChallengeID: challengeID, ChallengeTitle: "Défi", DistanceMeters: ptr(5000), DurationSeconds: 1800},
		{ChallengeID: bobID, ChallengeTitle: "Dénivelé", DurationSeconds: 1800},
	}
	places := &fakePlaces{byID: map[string]activity.Recorded{inviteID: {Contributions: timeOnly, Locked: true}}}
	rec, got := serve(t, NewService(repo, places, clock.NewFake(listAt), buildinfo.Info{}), adminToken, userPath+"/activities?limit=3")
	if rec.Code != http.StatusOK || repo.id != annID || repo.activityQuery.Limit != 4 {
		t.Fatalf("status = %d (asked %q, %+v), body %s", rec.Code, repo.id, repo.activityQuery, rec.Body.String())
	}
	sameJSON(t, got, map[string]any{
		"items": []any{map[string]any{
			"id": inviteID, "sport_code": "run", "sport_label": nil, "started_at": "2026-11-23T12:30:00Z",
			"duration_seconds": 1800.0, "distance_meters": 5000.0, "elevation_gain_meters": 40.0,
			"source": "manual", "created_at": "2026-11-23T14:30:00Z", "locked": true,
			// Computed as for Activity: 5 km in 30 min.
			"speed_mps": 5000.0 / 1800, "pace_seconds_per_km": 360.0,
			"contributions": []any{
				map[string]any{"challenge_id": challengeID, "challenge_title": "Défi", "distance_meters": 5000.0, "duration_seconds": 1800.0},
				map[string]any{"challenge_id": bobID, "challenge_title": "Dénivelé", "distance_meters": nil, "duration_seconds": 1800.0},
			},
			// The admin by its id only.
			"admin_change": map[string]any{"action": "created", "at": "2026-11-23T14:30:00Z", "admin_id": bobID},
		}, map[string]any{
			"id": bobID, "sport_code": "other", "sport_label": "Escalade", "started_at": "2026-11-23T11:30:00Z",
			"duration_seconds": 600.0, "distance_meters": nil, "elevation_gain_meters": nil,
			"source": "manual", "created_at": "2026-11-23T15:30:00Z", "locked": false, "contributions": []any{},
			// No distance, no speed nor pace.
			"speed_mps": nil, "pace_seconds_per_km": nil,
			"admin_change": nil,
		}, map[string]any{
			"id": challengeID, "sport_code": "run", "sport_label": nil, "started_at": "2026-11-23T10:30:00Z",
			"duration_seconds": 600.0, "distance_meters": nil, "elevation_gain_meters": nil,
			"source": "manual", "created_at": "2026-11-23T15:30:00Z", "locked": false, "contributions": []any{},
			"speed_mps": nil, "pace_seconds_per_km": nil,
			"admin_change": map[string]any{"action": "updated", "at": "2026-11-23T15:30:00Z", "admin_id": bobID},
		}},
		"next_cursor": encoded(t, ActivityPosition{StartedAt: started.Add(-2 * time.Hour), ID: challengeID}),
	})
}

// TestAdminUserRoutesLogNoEmail: the e-mail of an account is in the answers
// to an admin only, never in the logs of the API, whether it is the search
// of the request (in the body: the platform logs the URL) or in the answer,
// nor when a request fails or is refused.
func TestAdminUserRoutesLogNoEmail(t *testing.T) {
	withEmail := UserRow{ID: annID, Email: ptr(annEmail), State: UserActive, CreatedAt: listAt}
	search := `{"q": "` + annEmail + `"}`
	tests := []struct {
		name         string
		repo         *fakeRepo
		method       string
		target, body string
		wantStatus   int
	}{
		{name: "search by e-mail", repo: &fakeRepo{users: []UserRow{withEmail}}, method: http.MethodPost, target: searchPath, body: search, wantStatus: http.StatusOK},
		{name: "list", repo: &fakeRepo{users: []UserRow{withEmail}}, method: http.MethodGet, target: "/api/v1/admin/users", wantStatus: http.StatusOK},
		{name: "account", repo: &fakeRepo{user: withEmail}, method: http.MethodGet, target: userPath, wantStatus: http.StatusOK},
		{name: "failed search by e-mail", repo: &fakeRepo{usersErr: errors.New("connection reset")}, method: http.MethodPost, target: searchPath, body: search, wantStatus: http.StatusInternalServerError},
		{name: "invalid search by e-mail", repo: &fakeRepo{}, method: http.MethodPost, target: searchPath, body: `{"q": "` + annEmail + `", "limit": 0}`, wantStatus: http.StatusBadRequest},
		{name: "unknown account", repo: &fakeRepo{userErr: errUserNotFound}, method: http.MethodGet, target: userPath, wantStatus: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			rec, _ := serveLogged(t, newChallengeService(tt.repo), adminToken, tt.method, tt.target, tt.body, &logs)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if !strings.Contains(logs.String(), `"request_id"`) {
				t.Fatalf("request not logged: %q", logs.String())
			}
			// The failure is logged with its cause, the e-mail aside.
			if tt.wantStatus == http.StatusInternalServerError && !strings.Contains(logs.String(), "connection reset") {
				t.Fatalf("failure not logged with its cause:\n%s", logs.String())
			}
			for _, secret := range []string{annEmail, url.QueryEscape(annEmail), "example.com"} {
				if strings.Contains(logs.String(), secret) {
					t.Fatalf("logs hold %q:\n%s", secret, logs.String())
				}
			}
		})
	}
}
