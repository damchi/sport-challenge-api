package admin

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/activity"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/buildinfo"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/httpx"
)

const (
	someUUID  = "0b9d4a5e-6a43-4c8e-9d1f-7a2b3c4d5e6f"
	otherUUID = "7f3e2a1b-4c5d-4e6f-8a9b-0c1d2e3f4a5b"
)

// fakePlaces gives each activity the contributions and lock of byID, the
// account asked and its activities kept in user and asked; err fails it.
type fakePlaces struct {
	byID  map[string]activity.Recorded
	err   error
	user  string
	asked []activity.Activity
}

func (f *fakePlaces) AdminPlaces(_ context.Context, _ auth.Actor, userID string, as []activity.Activity) ([]activity.Recorded, error) {
	f.user, f.asked = userID, as
	if f.err != nil {
		return nil, f.err
	}
	out := make([]activity.Recorded, len(as))
	for i, a := range as {
		out[i] = activity.Recorded{Activity: a, Contributions: f.byID[a.ID].Contributions, Locked: f.byID[a.ID].Locked}
	}
	return out, nil
}

// encoded encodes position as a client sends it back.
func encoded(t *testing.T, position any) string {
	t.Helper()
	c, err := httpx.EncodeCursor(position)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestUserReadsAreHiddenFromANonAdmin(t *testing.T) {
	nonAdmin := auth.Actor{ID: "u1"}
	for name, read := range map[string]func(*Service) error{
		"list": func(s *Service) error {
			_, err := s.Users(t.Context(), nonAdmin, UserQuery{Limit: 20}, "")
			return err
		},
		"list with a bad cursor": func(s *Service) error {
			_, err := s.Users(t.Context(), nonAdmin, UserQuery{Limit: 20}, "!!")
			return err
		},
		"account": func(s *Service) error {
			_, err := s.User(t.Context(), nonAdmin, someUUID)
			return err
		},
		"challenges": func(s *Service) error {
			_, err := s.UserChallenges(t.Context(), nonAdmin, someUUID, 20, "")
			return err
		},
		"challenges with a bad cursor": func(s *Service) error {
			_, err := s.UserChallenges(t.Context(), nonAdmin, someUUID, 20, "!!")
			return err
		},
		"activities": func(s *Service) error {
			_, err := s.UserActivities(t.Context(), nonAdmin, someUUID, 20, "")
			return err
		},
		"activities with a bad cursor": func(s *Service) error {
			_, err := s.UserActivities(t.Context(), nonAdmin, someUUID, 20, "!!")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			repo := &fakeRepo{}
			if err := read(newChallengeService(repo)); !errors.Is(err, apperr.New(apperr.CodeNotFound, nil)) {
				t.Fatalf("got %v, want not_found", err)
			}
			if repo.calls != 0 {
				t.Fatalf("repository called %d times before the policy refused", repo.calls)
			}
		})
	}
}

func TestUsers(t *testing.T) {
	rows := []UserRow{
		{ID: "u3", CreatedAt: listAt.Add(-time.Minute)},
		{ID: "u2", CreatedAt: listAt.Add(-2 * time.Minute)},
		{ID: "u1", CreatedAt: listAt.Add(-3 * time.Minute)},
	}
	after := Position{CreatedAt: listAt, ID: "{" + someUUID + "}"}
	tests := []struct {
		name      string
		limit     int
		cursor    string
		wantLimit int
		wantAfter *Position
		wantItems []UserRow
		wantNext  *Position
	}{
		{name: "first page, more to come", limit: 2, wantLimit: 3, wantItems: rows[:2], wantNext: &Position{CreatedAt: rows[1].CreatedAt, ID: "u2"}},
		{name: "last page", limit: 3, wantLimit: 4, wantItems: rows},
		{name: "after a cursor, normalized", limit: 3, cursor: encoded(t, after), wantLimit: 4, wantAfter: &Position{CreatedAt: listAt, ID: someUUID}, wantItems: rows},
		{name: "a limit below 1 gives one per page", limit: 0, wantLimit: 2, wantItems: rows[:1], wantNext: &Position{CreatedAt: rows[0].CreatedAt, ID: "u3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{users: rows}
			got, err := newChallengeService(repo).Users(t.Context(), adminActor, UserQuery{Search: "ann@", Limit: tt.limit}, tt.cursor)
			if err != nil {
				t.Fatal(err)
			}
			q := repo.userQuery
			if q.Search != "ann@" || q.Limit != tt.wantLimit || !reflect.DeepEqual(q.After, tt.wantAfter) {
				t.Errorf("query = %+v, want search ann@, limit %d, after %+v", q, tt.wantLimit, tt.wantAfter)
			}
			// The fake gives every row whatever the limit: the service cuts.
			want := UserPage{Items: tt.wantItems, Next: tt.wantNext}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Users() =\n%+v\nwant\n%+v", got, want)
			}
		})
	}
}

func TestUser(t *testing.T) {
	u := UserRow{ID: someUUID, Email: ptr("ann@example.com"), State: UserActive}
	repo := &fakeRepo{user: u}
	got, err := newChallengeService(repo).User(t.Context(), adminActor, someUUID)
	if err != nil || !reflect.DeepEqual(got, u) || repo.id != someUUID {
		t.Fatalf("User() = %+v, %v (asked %q), want %+v", got, err, repo.id, u)
	}
}

func TestUserChallenges(t *testing.T) {
	creator := UserChallengeRow{ID: "c1", StartsAt: listAt.Add(time.Second), EndsAt: listAt.AddDate(0, 0, 14), CreatedAt: listAt.Add(-time.Minute), IsCreator: true}
	abandoned := UserChallengeRow{
		ID: "c2", StartsAt: listAt.AddDate(0, 0, -14), EndsAt: listAt, CreatedAt: listAt.Add(-2 * time.Minute),
		ParticipantStatus: ptr("abandoned"), JoinedAt: ptr(listAt.AddDate(0, 0, -14)), AbandonedAt: ptr(listAt.AddDate(0, 0, -7)),
	}
	ongoing := UserChallengeRow{ID: "c3", StartsAt: listAt, EndsAt: listAt.AddDate(0, 0, 14), CreatedAt: listAt.Add(-3 * time.Minute)}
	repo := &fakeRepo{userChallenges: []UserChallengeRow{creator, abandoned, ongoing}}
	after := Position{CreatedAt: listAt, ID: someUUID}

	got, err := newChallengeService(repo).UserChallenges(t.Context(), adminActor, otherUUID, 2, encoded(t, after))
	if err != nil {
		t.Fatal(err)
	}
	if repo.id != otherUUID || repo.pageQuery.Limit != 3 || !reflect.DeepEqual(repo.pageQuery.After, &after) {
		t.Errorf("asked %q with %+v, want the account, limit 3 and the cursor", repo.id, repo.pageQuery)
	}
	want := UserChallengePage{
		Items: []UserChallenge{
			{UserChallengeRow: creator, Status: StatusUpcoming},
			{UserChallengeRow: abandoned, Status: StatusFinished},
		},
		Next: &Position{CreatedAt: abandoned.CreatedAt, ID: "c2"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("UserChallenges() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestUserActivities(t *testing.T) {
	rows := []ActivityRow{
		{ID: "a2", StartedAt: listAt.Add(-time.Hour)},
		{ID: "a1", StartedAt: listAt.Add(-2 * time.Hour)},
	}
	after := ActivityPosition{StartedAt: listAt, ID: "urn:uuid:" + someUUID}

	repo := &fakeRepo{activities: rows}
	two := []activity.Contribution{{ChallengeID: "c1", DurationSeconds: 60}, {ChallengeID: "c2", DurationSeconds: 60}}
	places := &fakePlaces{byID: map[string]activity.Recorded{"a2": {Contributions: two, Locked: true}}}
	got, err := NewService(repo, places, clock.NewFake(listAt), buildinfo.Info{}).UserActivities(t.Context(), adminActor, otherUUID, 1, encoded(t, after))
	if err != nil {
		t.Fatal(err)
	}
	if repo.id != otherUUID || repo.activityQuery.Limit != 2 || !reflect.DeepEqual(repo.activityQuery.After, &ActivityPosition{StartedAt: listAt, ID: someUUID}) {
		t.Errorf("asked %q with %+v, want the account, limit 2 and the normalized cursor", repo.id, repo.activityQuery)
	}
	if places.user != otherUUID || len(places.asked) != 1 || places.asked[0].ID != "a2" || places.asked[0].UserID != otherUUID || !places.asked[0].StartedAt.Equal(rows[0].StartedAt) {
		t.Errorf("places asked for %q: %+v, want the page's activity of the account", places.user, places.asked)
	}
	want := ActivityPage{Items: []UserActivity{{ActivityRow: rows[0], Contributions: two, Locked: true}}, Next: &ActivityPosition{StartedAt: rows[0].StartedAt, ID: "a2"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("UserActivities() =\n%+v\nwant\n%+v", got, want)
	}

	repo = &fakeRepo{activities: rows}
	got, err = newChallengeService(repo).UserActivities(t.Context(), adminActor, otherUUID, 2, "")
	if err != nil || got.Next != nil || len(got.Items) != 2 || repo.activityQuery.After != nil {
		t.Fatalf("last page = %+v, %v (after %+v), want both activities and no next page", got, err, repo.activityQuery.After)
	}

	storage := errors.New("connection reset")
	_, err = NewService(&fakeRepo{activities: rows}, &fakePlaces{err: storage}, clock.NewFake(listAt), buildinfo.Info{}).UserActivities(t.Context(), adminActor, otherUUID, 2, "")
	if !errors.Is(err, storage) {
		t.Fatalf("places fail: error = %v, want the cause kept", err)
	}
}

func TestUserReadsOfAnUnknownAccount(t *testing.T) {
	repo := &fakeRepo{userErr: errUserNotFound, userChallengesErr: errUserNotFound, activitiesErr: errUserNotFound}
	s := newChallengeService(repo)
	for name, read := range map[string]func() error{
		"account": func() error {
			_, err := s.User(t.Context(), adminActor, someUUID)
			return err
		},
		"challenges": func() error {
			_, err := s.UserChallenges(t.Context(), adminActor, someUUID, 20, "")
			return err
		},
		"activities": func() error {
			_, err := s.UserActivities(t.Context(), adminActor, someUUID, 20, "")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := read(); !errors.Is(err, apperr.New(apperr.CodeUserNotFound, nil)) {
				t.Fatalf("got %v, want user_not_found", err)
			}
		})
	}
}

func TestUserReadsStorageFailure(t *testing.T) {
	boom := errors.New("boom")
	repo := &fakeRepo{usersErr: boom, userErr: boom, userChallengesErr: boom, activitiesErr: boom}
	s := newChallengeService(repo)
	for name, read := range map[string]func() error{
		"list": func() error {
			_, err := s.Users(t.Context(), adminActor, UserQuery{Limit: 20}, "")
			return err
		},
		"account": func() error {
			_, err := s.User(t.Context(), adminActor, someUUID)
			return err
		},
		"challenges": func() error {
			_, err := s.UserChallenges(t.Context(), adminActor, someUUID, 20, "")
			return err
		},
		"activities": func() error {
			_, err := s.UserActivities(t.Context(), adminActor, someUUID, 20, "")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := read()
			var appErr *apperr.Error
			if !errors.Is(err, boom) || errors.As(err, &appErr) {
				t.Fatalf("got %v, want it to wrap %v, as a 500", err, boom)
			}
		})
	}
}

// TestBadCursors: a cursor from the client that is not a position answers
// 400 validation_failed on the field cursor, before anything is read.
func TestBadCursors(t *testing.T) {
	cursors := map[string]string{
		"not base64":     "!!",
		"not a uuid":     encoded(t, map[string]any{"created_at": listAt, "started_at": listAt, "id": "nope"}),
		"without a date": encoded(t, map[string]any{"id": someUUID}),
		"JSON null":      encoded(t, nil),
	}
	reads := map[string]func(*Service, string) error{
		"users": func(s *Service, c string) error {
			_, err := s.Users(t.Context(), adminActor, UserQuery{Limit: 20}, c)
			return err
		},
		"challenges": func(s *Service, c string) error {
			_, err := s.UserChallenges(t.Context(), adminActor, someUUID, 20, c)
			return err
		},
		"activities": func(s *Service, c string) error {
			_, err := s.UserActivities(t.Context(), adminActor, someUUID, 20, c)
			return err
		},
	}
	for readName, read := range reads {
		for name, cursor := range cursors {
			t.Run(readName+"/"+name, func(t *testing.T) {
				repo := &fakeRepo{}
				err := read(newChallengeService(repo), cursor)
				var appErr *apperr.Error
				if !errors.As(err, &appErr) || appErr.Code != apperr.CodeValidationFailed || appErr.Params[apperr.FieldsParam] != "cursor" {
					t.Fatalf("got %v, want validation_failed on cursor", err)
				}
				if repo.calls != 0 {
					t.Fatalf("repository called %d times with a bad cursor", repo.calls)
				}
			})
		}
	}
}
