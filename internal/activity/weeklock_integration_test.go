//go:build integration

package activity

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"gorm.io/gorm"

	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
)

// weekLockProbe gives the challenges of staticParticipations and records
// whether the transaction reading them holds the week lock shared: the
// service reads them right before the clock, to check the lock of the weeks.
type weekLockProbe struct {
	staticParticipations
	gdb   *gorm.DB
	reads []bool
}

func (p *weekLockProbe) Participations(ctx context.Context, userID string) ([]Participation, error) {
	var held int
	err := db.Conn(ctx, p.gdb).Raw(`SELECT count(*) FROM pg_locks
		WHERE locktype = 'advisory' AND mode = 'ShareLock' AND granted AND pid = pg_backend_pid()`).Scan(&held).Error
	if err != nil {
		return nil, fmt.Errorf("read locks: %w", err)
	}
	p.reads = append(p.reads, held == 1)
	return p.staticParticipations.Participations(ctx, userID)
}

// TestWritesShareTheWeekLock: the worker waits for the writes that hold the
// week lock (db.AwaitWeekWrites), so each one takes it before checking the
// lock of its weeks.
func TestWritesShareTheWeekLock(t *testing.T) {
	f := setupRecord(t)
	probe := &weekLockProbe{staticParticipations: staticParticipations{challengeOf(t, "c1")}, gdb: f.gdb}
	svc := NewService(NewRepository(f.gdb), fakeProfiles{complete: true}, catalogue, probe, nil, clock.NewFake(recordNow))

	rec, err := svc.Record(t.Context(), f.user, nil, run(toronto(t, 2026, 6, 10, 7)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Update(t.Context(), f.user, rec.ID, run(toronto(t, 2026, 6, 11, 7))); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(t.Context(), f.user, rec.ID); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(probe.reads, []bool{true, true, true}) {
		t.Fatalf("week lock held at the checks of record, update and delete: %v, want 3 times true", probe.reads)
	}
}
