package db

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// weekLockClass and weekLockID name the advisory lock that orders the
// activity writes and the judgement of the weeks. Two keys: PostgreSQL keeps
// them apart from the single bigint keys (those of lockActivities), so no
// hash can collide with it.
const (
	weekLockClass int32 = 7130
	weekLockID    int32 = 1
)

// ShareWeekLock takes the week lock shared until the end of tx. An activity
// write takes it before reading the clock to check the lock of its weeks, so
// that AwaitWeekWrites waits for it; the worker takes it for each challenge
// it judges, so that no admin change (LockWeeks) runs in between. Those who
// hold it shared do not wait for each other.
func ShareWeekLock(tx *gorm.DB) error {
	if err := tx.Exec("SELECT pg_advisory_xact_lock_shared(?, ?)", weekLockClass, weekLockID).Error; err != nil {
		return fmt.Errorf("share week lock: %w", err)
	}
	return nil
}

// LockWeeks takes the week lock alone until the end of tx: it waits for
// every transaction holding it shared (activity writes, runs of the worker
// on a challenge) and holds back the next ones until tx ends. An admin
// change takes it first in its transaction, before any row, as the activity
// writes do (ShareWeekLock, then the author's row), so that none waits for
// the other; it is taken again by the recompute, which changes nothing.
func LockWeeks(tx *gorm.DB) error {
	if err := tx.Exec("SELECT pg_advisory_xact_lock(?, ?)", weekLockClass, weekLockID).Error; err != nil {
		return fmt.Errorf("lock weeks: %w", err)
	}
	return nil
}

// AwaitWeekWrites waits for every transaction holding the week lock shared
// to end. Called after reading the clock, it returns once every activity
// write that checked the lock of its weeks before that instant is committed
// or rolled back: a week locked at that instant can then be judged. It takes
// the lock alone for an instant, in a transaction of its own, even when ctx
// carries one of InTx (the lock would last as long as it).
func AwaitWeekWrites(ctx context.Context, gdb *gorm.DB) error {
	err := gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Exec("SELECT pg_advisory_xact_lock(?, ?)", weekLockClass, weekLockID).Error
	})
	if err != nil {
		return fmt.Errorf("await week writes: %w", err)
	}
	return nil
}
