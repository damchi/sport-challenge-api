//go:build integration

package db

import (
	"context"
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

func TestInTx(t *testing.T) {
	gdb, pool := newTxDB(t)
	failure := errors.New("later write failed")

	t.Run("Conn joins the transaction of the context", func(t *testing.T) {
		err := InTx(t.Context(), gdb, func(ctx context.Context) error {
			if err := insertUser(Conn(ctx, gdb), "intx|a"); err != nil {
				return err
			}
			// Not visible outside before the commit: Conn wrote in the transaction.
			if n := usersWith(t, pool, "intx|a"); n != 0 {
				t.Fatalf("%d rows seen outside before the commit, want 0", n)
			}
			return nil
		})
		if err != nil || usersWith(t, pool, "intx|a") != 1 {
			t.Fatalf("err = %v, want the row committed", err)
		}
	})

	t.Run("a nested WithTx commits and rolls back with it", func(t *testing.T) {
		err := InTx(t.Context(), gdb, func(ctx context.Context) error {
			if err := WithTx(ctx, gdb, func(tx *gorm.DB) error { return insertUser(tx, "intx|b") }); err != nil {
				return err
			}
			return failure
		})
		if !errors.Is(err, failure) || usersWith(t, pool, "intx|b") != 0 {
			t.Fatalf("err = %v, want the failure and the nested write rolled back", err)
		}
	})

	t.Run("a failed nested WithTx rolls back only its writes", func(t *testing.T) {
		err := InTx(t.Context(), gdb, func(ctx context.Context) error {
			if err := insertUser(Conn(ctx, gdb), "intx|c"); err != nil {
				return err
			}
			nested := WithTx(ctx, gdb, func(tx *gorm.DB) error {
				if err := insertUser(tx, "intx|d"); err != nil {
					return err
				}
				return failure
			})
			if !errors.Is(nested, failure) {
				t.Fatalf("nested err = %v, want the failure", nested)
			}
			return nil
		})
		if err != nil || usersWith(t, pool, "intx|c") != 1 || usersWith(t, pool, "intx|d") != 0 {
			t.Fatalf("err = %v, want the outer row only", err)
		}
	})

	t.Run("Conn outside of a transaction", func(t *testing.T) {
		if err := insertUser(Conn(t.Context(), gdb), "intx|e"); err != nil || usersWith(t, pool, "intx|e") != 1 {
			t.Fatalf("err = %v, want the row written directly", err)
		}
	})
}
