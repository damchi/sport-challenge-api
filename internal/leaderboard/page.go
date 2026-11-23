package leaderboard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
)

// podiumRanks is how many ranks the podium shows, always: equal ones share
// a rank, so it may hold more lines. A value of 0 is never on it (decision
// of 2026-11-23): at the start of a challenge, everyone is equal at 0.
const podiumRanks = 3

// Page returns the part of the leaderboard of challenge id that req asks
// for, as actor may read it (Get, whose rules and computation it keeps: it
// only cuts the ranking computed). A category not in the challenge, or a
// cursor of another category, answers 400 validation_failed; a cursor
// given in a ranking that has changed since answers 400 cursor_invalid.
func (s *Service) Page(ctx context.Context, actor auth.Actor, id string, req PageRequest) (Page, error) {
	// The contract bounds the limit; the page never relies on it alone.
	if req.Limit < 0 {
		return Page{}, apperr.InvalidFields(nil, "limit")
	}
	board, err := s.Get(ctx, actor, id)
	if err != nil {
		return Page{}, err
	}
	categories := board.Categories
	if req.Category != "" {
		categories = nil
		for _, cat := range board.Categories {
			if cat.Type == req.Category {
				categories = []CategoryRanking{cat}
			}
		}
		if categories == nil {
			return Page{}, apperr.InvalidFields(nil, "category")
		}
	}
	if req.After != nil && req.After.Category != req.Category {
		return Page{}, apperr.InvalidFields(nil, "cursor")
	}
	page := Page{ComputedAt: board.ComputedAt, Categories: make([]CategoryPage, len(categories))}
	for i, cat := range categories {
		if page.Categories[i], err = paginate(cat, actor.ID, req.Limit, req.After); err != nil {
			return Page{}, err
		}
	}
	return page, nil
}

// paginate cuts the ranking of a category for userID: without limit, every
// line; with it, the podium (ranks 1 to podiumRanks, above 0), then limit lines after
// it from after (nil for the first page), and the position of the next page
// when lines are left. The ranking orders the ranked lines first, the
// abandoned ones last, so the lines after the podium keep that order.
func paginate(cat CategoryRanking, userID string, limit int, after *Position) (CategoryPage, error) {
	out := CategoryPage{Type: cat.Type, RankedCount: cat.RankedCount}
	for i := range cat.Entries {
		if cat.Entries[i].UserID == userID {
			me := cat.Entries[i]
			out.Me = &me
		}
	}
	if limit == 0 {
		out.Entries = cat.Entries
		return out, nil
	}
	podium := 0
	for podium < len(cat.Entries) && cat.Entries[podium].Rank > 0 && cat.Entries[podium].Rank <= podiumRanks && cat.Entries[podium].Value > 0 {
		podium++
	}
	// Never nil with a limit: an empty podium is not a missing one.
	out.Podium = append(make([]Ranked, 0, podium), cat.Entries[:podium]...)
	rest := cat.Entries[podium:]
	version := versionOf(cat)
	from := 0
	if after != nil {
		if after.Version != version {
			return CategoryPage{}, apperr.New(apperr.CodeCursorInvalid, nil)
		}
		if after.Offset <= 0 || after.Offset > len(rest) {
			return CategoryPage{}, apperr.InvalidFields(nil, "cursor")
		}
		from = after.Offset
	}
	to := min(from+limit, len(rest))
	out.Entries = rest[from:to]
	if to < len(rest) {
		out.Next = &Position{Category: cat.Type, Offset: to, Version: version}
	}
	return out, nil
}

// versionOf is the state of the ranking of a category: who is on each line,
// with which rank, value and abandonment. Any change of one (an activity
// entered, a week locked, someone leaving) changes it; the provisional points
// and the elimination progress, which move no line, do not.
func versionOf(cat CategoryRanking) string {
	h := sha256.New()
	for _, e := range cat.Entries {
		// A hash never fails to write.
		_, _ = fmt.Fprintf(h, "%s:%d:%d:%t;", e.UserID, e.Rank, e.Value, e.Abandoned)
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}
