//go:build integration

package db

import (
	"testing"
	"time"

	"gorm.io/gorm"
)

// holdShared opens a transaction holding the week lock shared, as an
// activity write does, until release is closed; held is closed once it does.
func holdShared(t *testing.T, gdb *gorm.DB, release <-chan struct{}) (held <-chan struct{}, done <-chan error) {
	t.Helper()
	h, d := make(chan struct{}), make(chan error, 1)
	go func() {
		d <- WithTx(t.Context(), gdb, func(tx *gorm.DB) error {
			if err := ShareWeekLock(tx); err != nil {
				return err
			}
			close(h)
			<-release
			return nil
		})
	}()
	return h, d
}

func TestAwaitWeekWrites(t *testing.T) {
	gdb, _ := newTxDB(t)

	t.Run("waits for the writes holding the lock", func(t *testing.T) {
		release := make(chan struct{})
		held, written := holdShared(t, gdb, release)
		<-held
		awaited := make(chan error, 1)
		go func() { awaited <- AwaitWeekWrites(t.Context(), gdb) }()
		select {
		case err := <-awaited:
			t.Fatalf("AwaitWeekWrites() = %v while a write holds the lock, want it to wait", err)
		case <-time.After(200 * time.Millisecond):
		}
		close(release)
		if err := <-written; err != nil {
			t.Fatal(err)
		}
		if err := <-awaited; err != nil {
			t.Fatalf("AwaitWeekWrites() = %v once the write committed, want nil", err)
		}
	})

	t.Run("writes do not wait for each other", func(t *testing.T) {
		release := make(chan struct{})
		first, firstDone := holdShared(t, gdb, release)
		second, secondDone := holdShared(t, gdb, release)
		for _, held := range []<-chan struct{}{first, second} {
			select {
			case <-held:
			case <-time.After(5 * time.Second):
				t.Fatal("a write waited for another one to take the lock")
			}
		}
		close(release)
		for _, done := range []<-chan error{firstDone, secondDone} {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
	})

	t.Run("returns at once without a write", func(t *testing.T) {
		if err := AwaitWeekWrites(t.Context(), gdb); err != nil {
			t.Fatal(err)
		}
	})
}
