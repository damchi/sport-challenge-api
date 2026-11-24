package admin

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/damchi/sport-challenge-api/internal/leaderboard"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

// correctionLog records, in order, what the envelope did.
type correctionLog struct {
	steps      []string
	rolledBack bool
	// eliminations are those State reads by challenge, before then after.
	eliminations map[string][][]EliminationRow
	recomputeErr error
	lockErr      error
}

func (l *correctionLog) add(step string) { l.steps = append(l.steps, step) }

func (l *correctionLog) InLockedTx(ctx context.Context, fn func(ctx context.Context) error) error {
	l.add("lock")
	err := fn(ctx)
	if err != nil {
		l.rolledBack = true
	}
	return err
}

func (l *correctionLog) LockParticipants(_ context.Context, ids []string) error {
	l.add("lock participants " + strings.Join(ids, ","))
	return l.lockErr
}

func (l *correctionLog) State(_ context.Context, id string) (ChallengeState, error) {
	l.add("state " + id)
	reads := l.eliminations[id]
	if len(reads) == 0 {
		return ChallengeState{}, nil
	}
	out := reads[0]
	l.eliminations[id] = reads[1:]
	return ChallengeState{Eliminations: out}, nil
}

func (l *correctionLog) Recompute(_ context.Context, id string) error {
	l.add("recompute " + id)
	return l.recomputeErr
}

func (l *correctionLog) Weeks(_ context.Context, _ auth.Actor, id string) (leaderboard.Weeks, error) {
	l.add("weeks " + id)
	return leaderboard.Weeks{}, nil
}

func (l *correctionLog) Get(_ context.Context, actor auth.Actor, id string) (leaderboard.Leaderboard, error) {
	l.add("rank " + id)
	if !actor.Admin {
		return leaderboard.Leaderboard{}, errors.New("ranked for a non-admin")
	}
	n := strings.Count(strings.Join(l.steps, ","), "rank "+id)
	return leaderboard.Leaderboard{Categories: make([]leaderboard.CategoryRanking, n)}, nil
}

// journalLog is a journal store that logs its writes.
type journalLog struct {
	log     *correctionLog
	records []ActionRecord
}

func (j *journalLog) InsertAction(_ context.Context, a ActionRecord) error {
	j.log.add("journal")
	j.records = append(j.records, a)
	return nil
}

func (j *journalLog) EraseUserValues(context.Context, string) error {
	j.log.add("erase journal")
	return nil
}

func newCorrections(log *correctionLog) (*Corrections, *journalLog) {
	journal := &journalLog{log: log}
	return NewCorrections(log, NewJournal(journal, clock.NewFake(statsAt)), log, log), journal
}

var admin = auth.Actor{ID: annID, Admin: true}

// correction touches challengeID and inviteID (two challenges) and logs its change.
func correction(log *correctionLog, reason string, dryRun bool) Correction {
	return Correction{
		Touched: func(context.Context) ([]string, error) {
			log.add("touched")
			return []string{challengeID, inviteID}, nil
		},
		Apply: func(context.Context) (Entry, error) {
			log.add("apply")
			return Entry{Action: "activity.update", TargetType: TargetActivity, TargetID: actionID}, nil
		},
		Reason: reason,
		DryRun: dryRun,
	}
}

func TestCorrectionsRun(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		name := map[bool]string{false: "committed", true: "dry run"}[dryRun]
		t.Run(name, func(t *testing.T) {
			bob := UserRef{ID: bobID}
			log := &correctionLog{eliminations: map[string][][]EliminationRow{
				challengeID: {nil, {{User: bob, WeekNumber: 2, DistanceMeters: 6999, RequiredMeters: 7000}}},
			}}
			s, journal := newCorrections(log)
			previews, err := s.Run(t.Context(), admin, correction(log, " Oubli ", dryRun))
			if err != nil {
				t.Fatal(err)
			}
			want := []string{
				"lock", "touched", "lock participants " + challengeID + "," + inviteID,
				"rank " + challengeID, "weeks " + challengeID, "state " + challengeID, "rank " + inviteID, "weeks " + inviteID, "state " + inviteID,
				"apply", "journal",
				"recompute " + challengeID, "rank " + challengeID, "weeks " + challengeID, "state " + challengeID,
				"recompute " + inviteID, "rank " + inviteID, "weeks " + inviteID, "state " + inviteID,
			}
			if !reflect.DeepEqual(log.steps, want) {
				t.Fatalf("steps =\n%v\nwant\n%v", log.steps, want)
			}
			if log.rolledBack != dryRun {
				t.Fatalf("rolled back = %v, want %v", log.rolledBack, dryRun)
			}
			r := journal.records[0]
			if r.AdminID != annID || !reflect.DeepEqual(r.ChallengeIDs, []string{challengeID, inviteID}) || r.Reason != "Oubli" {
				t.Fatalf("journal line %+v, want the admin, the challenges touched and the reason", r)
			}
			if len(previews) != 2 || previews[0].ChallengeID != challengeID || previews[1].ChallengeID != inviteID {
				t.Fatalf("previews = %+v, want one per challenge touched, in order", previews)
			}
			if len(previews[0].Before.Categories) != 1 || len(previews[0].After.Categories) != 2 {
				t.Fatalf("preview %+v, want the ranking before then after", previews[0])
			}
			wantChange := []EliminationChange{{User: bob, After: &EliminationRow{User: bob, WeekNumber: 2, DistanceMeters: 6999, RequiredMeters: 7000}}}
			if !reflect.DeepEqual(previews[0].Eliminations, wantChange) || len(previews[1].Eliminations) != 0 {
				t.Fatalf("elimination changes %+v / %+v, want b eliminated in the first challenge only", previews[0].Eliminations, previews[1].Eliminations)
			}
		})
	}
}

func TestCorrectionsRunRefusesABadReasonBeforeTheTransaction(t *testing.T) {
	tests := []struct {
		reason string
		dryRun bool
	}{
		{"ab", false},
		{"ab", true},
		{strings.Repeat("a", 501), false},
		{strings.Repeat("a", 501), true},
		// Only a preview goes without a reason; blanks are one, too short.
		{"", false},
		{"   ", true},
	}
	for _, tt := range tests {
		log := &correctionLog{}
		s, _ := newCorrections(log)
		if _, err := s.Run(t.Context(), admin, correction(log, tt.reason, tt.dryRun)); fieldsOf(err) != "reason" {
			t.Fatalf("reason %q, dry run %v: got %v, want validation_failed on reason", tt.reason, tt.dryRun, err)
		}
		if len(log.steps) != 0 {
			t.Fatalf("steps %v, want nothing locked nor applied", log.steps)
		}
	}
}

func TestCorrectionsRunPreviewsWithoutAReason(t *testing.T) {
	log := &correctionLog{}
	s, journal := newCorrections(log)
	previews, err := s.Run(t.Context(), admin, correction(log, "", true))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"lock", "touched", "lock participants " + challengeID + "," + inviteID,
		"rank " + challengeID, "weeks " + challengeID, "state " + challengeID, "rank " + inviteID, "weeks " + inviteID, "state " + inviteID,
		"apply",
		"recompute " + challengeID, "rank " + challengeID, "weeks " + challengeID, "state " + challengeID,
		"recompute " + inviteID, "rank " + inviteID, "weeks " + inviteID, "state " + inviteID,
	}
	if !reflect.DeepEqual(log.steps, want) {
		t.Fatalf("steps =\n%v\nwant\n%v", log.steps, want)
	}
	if !log.rolledBack || len(journal.records) != 0 || len(previews) != 2 || previews[0].After == nil {
		t.Fatalf("rolled back %v, journal %v, previews %+v: want a preview, rolled back, no journal line", log.rolledBack, journal.records, previews)
	}
}

func TestCorrectionsRunWithoutItsParts(t *testing.T) {
	log := &correctionLog{}
	s, _ := newCorrections(log)
	if _, err := s.Run(t.Context(), admin, Correction{Reason: "Oubli"}); err == nil || len(log.steps) != 0 {
		t.Fatalf("got %v, steps %v, want an error and nothing done", err, log.steps)
	}
}

func TestCorrectionsRunIsHiddenFromANonAdmin(t *testing.T) {
	log := &correctionLog{}
	s, _ := newCorrections(log)
	_, err := s.Run(t.Context(), auth.Actor{ID: annID}, correction(log, "Oubli", false))
	if !errors.Is(err, apperr.New(apperr.CodeNotFound, nil)) {
		t.Fatalf("got %v, want not_found", err)
	}
	if len(log.steps) != 0 {
		t.Fatalf("steps %v before the policy refused", log.steps)
	}
}

func TestCorrectionsRunRollsBack(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name     string
		prepare  func(log *correctionLog, c *Correction)
		wantErr  func(error) bool
		lastStep string
	}{
		{
			name: "a change refused",
			prepare: func(log *correctionLog, c *Correction) {
				c.Apply = func(context.Context) (Entry, error) { log.add("apply"); return Entry{}, boom }
			},
			wantErr: func(err error) bool { return errors.Is(err, boom) }, lastStep: "apply",
		},
		{
			name: "touched challenges not read",
			prepare: func(log *correctionLog, c *Correction) {
				c.Touched = func(context.Context) ([]string, error) { log.add("touched"); return nil, boom }
			},
			wantErr: func(err error) bool { return errors.Is(err, boom) }, lastStep: "touched",
		},
		{
			name: "participants not locked", prepare: func(log *correctionLog, _ *Correction) { log.lockErr = boom },
			wantErr: func(err error) bool { return errors.Is(err, boom) }, lastStep: "lock participants " + challengeID + "," + inviteID,
		},
		{
			name: "a recompute that fails", prepare: func(log *correctionLog, _ *Correction) { log.recomputeErr = boom },
			wantErr: func(err error) bool { return errors.Is(err, boom) }, lastStep: "recompute " + challengeID,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := &correctionLog{}
			c := correction(log, "Oubli", false)
			tt.prepare(log, &c)
			s, _ := newCorrections(log)
			previews, err := s.Run(t.Context(), admin, c)
			if !tt.wantErr(err) || previews != nil {
				t.Fatalf("got %v, %+v", err, previews)
			}
			if !log.rolledBack || log.steps[len(log.steps)-1] != tt.lastStep {
				t.Fatalf("steps %v, rolled back %v, want a rollback after %q", log.steps, log.rolledBack, tt.lastStep)
			}
		})
	}
}

func TestEliminationChanges(t *testing.T) {
	a, b, c, d := UserRef{ID: "a"}, UserRef{ID: "b"}, UserRef{ID: "c"}, UserRef{ID: "d"}
	row := func(u UserRef, week, meters int) EliminationRow {
		return EliminationRow{User: u, WeekNumber: week, DistanceMeters: meters, RequiredMeters: 7000}
	}
	before := []EliminationRow{row(a, 2, 6000), row(b, 2, 6500), row(c, 3, 8000)}
	after := []EliminationRow{row(a, 2, 6000), row(b, 3, 8500), row(d, 4, 9000)}
	got := eliminationChanges(before, after)
	bBefore, bAfter, dAfter, cBefore := row(b, 2, 6500), row(b, 3, 8500), row(d, 4, 9000), row(c, 3, 8000)
	want := []EliminationChange{
		{User: b, Before: &bBefore, After: &bAfter},
		{User: d, After: &dAfter},
		{User: c, Before: &cBefore},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changes = %+v, want %+v", got, want)
	}
	if got := eliminationChanges(before, before); len(got) != 0 {
		t.Fatalf("changes = %+v, want none", got)
	}
}
