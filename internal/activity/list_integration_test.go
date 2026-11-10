//go:build integration

package activity

import (
	"slices"
	"testing"
	"time"
)

// insert stores an activity of userID started at startedAt, deleted when
// deleted, and returns its id.
func (f fixture) insert(userID string, startedAt time.Time, deleted bool) string {
	f.t.Helper()
	var deletedAt *time.Time
	if deleted {
		deletedAt = &recordNow
	}
	var id string
	if err := f.pool.QueryRow(f.t.Context(), `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, deleted_at)
		VALUES ($1, 'yoga', $2, 3600, $3) RETURNING id`, userID, startedAt, deletedAt).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	return id
}

func ids(as []Activity) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.ID)
	}
	return out
}

func TestListFiltersAndOrders(t *testing.T) {
	f := setupRecord(t)
	var other string
	if err := f.pool.QueryRow(t.Context(), "INSERT INTO users (auth_subject, pseudo) VALUES ('email|other', 'other') RETURNING id").Scan(&other); err != nil {
		t.Fatal(err)
	}
	from, to := toronto(t, 2026, 6, 1, 0), toronto(t, 2026, 6, 8, 0)
	first := f.insert(f.user, from, false) // at the start: in
	middle := f.insert(f.user, toronto(t, 2026, 6, 3, 7), false)
	f.insert(f.user, toronto(t, 2026, 6, 4, 7), true) // deleted
	f.insert(f.user, to, false)                       // at the end: out
	f.insert(f.user, from.Add(-time.Second), false)   // before: out
	f.insert(other, toronto(t, 2026, 6, 3, 7), false) // someone else
	repo := NewRepository(f.gdb)

	got, err := repo.List(t.Context(), ListQuery{UserID: f.user, From: &from, To: &to, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(got), []string{middle, first}) {
		t.Fatalf("List() = %v, want [%s %s], most recent first", ids(got), middle, first)
	}
	if a := got[0]; a.UserID != f.user || a.SportCode != "yoga" || a.DurationSeconds != 3600 || a.Source != SourceManual || !a.StartedAt.Equal(toronto(t, 2026, 6, 3, 7)) {
		t.Fatalf("first activity %+v, want the row as stored", a)
	}

	all, err := repo.List(t.Context(), ListQuery{UserID: f.user, Limit: 10})
	if err != nil || len(all) != 4 {
		t.Fatalf("List() without a period = %v, %v, want the 4 not deleted", ids(all), err)
	}
}

// TestListPages walks every page, activities started at the same instant
// included: each one comes once, ordered by id among equals.
func TestListPages(t *testing.T) {
	f := setupRecord(t)
	same := toronto(t, 2026, 6, 3, 7)
	for range 3 {
		f.insert(f.user, same, false)
	}
	f.insert(f.user, toronto(t, 2026, 6, 4, 7), false)
	f.insert(f.user, toronto(t, 2026, 6, 2, 7), false)
	repo := NewRepository(f.gdb)

	want, err := repo.List(t.Context(), ListQuery{UserID: f.user, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var walked []string
	var after *Position
	for range 10 {
		page, err := repo.List(t.Context(), ListQuery{UserID: f.user, Limit: 2, After: after})
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		walked = append(walked, ids(page)...)
		last := page[len(page)-1]
		after = &Position{StartedAt: last.StartedAt, ID: last.ID}
	}
	if !slices.Equal(walked, ids(want)) {
		t.Fatalf("pages %v, want %v", walked, ids(want))
	}
	for i := 1; i < len(want); i++ {
		prev, cur := want[i-1], want[i]
		if cur.StartedAt.After(prev.StartedAt) || (cur.StartedAt.Equal(prev.StartedAt) && cur.ID > prev.ID) {
			t.Fatalf("order %v, want most recent first, then id descending", ids(want))
		}
	}
}
