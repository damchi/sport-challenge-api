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
		svc:   NewService(NewRepository(gdb), nil, nil, clock.NewFake(now)),
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
	}{
		// Never a private or deleted challenge in public; soonest first.
		{name: "public", scope: ScopePublic, want: []string{"G finished long ago", "E finished", "E2 finished, same start as E", "B ongoing, mine", "A upcoming", "H upcoming, same start as A"}},
		{name: "public upcoming", scope: ScopePublic, status: StatusUpcoming, want: []string{"A upcoming", "H upcoming, same start as A"}},
		{name: "public ongoing", scope: ScopePublic, status: StatusOngoing, want: []string{"B ongoing, mine"}},
		// Finished: most recent first.
		{name: "public finished", scope: ScopePublic, status: StatusFinished, want: []string{"E2 finished, same start as E", "E finished", "G finished long ago"}},
		// Mine: joined, abandoned included, private included, deleted excluded;
		// never another's private challenge.
		{name: "mine", scope: ScopeMine, want: []string{"B ongoing, mine", "D private, I abandoned"}},
		{name: "mine upcoming", scope: ScopeMine, status: StatusUpcoming, want: []string{"D private, I abandoned"}},
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
		})
	}
}

func TestListCountsAndMyStatus(t *testing.T) {
	f := newListFixture(t)
	page, err := f.svc.List(t.Context(), f.me, ScopeMine, "", 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Summary{}
	for _, s := range page.Items {
		got[s.Title] = s
	}
	// B: me active and the creator abandoned, both counted.
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
