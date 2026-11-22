//go:build integration

package challenge

import (
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
)

// listFixture holds two users and challenges around now (2026-03-04 15:00
// UTC), inserted directly to control their dates.
type listFixture struct {
	pool      *pgxpool.Pool
	svc       *Service
	me, other string
}

func newListFixture(t *testing.T) listFixture {
	t.Helper()
	pool := testdb.New(t)
	gdb, err := db.NewGorm(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	f := listFixture{
		pool:  pool,
		svc:   NewService(NewRepository(gdb), nil, nil, fakeStandings{}, clock.NewFake(now)),
		me:    insertTestUser(t, pool, "email|me"),
		other: insertTestUser(t, pool, "email|other"),
	}
	day := 24 * time.Hour
	// Fixed ids make the order of challenges starting at the same instant
	// known: by id, ascending or descending like the dates.
	type row struct {
		id, title, visibility string
		start, end            time.Duration
		creator               string
		deleted               bool
		participants          map[string]ParticipantStatus
	}
	rows := []row{
		{id: "00000000-0000-0000-0000-000000000010", title: "G finished long ago", visibility: "public", start: -30 * day, end: -16 * day, creator: f.other},
		{id: "00000000-0000-0000-0000-0000000000e2", title: "E2 finished, same start as E", visibility: "public", start: -15 * day, end: -1 * day, creator: f.other},
		{id: "00000000-0000-0000-0000-0000000000e1", title: "E finished", visibility: "public", start: -15 * day, end: -1 * day, creator: f.other},
		{
			id: "00000000-0000-0000-0000-0000000000b0", title: "B ongoing, mine", visibility: "public", start: -1 * day, end: 13 * day, creator: f.other,
			participants: map[string]ParticipantStatus{f.me: ParticipantActive, f.other: ParticipantAbandoned},
		},
		{id: "00000000-0000-0000-0000-0000000000c0", title: "C private of another", visibility: "private", start: -1 * day, end: 13 * day, creator: f.other},
		{
			id: "00000000-0000-0000-0000-0000000000f0", title: "F deleted", visibility: "public", start: -1 * day, end: 13 * day, creator: f.me, deleted: true,
			participants: map[string]ParticipantStatus{f.me: ParticipantActive},
		},
		{id: "00000000-0000-0000-0000-0000000000a1", title: "A upcoming", visibility: "public", start: 2 * day, end: 16 * day, creator: f.other},
		{id: "00000000-0000-0000-0000-0000000000a2", title: "H upcoming, same start as A", visibility: "public", start: 2 * day, end: 16 * day, creator: f.other},
		{
			id: "00000000-0000-0000-0000-0000000000d0", title: "D private, I abandoned", visibility: "private", start: 3 * day, end: 17 * day, creator: f.other,
			participants: map[string]ParticipantStatus{f.me: ParticipantAbandoned},
		},
	}
	for _, r := range rows {
		var id string
		var deletedAt *time.Time
		if r.deleted {
			deletedAt = &now
		}
		if err := pool.QueryRow(t.Context(), `INSERT INTO challenges (id, title, visibility, timezone, starts_at, ends_at, created_by, deleted_at)
			VALUES ($1, $2, $3, 'UTC', $4, $5, $6, $7) RETURNING id`, r.id, r.title, r.visibility, now.Add(r.start), now.Add(r.end), r.creator, deletedAt).Scan(&id); err != nil {
			t.Fatal(err)
		}
		for user, status := range r.participants {
			var abandonedAt *time.Time
			if status == ParticipantAbandoned {
				abandonedAt = &now
			}
			if _, err := pool.Exec(t.Context(), "INSERT INTO participants VALUES ($1, $2, $3, $4, $5)", id, user, status, now.Add(-2*day), abandonedAt); err != nil {
				t.Fatal(err)
			}
		}
	}
	return f
}

func insertTestUser(t *testing.T, pool *pgxpool.Pool, subject string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(t.Context(), "INSERT INTO users (auth_subject) VALUES ($1) RETURNING id", subject).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func titles(items []Summary) []string {
	out := make([]string, len(items))
	for i, s := range items {
		out[i] = s.Title
	}
	return out
}

func TestListScopesAndStatuses(t *testing.T) {
	f := newListFixture(t)
	tests := []struct {
		name   string
		scope  string
		status string
		want   []string
		// counts are those of the scope, whatever the status filter.
		counts Counts
	}{
		// Never a private or deleted challenge in public; soonest first.
		{name: "public", scope: ScopePublic, want: []string{"G finished long ago", "E finished", "E2 finished, same start as E", "B ongoing, mine", "A upcoming", "H upcoming, same start as A"}, counts: Counts{Upcoming: 2, Ongoing: 1, Finished: 3}},
		{name: "public upcoming", scope: ScopePublic, status: StatusUpcoming, want: []string{"A upcoming", "H upcoming, same start as A"}, counts: Counts{Upcoming: 2, Ongoing: 1, Finished: 3}},
		{name: "public ongoing", scope: ScopePublic, status: StatusOngoing, want: []string{"B ongoing, mine"}, counts: Counts{Upcoming: 2, Ongoing: 1, Finished: 3}},
		// Finished: most recent first.
		{name: "public finished", scope: ScopePublic, status: StatusFinished, want: []string{"E2 finished, same start as E", "E finished", "G finished long ago"}, counts: Counts{Upcoming: 2, Ongoing: 1, Finished: 3}},
		// Mine: joined, abandoned included, private included, deleted excluded;
		// never another's private challenge.
		{name: "mine", scope: ScopeMine, want: []string{"B ongoing, mine", "D private, I abandoned"}, counts: Counts{Upcoming: 1, Ongoing: 1, Finished: 0}},
		{name: "mine upcoming", scope: ScopeMine, status: StatusUpcoming, want: []string{"D private, I abandoned"}, counts: Counts{Upcoming: 1, Ongoing: 1, Finished: 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := f.svc.List(t.Context(), f.me, tt.scope, tt.status, 20, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := titles(page.Items); !slices.Equal(got, tt.want) {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			if page.Next != nil {
				t.Fatalf("next %+v on a single page", page.Next)
			}
			if page.Counts != tt.counts {
				t.Fatalf("counts %+v, want %+v", page.Counts, tt.counts)
			}
		})
	}
}

// TestListWeekCountIsTheDetails: each item has the week count of its detail,
// a challenge of 14 days and one of 7 days, both from 15:00: the weeks start
// at midnight, the last one holds 15 hours.
func TestListWeekCountIsTheDetails(t *testing.T) {
	f := newListFixture(t)
	if _, err := f.pool.Exec(t.Context(), "UPDATE challenges SET ends_at = starts_at + interval '7 days' WHERE id = '00000000-0000-0000-0000-0000000000a1'"); err != nil {
		t.Fatal(err)
	}
	page, err := f.svc.List(t.Context(), f.me, ScopePublic, StatusUpcoming, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []int
	for _, it := range page.Items {
		d, err := f.svc.Get(t.Context(), f.me, it.ID)
		if err != nil {
			t.Fatal(err)
		}
		if it.WeekCount != d.WeekCount {
			t.Fatalf("%s: week count %d, want that of the detail, %d", it.Title, it.WeekCount, d.WeekCount)
		}
		got = append(got, it.WeekCount)
	}
	if !slices.Equal(got, []int{2, 3}) {
		t.Fatalf("week counts %v, want [2 3]", got)
	}
}

// TestListCurrentWeekIsTheDetails: in both scopes, every item has the
// current week of its detail, whoever reads it: null (0) for the upcoming
// and the finished ones, present for the ongoing one, to the active
// participant, to the one who abandoned and to a stranger alike.
func TestListCurrentWeekIsTheDetails(t *testing.T) {
	f := newListFixture(t)
	stranger := insertTestUser(t, f.pool, "email|stranger")
	readers := map[string]string{"active": f.me, "abandoned": f.other, "stranger": stranger}
	for name, actor := range readers {
		for _, scope := range []string{ScopePublic, ScopeMine} {
			t.Run(name+" "+scope, func(t *testing.T) {
				page, err := f.svc.List(t.Context(), actor, scope, "", 20, nil)
				if err != nil {
					t.Fatal(err)
				}
				for _, it := range page.Items {
					d, err := f.svc.Get(t.Context(), actor, it.ID)
					if err != nil {
						t.Fatal(err)
					}
					if it.CurrentWeek != d.CurrentWeek || !it.WeekEndsAt.Equal(d.WeekEndsAt) {
						t.Fatalf("%s: week %d ending %s, want that of the detail, %d ending %s", it.Title, it.CurrentWeek, it.WeekEndsAt, d.CurrentWeek, d.WeekEndsAt)
					}
					ongoing := it.Status == StatusOngoing
					if (it.CurrentWeek > 0) != ongoing || it.WeekEndsAt.IsZero() == ongoing {
						t.Fatalf("%s (%s): week %d ending %s, want one only while it runs", it.Title, it.Status, it.CurrentWeek, it.WeekEndsAt)
					}
				}
			})
		}
	}
	// The ongoing challenge, started yesterday at 15:00 UTC: week 1, ending
	// at midnight seven days after its first day.
	page, err := f.svc.List(t.Context(), stranger, ScopePublic, StatusOngoing, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].CurrentWeek != 1 || !page.Items[0].WeekEndsAt.Equal(time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("ongoing %+v, want week 1 ending 2026-03-10", page.Items)
	}
}

// TestListGivesTheReferenceSport: both lists read the reference sport of
// each challenge, a run and a ride one, the same as the detail.
func TestListGivesTheReferenceSport(t *testing.T) {
	f := newListFixture(t)
	if _, err := f.pool.Exec(t.Context(), "UPDATE challenges SET reference_sport = 'ride' WHERE id = '00000000-0000-0000-0000-0000000000b0'"); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		scope string
		want  map[string]string
	}{
		{scope: ScopePublic, want: map[string]string{"B ongoing, mine": "ride", "A upcoming": "run"}},
		{scope: ScopeMine, want: map[string]string{"B ongoing, mine": "ride", "D private, I abandoned": "run"}},
	}
	for _, tt := range tests {
		t.Run(tt.scope, func(t *testing.T) {
			page, err := f.svc.List(t.Context(), f.me, tt.scope, "", 20, nil)
			if err != nil {
				t.Fatal(err)
			}
			found := 0
			for _, s := range page.Items {
				want, ok := tt.want[s.Title]
				if !ok {
					continue
				}
				found++
				d, err := f.svc.Get(t.Context(), f.me, s.ID)
				if err != nil {
					t.Fatal(err)
				}
				if s.ReferenceSport != want || d.ReferenceSport != want {
					t.Errorf("%s: list %q, detail %q, want %s", s.Title, s.ReferenceSport, d.ReferenceSport, want)
				}
			}
			if found != len(tt.want) {
				t.Fatalf("found %d of %d challenges in %q", found, len(tt.want), titles(page.Items))
			}
		})
	}
}

func TestListCountsAndMyStatus(t *testing.T) {
	f := newListFixture(t)
	// A deleted account in B is not counted.
	gone := insertTestUser(t, f.pool, "email|gone")
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ('00000000-0000-0000-0000-0000000000b0', $1, $2)`, gone, now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE users SET anonymized_at = now() WHERE id = $1", gone); err != nil {
		t.Fatal(err)
	}
	page, err := f.svc.List(t.Context(), f.me, ScopeMine, "", 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Summary{}
	for _, s := range page.Items {
		got[s.Title] = s
	}
	// B: me active and the creator abandoned, both counted; the deleted
	// account is not.
	if b := got["B ongoing, mine"]; b.ParticipantCount != 2 || b.MyStatus == nil || *b.MyStatus != "active" {
		t.Errorf("B = %d participants, my status %v, want 2 and active", b.ParticipantCount, b.MyStatus)
	}
	if d := got["D private, I abandoned"]; d.ParticipantCount != 1 || d.MyStatus == nil || *d.MyStatus != "abandoned" {
		t.Errorf("D = %d participants, my status %v, want 1 and abandoned", d.ParticipantCount, d.MyStatus)
	}

	public, err := f.svc.List(t.Context(), f.me, ScopePublic, StatusUpcoming, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range public.Items {
		if s.MyStatus != nil || s.ParticipantCount != 0 {
			t.Errorf("%s = %d participants, my status %v, want none", s.Title, s.ParticipantCount, s.MyStatus)
		}
	}
}

// TestListRoleAndStatus gives every role and status in the list, the same
// as the detail of each challenge at the same instant.
func TestListRoleAndStatus(t *testing.T) {
	f := newListFixture(t)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.pool.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	const (
		g = "00000000-0000-0000-0000-000000000010"
		b = "00000000-0000-0000-0000-0000000000b0"
		a = "00000000-0000-0000-0000-0000000000a1"
	)
	// Me creator of B, a participant of G; invited to A by its creator.
	exec("INSERT INTO challenge_creators (challenge_id, user_id) VALUES ($1, $2)", b, f.me)
	exec("INSERT INTO participants (challenge_id, user_id, status, joined_at) VALUES ($1, $2, 'active', $3)", g, f.me, now.Add(-40*24*time.Hour))
	exec("INSERT INTO challenge_creators (challenge_id, user_id) VALUES ($1, $2)", a, f.other)
	var invitation string
	if err := f.pool.QueryRow(t.Context(), `INSERT INTO invitations (challenge_id, invited_by, token_hash, expires_at, created_at)
		VALUES ($1, $2, sha256('t'::bytea), $3, $4) RETURNING id`, a, f.other, now.Add(24*time.Hour), now).Scan(&invitation); err != nil {
		t.Fatal(err)
	}
	exec("INSERT INTO invitation_recipients VALUES ($1, $2, $3)", invitation, f.me, now)

	type want struct {
		role   Role
		status string
	}
	tests := []struct {
		scope string
		want  map[string]want
	}{
		{scope: ScopePublic, want: map[string]want{
			"G finished long ago":          {RoleParticipant, StatusFinished},
			"E finished":                   {RoleOutsider, StatusFinished},
			"E2 finished, same start as E": {RoleOutsider, StatusFinished},
			"B ongoing, mine":              {RoleCreator, StatusOngoing},
			"A upcoming":                   {RoleInvited, StatusUpcoming},
			"H upcoming, same start as A":  {RoleOutsider, StatusUpcoming},
		}},
		{scope: ScopeMine, want: map[string]want{
			"G finished long ago":    {RoleParticipant, StatusFinished},
			"B ongoing, mine":        {RoleCreator, StatusOngoing},
			"D private, I abandoned": {RoleAbandoned, StatusUpcoming},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.scope, func(t *testing.T) {
			page, err := f.svc.List(t.Context(), f.me, tt.scope, "", 20, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Items) != len(tt.want) {
				t.Fatalf("got %q, want %d challenges", titles(page.Items), len(tt.want))
			}
			for _, s := range page.Items {
				w, ok := tt.want[s.Title]
				if !ok || s.Role != w.role || s.Status != w.status {
					t.Errorf("%s = role %v, status %q, want %+v", s.Title, s.Role, s.Status, w)
				}
				d, err := f.svc.Get(t.Context(), f.me, s.ID)
				if err != nil {
					t.Fatal(err)
				}
				if d.Role != s.Role || d.Status != s.Status {
					t.Errorf("%s: detail role %v, status %q, list %v, %q", s.Title, d.Role, d.Status, s.Role, s.Status)
				}
			}
		})
	}
}

// TestListPagination walks every page one item at a time: each challenge
// once, in order, across the ties on starts_at (A and H ascending, E and E2
// descending), for every scope and status.
func TestListPagination(t *testing.T) {
	f := newListFixture(t)
	for _, scope := range []string{ScopePublic, ScopeMine} {
		for _, status := range []string{"", StatusUpcoming, StatusOngoing, StatusFinished} {
			t.Run(scope+" "+status, func(t *testing.T) {
				all, err := f.svc.List(t.Context(), f.me, scope, status, 100, nil)
				if err != nil {
					t.Fatal(err)
				}
				var walked []string
				var after *Position
				for range len(all.Items) + 1 {
					page, err := f.svc.List(t.Context(), f.me, scope, status, 1, after)
					if err != nil {
						t.Fatal(err)
					}
					walked = append(walked, titles(page.Items)...)
					if page.Next == nil {
						break
					}
					after = page.Next
				}
				if want := titles(all.Items); !slices.Equal(walked, want) {
					t.Fatalf("walked %q, want %q", walked, want)
				}
			})
		}
	}
}
