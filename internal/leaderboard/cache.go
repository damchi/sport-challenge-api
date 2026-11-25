package leaderboard

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"time"

	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// keptFor is how long a computed ranking is served to the next reads of its
// challenge (decision of 2026-11-24): an activity entered meanwhile shows
// once it has passed.
const keptFor = 5 * time.Second

// keptChallenges bounds the challenges whose ranking an instance keeps; the
// oldest computed go first.
const keptChallenges = 256

// errAborted is the outcome of a computation until it returns: it stays so
// for one that panicked, and its readers compute again. Never returned to a
// caller.
var errAborted = errors.New("ranking computation aborted")

// computed is the ranking of a challenge computed at an instant from the
// raw activities, with what it was computed from: never a stored total, it
// is only kept in memory a short while (keptFor).
type computed struct {
	// window is that of the challenge at the instant of the computation.
	window       scoring.Window
	participants []scoring.Participant
	activities   []scoring.Activity
	// board is read only: every reader shares it.
	board Leaderboard
}

// rankings keeps the ranking computed for each challenge, so that the reads
// that follow it within keptFor do not compute it again, and computes it
// once at a time per challenge: the reads that come during a computation
// wait for its result.
type rankings struct {
	mu      sync.Mutex
	entries map[string]*kept
}

// kept is the ranking of a challenge, computed or being computed.
type kept struct {
	// challenge is the one it was computed for, as every reader sees it: a
	// challenge whose settings or window changed since is computed again.
	challenge Challenge
	// at is the instant of the computation; until the one it is no longer
	// served from, even while it is being computed.
	at    time.Time
	until time.Time
	// done is closed once ranking and err are set.
	done    chan struct{}
	ranking computed
	err     error
}

func newRankings() *rankings {
	return &rankings{entries: make(map[string]*kept)}
}

// get returns the ranking of challenge c at now: the one kept, or being
// computed, for the same challenge and served until after now; otherwise the
// one compute gives, served until until (keptFor after now, or sooner when a
// week locks), and kept unless it fails. A computation that fails is not
// shared: the reads that waited for it try again, so that one whose request
// was cancelled does not fail the others. Every read lets the expired
// rankings go.
func (r *rankings) get(ctx context.Context, c Challenge, now, until time.Time, compute func() (computed, error)) (computed, error) {
	for {
		r.mu.Lock()
		r.expire(now)
		e, ok := r.entries[c.ID]
		if ok && finished(e) && (e.err != nil || !sameChallenge(e.challenge, c)) {
			delete(r.entries, c.ID)
			ok = false
		}
		if !ok {
			e = &kept{challenge: c, at: now, until: until, done: make(chan struct{}), err: errAborted}
			r.makeRoom()
			r.entries[c.ID] = e
			r.mu.Unlock()
			return r.run(c.ID, e, compute)
		}
		r.mu.Unlock()

		select {
		case <-e.done:
		case <-ctx.Done():
			return computed{}, ctx.Err()
		}
		if e.err == nil && sameChallenge(e.challenge, c) {
			return e.ranking, nil
		}
	}
}

// run computes the ranking of e, then lets its readers have it; a failed one
// is no longer kept.
func (r *rankings) run(id string, e *kept, compute func() (computed, error)) (computed, error) {
	defer func() {
		close(e.done)
		if e.err != nil {
			r.mu.Lock()
			if r.entries[id] == e {
				delete(r.entries, id)
			}
			r.mu.Unlock()
		}
	}()
	e.ranking, e.err = compute()
	return e.ranking, e.err
}

// expire lets go the rankings no longer served at now, those being computed
// too: a read after their end computes again. r.mu is held.
func (r *rankings) expire(now time.Time) {
	for id, e := range r.entries {
		if !now.Before(e.until) {
			delete(r.entries, id)
		}
	}
}

// makeRoom lets go the oldest rankings computed until there are fewer than
// keptChallenges. One being computed stays, so that its challenge is
// computed once at a time: only as many as the reads under way may then go
// past the bound. r.mu is held.
func (r *rankings) makeRoom() {
	for len(r.entries) >= keptChallenges {
		oldest := ""
		for id, e := range r.entries {
			if finished(e) && (oldest == "" || e.at.Before(r.entries[oldest].at)) {
				oldest = id
			}
		}
		if oldest == "" {
			return
		}
		delete(r.entries, oldest)
	}
}

// sameChallenge reports whether a ranking computed for challenge a holds for
// b: the same window and categories with their settings. Who reads it
// (ReadsActivities) does not count: every reader shares the ranking.
func sameChallenge(a, b Challenge) bool {
	return a.ID == b.ID && a.Timezone == b.Timezone && a.StartsAt.Equal(b.StartsAt) && a.EndsAt.Equal(b.EndsAt) &&
		reflect.DeepEqual(a.Categories, b.Categories)
}

// finished reports whether the computation of e is over.
func finished(e *kept) bool {
	select {
	case <-e.done:
		return true
	default:
		return false
	}
}
