package admin

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/buildinfo"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

var (
	adminActor = auth.Actor{ID: "u1", Admin: true}
	// listAt is the instant of the tests of the list: challenge c2 started
	// at it (ongoing), c3 ended at it (finished).
	listAt = statsAt
)

func newChallengeService(repo *fakeRepo) *Service {
	return NewService(repo, clock.NewFake(listAt), buildinfo.Info{})
}

func TestChallengeReadsAreHiddenFromANonAdmin(t *testing.T) {
	for name, read := range map[string]func(*Service) error{
		"list": func(s *Service) error {
			_, err := s.Challenges(t.Context(), auth.Actor{ID: "u1"}, ChallengeQuery{Limit: 20}, "")
			return err
		},
		"list with a bad cursor": func(s *Service) error {
			_, err := s.Challenges(t.Context(), auth.Actor{ID: "u1"}, ChallengeQuery{Limit: 20}, "!!")
			return err
		},
		"detail": func(s *Service) error {
			_, err := s.Challenge(t.Context(), auth.Actor{ID: "u1"}, "c1")
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

func TestChallenges(t *testing.T) {
	deleted := listAt.Add(-time.Hour)
	ann, bob := UserRef{ID: "ann", Pseudo: ptr("Ann")}, UserRef{ID: "bob"}
	rows := []ChallengeRow{
		{ID: "c1", StartsAt: listAt.Add(time.Second), EndsAt: listAt.AddDate(0, 0, 14), CreatedAt: listAt.Add(-time.Minute)},
		{ID: "c2", StartsAt: listAt, EndsAt: listAt.AddDate(0, 0, 14), CreatedAt: listAt.Add(-2 * time.Minute), DeletedAt: &deleted},
		{ID: "c3", StartsAt: listAt.AddDate(0, 0, -14), EndsAt: listAt, CreatedAt: listAt.Add(-3 * time.Minute)},
	}
	deletedOnly := true
	after := &Position{CreatedAt: listAt, ID: "c0"}
	q := ChallengeQuery{Search: "run", Status: StatusOngoing, Visibility: "private", Deleted: &deletedOnly, After: after}

	tests := []struct {
		name      string
		limit     int
		wantItems []ChallengeSummary
		wantNext  *Position
	}{
		{
			name:  "a next page: one more than asked was read",
			limit: 2,
			wantItems: []ChallengeSummary{
				{ChallengeRow: rows[0], Status: StatusUpcoming, Creators: []UserRef{ann, bob}},
				{ChallengeRow: rows[1], Status: StatusOngoing},
			},
			wantNext: &Position{CreatedAt: rows[1].CreatedAt, ID: "c2"},
		},
		{
			name:  "the last page",
			limit: 3,
			wantItems: []ChallengeSummary{
				{ChallengeRow: rows[0], Status: StatusUpcoming, Creators: []UserRef{ann, bob}},
				{ChallengeRow: rows[1], Status: StatusOngoing},
				{ChallengeRow: rows[2], Status: StatusFinished, Creators: []UserRef{ann}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{rows: rows, creators: map[string][]UserRef{"c1": {ann, bob}, "c3": {ann}}}
			q := q
			q.Limit = tt.limit
			got, err := newChallengeService(repo).Challenges(t.Context(), adminActor, q, "")
			if err != nil {
				t.Fatal(err)
			}
			wantQuery := q
			wantQuery.Limit, wantQuery.Now = tt.limit+1, listAt
			if !reflect.DeepEqual(repo.query, wantQuery) {
				t.Errorf("query = %+v, want %+v", repo.query, wantQuery)
			}
			wantIDs := []string{"c1", "c2", "c3"}[:len(tt.wantItems)]
			if !reflect.DeepEqual(repo.creatorIDs, wantIDs) {
				t.Errorf("creators read for %v, want %v", repo.creatorIDs, wantIDs)
			}
			want := ChallengePage{Items: tt.wantItems, Next: tt.wantNext}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Challenges() =\n%+v\nwant\n%+v", got, want)
			}
		})
	}
}

func TestChallengesLimitBelowOne(t *testing.T) {
	repo := &fakeRepo{rows: []ChallengeRow{{ID: "c1"}, {ID: "c2"}}}
	got, err := newChallengeService(repo).Challenges(t.Context(), adminActor, ChallengeQuery{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if repo.query.Limit != 2 || len(got.Items) != 1 || got.Next == nil || got.Next.ID != "c1" {
		t.Fatalf("Challenges() = %+v (asked %d), want one challenge and a next page", got, repo.query.Limit)
	}
}

func TestChallengesEmpty(t *testing.T) {
	got, err := newChallengeService(&fakeRepo{}).Challenges(t.Context(), adminActor, ChallengeQuery{Limit: 20}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 0 || got.Next != nil {
		t.Fatalf("Challenges() = %+v, want an empty last page", got)
	}
}

func TestChallengesStorageFailure(t *testing.T) {
	boom := errors.New("boom")
	for name, repo := range map[string]*fakeRepo{
		"list":     {listErr: boom},
		"creators": {rows: []ChallengeRow{{ID: "c1"}}, creatorsErr: boom},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := newChallengeService(repo).Challenges(t.Context(), adminActor, ChallengeQuery{Limit: 20}, "")
			if !errors.Is(err, boom) {
				t.Fatalf("Challenges() = %v, want it to wrap %v", err, boom)
			}
		})
	}
}

// A two-week challenge of America/Toronto from Monday 2026-11-09 (local
// midnight, 5 am UTC), ended at 5 am UTC on 2026-11-23. Week 1 locked at
// 7 am local on 2026-11-16 (12:00 UTC), week 2 at 7 am local on 2026-11-23,
// before statsAt (15:30 UTC).
var (
	detailStart = time.Date(2026, 11, 9, 5, 0, 0, 0, time.UTC)
	detailEnd   = time.Date(2026, 11, 23, 5, 0, 0, 0, time.UTC)
)

func TestChallenge(t *testing.T) {
	closed := time.Date(2026, 11, 16, 12, 5, 0, 0, time.UTC)
	frozen := time.Date(2026, 11, 23, 12, 6, 0, 0, time.UTC)
	revoked := detailStart
	ann := UserRef{ID: "ann", Pseudo: ptr("Ann")}
	active := InvitationRow{ID: "i1", InvitedBy: ann, CreatedAt: detailStart, ExpiresAt: statsAt.Add(time.Second), AuthorCanInvite: true}
	expired := InvitationRow{ID: "i2", InvitedBy: ann, CreatedAt: detailStart, ExpiresAt: statsAt, AuthorCanInvite: true}
	revokedInv := InvitationRow{ID: "i3", InvitedBy: ann, CreatedAt: detailStart, ExpiresAt: statsAt.Add(time.Hour), RevokedAt: &revoked}
	record := ChallengeRecord{ID: "c1", Timezone: "America/Toronto", StartsAt: detailStart, EndsAt: detailEnd}
	data := ChallengeData{
		Challenge:       record,
		Categories:      []CategoryRow{{Type: "cumulative_time", Position: 1, Params: []byte(`{}`)}},
		Creators:        []UserRef{ann},
		ClosedWeeks:     map[int]time.Time{1: closed},
		Eliminations:    []EliminationRow{{User: ann, WeekNumber: 2, DistanceMeters: 1000, RequiredMeters: 6000}},
		ResultsFrozenAt: &frozen,
		Invitations:     []InvitationRow{active, expired, revokedInv},
	}

	tests := []struct {
		name      string
		now       time.Time
		wantState string
		wantWeeks []Week
	}{
		{
			name:      "finished: both weeks locked, the last one not judged yet",
			now:       statsAt,
			wantState: StatusFinished,
			wantWeeks: []Week{
				{Number: 1, StartsAt: detailStart, EndsAt: detailStart.AddDate(0, 0, 7), LocksAt: closed.Add(-5 * time.Minute), Locked: true, ClosedAt: &closed},
				{Number: 2, StartsAt: detailStart.AddDate(0, 0, 7), EndsAt: detailEnd, LocksAt: time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC), Locked: true},
			},
		},
		{
			name:      "ongoing, just before week 1 locks",
			now:       time.Date(2026, 11, 16, 11, 59, 59, 0, time.UTC),
			wantState: StatusOngoing,
			wantWeeks: []Week{
				{Number: 1, StartsAt: detailStart, EndsAt: detailStart.AddDate(0, 0, 7), LocksAt: closed.Add(-5 * time.Minute), ClosedAt: &closed},
				{Number: 2, StartsAt: detailStart.AddDate(0, 0, 7), EndsAt: detailEnd, LocksAt: time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{data: data}
			got, err := NewService(repo, clock.NewFake(tt.now), buildinfo.Info{}).Challenge(t.Context(), adminActor, "c1")
			if err != nil {
				t.Fatal(err)
			}
			if repo.id != "c1" {
				t.Errorf("read challenge %q, want c1", repo.id)
			}
			statuses := []string{InvitationActive, InvitationExpired, InvitationRevoked}
			if tt.now.Before(statsAt) {
				// None has expired yet.
				statuses = []string{InvitationActive, InvitationActive, InvitationRevoked}
			}
			want := ChallengeDetail{
				Challenge:       record,
				Categories:      data.Categories,
				Status:          tt.wantState,
				Creators:        data.Creators,
				Weeks:           tt.wantWeeks,
				Eliminations:    data.Eliminations,
				ResultsFrozenAt: &frozen,
				Invitations: []Invitation{
					{InvitationRow: active, Status: statuses[0]},
					{InvitationRow: expired, Status: statuses[1]},
					{InvitationRow: revokedInv, Status: statuses[2]},
				},
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Challenge() =\n%+v\nwant\n%+v", got, want)
			}
		})
	}
}

func TestChallengeNotFound(t *testing.T) {
	repo := &fakeRepo{challengeErr: errChallengeNotFound}
	_, err := newChallengeService(repo).Challenge(t.Context(), adminActor, "c1")
	if !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("Challenge() = %v, want challenge_not_found", err)
	}
}

func TestChallengeFailures(t *testing.T) {
	boom := errors.New("boom")
	for name, repo := range map[string]*fakeRepo{
		"storage":   {challengeErr: boom},
		"time zone": {data: ChallengeData{Challenge: ChallengeRecord{Timezone: "Nowhere/Else"}}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := newChallengeService(repo).Challenge(t.Context(), adminActor, "c1")
			var appErr *apperr.Error
			if err == nil || errors.As(err, &appErr) {
				t.Fatalf("Challenge() = %v, want an internal error", err)
			}
			if name == "storage" && !errors.Is(err, boom) {
				t.Fatalf("Challenge() = %v, want it to wrap %v", err, boom)
			}
		})
	}
}

func TestInvitationStatus(t *testing.T) {
	revoked := listAt.Add(-time.Hour)
	tests := []struct {
		name string
		inv  InvitationRow
		want string
	}{
		{name: "valid", inv: InvitationRow{ExpiresAt: listAt.Add(time.Nanosecond), AuthorCanInvite: true}, want: InvitationActive},
		{name: "expires now", inv: InvitationRow{ExpiresAt: listAt, AuthorCanInvite: true}, want: InvitationExpired},
		{name: "its author may no longer invite", inv: InvitationRow{ExpiresAt: listAt.Add(time.Hour)}, want: InvitationCancelled},
		{name: "expired wins over cancelled", inv: InvitationRow{ExpiresAt: listAt}, want: InvitationExpired},
		{name: "revoked wins over expired", inv: InvitationRow{ExpiresAt: listAt.Add(-time.Hour), RevokedAt: &revoked}, want: InvitationRevoked},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := invitationStatus(tt.inv, listAt); got != tt.want {
				t.Fatalf("invitationStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStatusAt(t *testing.T) {
	start, end := listAt, listAt.AddDate(0, 0, 14)
	tests := []struct {
		now  time.Time
		want string
	}{
		{now: start.Add(-time.Nanosecond), want: StatusUpcoming},
		{now: start, want: StatusOngoing},
		{now: end.Add(-time.Nanosecond), want: StatusOngoing},
		{now: end, want: StatusFinished},
	}
	for _, tt := range tests {
		if got := statusAt(tt.now, start, end); got != tt.want {
			t.Errorf("statusAt(%v) = %q, want %q", tt.now, got, tt.want)
		}
	}
}

// TestChallengeWeeksAcrossATimeChange: the weeks of the detail are those of
// scoring.Window, in local days, locked from 7 am local the next day, the
// lock instant included. America/Toronto falls back on 2026-11-01: week 1
// (from Monday 2026-10-26, 4 am UTC) lasts 7 × 24 h + 1 h, and week 2 locks
// at 12:00 UTC, no longer 11:00.
func TestChallengeWeeksAcrossATimeChange(t *testing.T) {
	start := time.Date(2026, 10, 26, 4, 0, 0, 0, time.UTC)
	week2 := time.Date(2026, 11, 2, 5, 0, 0, 0, time.UTC)
	end := time.Date(2026, 11, 9, 5, 0, 0, 0, time.UTC)
	lock1 := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	lock2 := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	repo := &fakeRepo{data: ChallengeData{Challenge: ChallengeRecord{ID: "c1", Timezone: "America/Toronto", StartsAt: start, EndsAt: end}}}

	got, err := NewService(repo, clock.NewFake(lock1), buildinfo.Info{}).Challenge(t.Context(), adminActor, "c1")
	if err != nil {
		t.Fatal(err)
	}
	want := []Week{
		{Number: 1, StartsAt: start, EndsAt: week2, LocksAt: lock1, Locked: true},
		{Number: 2, StartsAt: week2, EndsAt: end, LocksAt: lock2},
	}
	if !reflect.DeepEqual(got.Weeks, want) {
		t.Fatalf("Weeks =\n%+v\nwant\n%+v", got.Weeks, want)
	}
}
