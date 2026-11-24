package admin

import (
	"maps"
	"slices"
	"time"

	"github.com/damchi/sport-challenge-api/internal/leaderboard"
)

// What a change does to the frozen results of a challenge (resultsChange).
const (
	ResultsRefrozen = "refrozen"
	ResultsReopened = "reopened"
	ResultsFrozen   = "frozen"
)

// SportChange is the reference sport of a challenge before and after a
// change.
type SportChange struct {
	Before, After string
}

// WeekChange is a week whose outcome a change moves: Locked after the
// change, it is judged again (its eliminations or acquired points change);
// otherwise recomputed (its provisional points or elimination minimum).
type WeekChange struct {
	Number int
	Locked bool
}

// PointsChange is a participant whose points of a week of the points
// category change: Before and After are 0 when not ranked in it;
// Provisional when the week is not locked after the change.
type PointsChange struct {
	User          UserRef
	Week          int
	Before, After int
	Provisional   bool
}

// ChallengeState is what a preview compares of a challenge, read in the
// transaction of a change, deleted or not: its title, reference sport and
// dates, whether its results are frozen, and its eliminations, earliest
// week first.
type ChallengeState struct {
	Title          string
	ReferenceSport string
	StartsAt       time.Time
	EndsAt         time.Time
	Frozen         bool
	Eliminations   []EliminationRow
}

// reading is a challenge as read before or after a change: its state, and
// its rankings and weeks when it is ranked (a deleted challenge is not).
type reading struct {
	state ChallengeState
	board *leaderboard.Leaderboard
	weeks *leaderboard.Weeks
}

// preview tells what a change does to challenge id, read before and after
// it, at now. A deleted challenge (lc deletes) is told by its title, status
// and reference sport alone: nothing of it is judged again. The weeks and
// the points need its rankings on both sides: none for a restore.
func preview(id string, before, after reading, now time.Time, lc lifecycle) ChallengePreview {
	p := ChallengePreview{
		ChallengeID:    id,
		Before:         before.board,
		After:          after.board,
		Title:          after.state.Title,
		Status:         statusAt(now, after.state.StartsAt, after.state.EndsAt),
		ReferenceSport: SportChange{Before: before.state.ReferenceSport, After: after.state.ReferenceSport},
	}
	if lc == deletes {
		return p
	}
	p.Eliminations = eliminationChanges(before.state.Eliminations, after.state.Eliminations)
	p.Results = resultsChange(before.state.Frozen, after.state.Frozen)
	if before.board != nil && after.board != nil && before.weeks != nil && after.weeks != nil {
		p.Weeks = weekChanges(before, after)
		p.Points = pointsChanges(*before.board, *after.board, *before.weeks, *after.weeks)
	}
	return p
}

// resultsChange is what a change does to the frozen results, frozen before
// and after it or not: "" when frozen on neither side.
func resultsChange(before, after bool) string {
	switch {
	case before && after:
		return ResultsRefrozen
	case before:
		return ResultsReopened
	case after:
		return ResultsFrozen
	default:
		return ""
	}
}

// weekChanges lists, by number, the weeks whose outcome differs between
// before and after: their points or eliminations (judged in a locked week
// only, so a week locked before and not after, its first day moved later,
// loses them), or, for the week in progress, the elimination minimums.
func weekChanges(before, after reading) []WeekChange {
	// The last week begun has points even once over, before it locks (no
	// week in progress then).
	last := max(before.weeks.Locked, before.weeks.Current, after.weeks.Locked, after.weeks.Current,
		lastPointsWeek(*before.weeks), lastPointsWeek(*after.weeks))
	var out []WeekChange
	for n := 1; n <= last; n++ {
		locked := n <= after.weeks.Locked
		changed := pointsDiffer(*before.weeks, *after.weeks, n) ||
			eliminationsDiffer(before.state.Eliminations, after.state.Eliminations, n) ||
			(!locked && n == after.weeks.Current && !maps.Equal(minimums(*before.board), minimums(*after.board)))
		if changed {
			out = append(out, WeekChange{Number: n, Locked: locked})
		}
	}
	return out
}

// pointsDiffer tells whether a participant has other points in week n
// before and after.
func pointsDiffer(before, after leaderboard.Weeks, n int) bool {
	for _, user := range pointsUsers(before, after) {
		if before.Points[user][n] != after.Points[user][n] {
			return true
		}
	}
	return false
}

// eliminationsDiffer tells whether the eliminations judged in week n differ
// between before and after: another participant, distance or minimum.
func eliminationsDiffer(before, after []EliminationRow, n int) bool {
	of := func(rows []EliminationRow) map[string][2]int {
		out := map[string][2]int{}
		for _, e := range rows {
			if e.WeekNumber == n {
				out[e.User.ID] = [2]int{e.DistanceMeters, e.RequiredMeters}
			}
		}
		return out
	}
	return !maps.Equal(of(before), of(after))
}

// minimums gives, by participant, the elimination minimum of the week in
// progress in board (nil without one: week 1, or eliminated).
func minimums(board leaderboard.Leaderboard) map[string]int {
	out := map[string]int{}
	for _, cat := range board.Categories {
		if cat.Type != leaderboard.CategoryElimination {
			continue
		}
		for _, e := range cat.Entries {
			if e.Progress != nil && e.Progress.MinimumMeters != nil {
				out[e.UserID] = *e.Progress.MinimumMeters
			}
		}
	}
	return out
}

// pointsChanges lists, by week then participant ID, the points of a week
// that differ between before and after, each participant named as in the
// rankings (after the change first).
func pointsChanges(beforeBoard, afterBoard leaderboard.Leaderboard, before, after leaderboard.Weeks) []PointsChange {
	users := pointsUsers(before, after)
	names := namesOf(beforeBoard)
	for id, n := range namesOf(afterBoard) {
		names[id] = n
	}
	last := max(lastPointsWeek(before), lastPointsWeek(after))
	var out []PointsChange
	for n := 1; n <= last; n++ {
		for _, user := range users {
			was, is := before.Points[user][n], after.Points[user][n]
			if was == is {
				continue
			}
			ref := UserRef{ID: user}
			if name, ok := names[user]; ok {
				ref.Pseudo, ref.FirstName, ref.LastName = name.Pseudo, name.FirstName, name.LastName
			}
			out = append(out, PointsChange{User: ref, Week: n, Before: was, After: is, Provisional: n > after.Locked})
		}
	}
	return out
}

// lastPointsWeek is the last week with points in w, 0 for none.
func lastPointsWeek(w leaderboard.Weeks) int {
	last := 0
	for _, weeks := range w.Points {
		for n := range weeks {
			last = max(last, n)
		}
	}
	return last
}

// pointsUsers lists, sorted, the participants with points before or after.
func pointsUsers(before, after leaderboard.Weeks) []string {
	var out []string
	for _, w := range []leaderboard.Weeks{before, after} {
		for user := range w.Points {
			if !slices.Contains(out, user) {
				out = append(out, user)
			}
		}
	}
	slices.Sort(out)
	return out
}

// namesOf gives the name of every participant ranked in board, by ID.
func namesOf(board leaderboard.Leaderboard) map[string]leaderboard.Name {
	out := map[string]leaderboard.Name{}
	for _, cat := range board.Categories {
		for _, e := range cat.Entries {
			out[e.UserID] = e.Name
		}
	}
	return out
}
