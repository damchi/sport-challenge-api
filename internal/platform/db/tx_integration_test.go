//go:build integration

package db

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/gorm"

	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
)

func newTxDB(t *testing.T) (*gorm.DB, *pgxpool.Pool) {
	t.Helper()
	pool := testdb.New(t)
	gdb, err := NewGorm(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return gdb, pool
}

func insertUser(tx *gorm.DB, subject string) error {
	return tx.Exec("INSERT INTO users (auth_subject) VALUES (?)", subject).Error
}

func usersWith(t *testing.T, pool *pgxpool.Pool, subject string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM users WHERE auth_subject = $1", subject).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWithTx(t *testing.T) {
	gdb, pool := newTxDB(t)
	failure := errors.New("second write failed")

	t.Run("commits when fn succeeds", func(t *testing.T) {
		err := WithTx(t.Context(), gdb, func(tx *gorm.DB) error {
			if err := insertUser(tx, "tx|a"); err != nil {
				return err
			}
			return insertUser(tx, "tx|b")
		})
		if err != nil || usersWith(t, pool, "tx|a") != 1 || usersWith(t, pool, "tx|b") != 1 {
			t.Fatalf("err = %v, want both rows committed", err)
		}
	})

	t.Run("rolls back when fn fails", func(t *testing.T) {
		err := WithTx(t.Context(), gdb, func(tx *gorm.DB) error {
			if err := insertUser(tx, "tx|c"); err != nil {
				return err
			}
			return failure
		})
		if !errors.Is(err, failure) || usersWith(t, pool, "tx|c") != 0 {
			t.Fatalf("err = %v, want the failure and no row", err)
		}
	})

	t.Run("rolls back on panic, which goes on", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("the panic was swallowed")
			}
			if n := usersWith(t, pool, "tx|d"); n != 0 {
				t.Fatalf("%d rows after the panic, want 0", n)
			}
		}()
		_ = WithTx(t.Context(), gdb, func(tx *gorm.DB) error {
			if err := insertUser(tx, "tx|d"); err != nil {
				return err
			}
			panic("boom")
		})
	})
}
