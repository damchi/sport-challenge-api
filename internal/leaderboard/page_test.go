package leaderboard

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// line is a ranked line of user u at rank and value; rank 0 is an
// abandoned participant.
func line(u string, rank, value int) Ranked {
	return Ranked{Entry: scoring.Entry{UserID: u, Rank: rank, Value: value, Abandoned: rank == 0}}
}

// users of the lines, in order.
func usersOf(lines []Ranked) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.UserID
	}
	return out
}

// isCode reports whether err is an apperr of code, on fields when given.
func isCode(err error, code apperr.Code, fields string) bool {
	var e *apperr.Error
	return errors.As(err, &e) && e.Code == code && (fields == "" || e.Params[apperr.FieldsParam] == fields)
}

func TestPaginate(t *testing.T) {
	// a b tied 1st, c d tied 3rd: a podium of 4 lines; then e f g, and two
	// who abandoned.
	cat := CategoryRanking{Type: CategoryCumulativeTime, RankedCount: 7, Entries: []Ranked{
		line("a", 1, 90), line("b", 1, 90), line("c", 3, 80), line("d", 3, 80),
		line("e", 5, 70), line("f", 6, 60), line("g", 7, 50), line("x", 0, 0), line("y", 0, 0),
	}}
	version := versionOf(cat)
	tests := []struct {
		name        string
		user        string
		limit       int
		after       *Position
		wantPodium  []string
		wantEntries []string
		wantMe      string
		wantNext    *Position
	}{
		{
			name: "without limit, every line and no podium", user: "f",
			wantEntries: []string{"a", "b", "c", "d", "e", "f", "g", "x", "y"}, wantMe: "f",
		},
		{
			name: "the podium holds the equal ones at the 3rd rank", user: "a", limit: 2,
			wantPodium: []string{"a", "b", "c", "d"}, wantEntries: []string{"e", "f"}, wantMe: "a",
			wantNext: &Position{Category: CategoryCumulativeTime, Offset: 2, Version: version},
		},
		{
			name: "me out of the page", user: "y", limit: 2,
			wantPodium: []string{"a", "b", "c", "d"}, wantEntries: []string{"e", "f"}, wantMe: "y",
			wantNext: &Position{Category: CategoryCumulativeTime, Offset: 2, Version: version},
		},
		{
			name: "the next page, the abandoned ones last", user: "e", limit: 2,
			after:      &Position{Category: CategoryCumulativeTime, Offset: 2, Version: version},
			wantPodium: []string{"a", "b", "c", "d"}, wantEntries: []string{"g", "x"}, wantMe: "e",
			wantNext: &Position{Category: CategoryCumulativeTime, Offset: 4, Version: version},
		},
		{
			name: "the last page", user: "x", limit: 2,
			after:      &Position{Category: CategoryCumulativeTime, Offset: 4, Version: version},
			wantPodium: []string{"a", "b", "c", "d"}, wantEntries: []string{"y"}, wantMe: "x",
		},
		{
			name: "a page as long as the rest ends it", user: "a", limit: 5,
			wantPodium: []string{"a", "b", "c", "d"}, wantEntries: []string{"e", "f", "g", "x", "y"}, wantMe: "a",
		},
		{
			name: "not a participant: no line of mine", user: "admin", limit: 100,
			wantPodium: []string{"a", "b", "c", "d"}, wantEntries: []string{"e", "f", "g", "x", "y"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := paginate(cat, tt.user, tt.limit, tt.after)
			if err != nil {
				t.Fatal(err)
			}
			if got.Type != cat.Type || got.RankedCount != 7 {
				t.Fatalf("type %s, ranked %d, want the category's", got.Type, got.RankedCount)
			}
			if tt.wantPodium == nil && got.Podium != nil {
				t.Fatalf("podium %v, want none", usersOf(got.Podium))
			}
			if tt.wantPodium != nil && !reflect.DeepEqual(usersOf(got.Podium), tt.wantPodium) {
				t.Fatalf("podium %v, want %v", usersOf(got.Podium), tt.wantPodium)
			}
			if !reflect.DeepEqual(usersOf(got.Entries), tt.wantEntries) {
				t.Fatalf("entries %v, want %v", usersOf(got.Entries), tt.wantEntries)
			}
			switch {
			case tt.wantMe == "" && got.Me != nil:
				t.Fatalf("me %+v, want none", got.Me)
			case tt.wantMe != "" && (got.Me == nil || got.Me.UserID != tt.wantMe):
				t.Fatalf("me %+v, want %s", got.Me, tt.wantMe)
			}
			if !reflect.DeepEqual(got.Next, tt.wantNext) {
				t.Fatalf("next %+v, want %+v", got.Next, tt.wantNext)
			}
		})
	}
}

func TestPaginateEdges(t *testing.T) {
	t.Run("nobody ranked yet: an empty podium and page", func(t *testing.T) {
		got, err := paginate(CategoryRanking{Type: CategoryPoints}, "a", 10, nil)
		if err != nil || got.Podium == nil || len(got.Podium) != 0 || len(got.Entries) != 0 || got.Next != nil || got.Me != nil {
			t.Fatalf("paginate() = %+v, %v, want an empty podium and page", got, err)
		}
	})
	t.Run("everyone on the podium: an empty page", func(t *testing.T) {
		cat := CategoryRanking{Type: CategoryPoints, Entries: []Ranked{line("a", 1, 10), line("b", 2, 8)}}
		got, err := paginate(cat, "b", 10, nil)
		if err != nil || len(got.Podium) != 2 || len(got.Entries) != 0 || got.Next != nil || got.Me.UserID != "b" {
			t.Fatalf("paginate() = %+v, %v, want the podium alone", got, err)
		}
	})
	t.Run("a value of 0 is not on the podium", func(t *testing.T) {
		cat := CategoryRanking{Type: CategoryPoints, Entries: []Ranked{line("a", 1, 10), line("b", 2, 0), line("c", 2, 0), line("q", 0, 0)}}
		got, err := paginate(cat, "b", 10, nil)
		if err != nil || !reflect.DeepEqual(usersOf(got.Podium), []string{"a"}) || !reflect.DeepEqual(usersOf(got.Entries), []string{"b", "c", "q"}) {
			t.Fatalf("paginate() = %+v, %v, want a alone on the podium", got, err)
		}
	})
	t.Run("everyone at 0, at the start: an empty podium, every line paginated", func(t *testing.T) {
		cat := CategoryRanking{Type: CategoryCumulativeTime, Entries: []Ranked{line("a", 1, 0), line("b", 1, 0), line("c", 1, 0), line("d", 1, 0)}}
		got, err := paginate(cat, "a", 2, nil)
		if err != nil || len(got.Podium) != 0 || !reflect.DeepEqual(usersOf(got.Entries), []string{"a", "b"}) || got.Next == nil {
			t.Fatalf("paginate() = %+v, %v, want no podium and a first page of 2", got, err)
		}
	})
	t.Run("the abandoned one is my line", func(t *testing.T) {
		cat := CategoryRanking{Type: CategoryPoints, Entries: []Ranked{line("a", 1, 10), line("q", 0, 0)}}
		got, err := paginate(cat, "q", 1, nil)
		if err != nil || got.Me == nil || !got.Me.Abandoned {
			t.Fatalf("me %+v, %v, want the abandoned line", got.Me, err)
		}
	})
	cat := CategoryRanking{Type: CategoryCumulativeTime, Entries: []Ranked{
		line("a", 1, 9), line("b", 2, 8), line("c", 3, 7), line("d", 4, 6), line("e", 5, 5),
	}}
	version := versionOf(cat)
	t.Run("a ranking that changed refuses the cursor", func(t *testing.T) {
		moved := CategoryRanking{Type: cat.Type, Entries: []Ranked{
			line("a", 1, 9), line("b", 2, 8), line("c", 3, 7), line("d", 4, 6), line("e", 5, 6),
		}}
		_, err := paginate(moved, "a", 1, &Position{Category: cat.Type, Offset: 1, Version: version})
		if !isCode(err, apperr.CodeCursorInvalid, "") {
			t.Fatalf("err = %v, want cursor_invalid", err)
		}
	})
	t.Run("a cursor past the end is refused", func(t *testing.T) {
		_, err := paginate(cat, "a", 1, &Position{Category: cat.Type, Offset: 3, Version: version})
		if !isCode(err, apperr.CodeValidationFailed, "cursor") {
			t.Fatalf("err = %v, want validation_failed on cursor", err)
		}
	})
	t.Run("the provisional points do not change the version", func(t *testing.T) {
		later := CategoryRanking{Type: cat.Type, Entries: append([]Ranked(nil), cat.Entries...)}
		p := 12
		later.Entries[0].Provisional = &p
		if versionOf(later) != version {
			t.Fatal("version changed with the provisional points alone")
		}
	})
}

func TestPage(t *testing.T) {
	c := twoWeeks(t)
	run := func(user string, seconds int) scoring.Activity {
		return scoring.Activity{UserID: user, SportCode: "run", StartedAt: c.StartsAt, DurationSeconds: seconds, DistanceMeters: meters(seconds)}
	}
	participants := []scoring.Participant{{UserID: "a"}, {UserID: "b"}, {UserID: "c"}, {UserID: "d"}, {UserID: "e"}, {UserID: "q", Abandoned: true}}
	activities := []scoring.Activity{run("a", 900), run("b", 800), run("c", 700), run("d", 600), run("e", 500)}
	service := func() *Service {
		return NewService(&fakeChallenges{c: c}, fakeProfiles{}, &fakeRepo{participants: participants, activities: activities}, clock.NewFake(c.StartsAt.Add(24*time.Hour)), 24*time.Hour)
	}
	actor := auth.Actor{ID: "e"}

	t.Run("limit absent: the whole leaderboard, as Get", func(t *testing.T) {
		got, err := service().Page(t.Context(), actor, "c1", PageRequest{})
		if err != nil {
			t.Fatal(err)
		}
		want, err := service().Get(t.Context(), actor, "c1")
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Categories) != len(want.Categories) {
			t.Fatalf("%d categories, want %d", len(got.Categories), len(want.Categories))
		}
		for i, cat := range got.Categories {
			if cat.Podium != nil || cat.Next != nil || !reflect.DeepEqual(cat.Entries, want.Categories[i].Entries) || cat.Me == nil || cat.Me.UserID != "e" {
				t.Fatalf("category %s = %+v, want every line, no podium nor cursor, and my line", cat.Type, cat)
			}
		}
	})

	t.Run("a category alone, a page, then the next", func(t *testing.T) {
		first, err := service().Page(t.Context(), actor, "c1", PageRequest{Category: CategoryCumulativeTime, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if len(first.Categories) != 1 || first.Categories[0].Type != CategoryCumulativeTime {
			t.Fatalf("categories %+v, want the time alone", first.Categories)
		}
		cat := first.Categories[0]
		if !reflect.DeepEqual(usersOf(cat.Podium), []string{"a", "b", "c"}) || !reflect.DeepEqual(usersOf(cat.Entries), []string{"d"}) || cat.Next == nil {
			t.Fatalf("first page %+v, want a b c, then d and a cursor", cat)
		}
		next, err := service().Page(t.Context(), actor, "c1", PageRequest{Category: CategoryCumulativeTime, Limit: 5, After: cat.Next})
		if err != nil {
			t.Fatal(err)
		}
		if got := next.Categories[0]; !reflect.DeepEqual(usersOf(got.Entries), []string{"e", "q"}) || got.Next != nil || len(got.Podium) != 3 {
			t.Fatalf("next page %+v, want e then q, the podium again, and the end", got)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		timeCursor := &Position{Category: CategoryCumulativeTime, Offset: 1, Version: "x"}
		tests := []struct {
			name   string
			req    PageRequest
			code   apperr.Code
			fields string
		}{
			{"a negative limit", PageRequest{Limit: -1}, apperr.CodeValidationFailed, "limit"},
			{"a category not in the challenge", PageRequest{Category: CategoryPoints, Limit: 5}, apperr.CodeValidationFailed, "category"},
			{"a cursor of another category", PageRequest{Category: CategoryCumulativeDistance, Limit: 5, After: timeCursor}, apperr.CodeValidationFailed, "cursor"},
			{"a cursor of another state", PageRequest{Category: CategoryCumulativeTime, Limit: 5, After: timeCursor}, apperr.CodeCursorInvalid, ""},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if _, err := service().Page(t.Context(), actor, "c1", tt.req); !isCode(err, tt.code, tt.fields) {
					t.Fatalf("err = %v, want %s %s", err, tt.code, tt.fields)
				}
			})
		}
	})

	t.Run("a challenge the actor may not see stays not found", func(t *testing.T) {
		notFound := apperr.New(apperr.CodeChallengeNotFound, nil)
		repo := &fakeRepo{}
		svc := NewService(&fakeChallenges{err: notFound}, fakeProfiles{}, repo, clock.NewFake(c.StartsAt), time.Hour)
		if _, err := svc.Page(t.Context(), actor, "c1", PageRequest{Category: CategoryPoints, Limit: 5}); !errors.Is(err, notFound) || repo.calls != 0 {
			t.Fatalf("err = %v after %d reads, want challenge_not_found before anything is read", err, repo.calls)
		}
	})
}
