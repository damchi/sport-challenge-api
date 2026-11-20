package db

import (
	"strings"
	"testing"
)

func TestNewPool(t *testing.T) {
	// Creating the pool does not connect: no database is needed here.
	// pool_max_conns of the URL gives way to the size passed by the caller.
	pool, err := NewPool(t.Context(), "postgres://sport@localhost:5432/sport_challenge?sslmode=disable&pool_max_conns=20", 3)
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	t.Cleanup(pool.Close)

	if got := pool.Config().ConnConfig.ConnectTimeout; got != connectTimeout {
		t.Fatalf("ConnectTimeout = %v, want %v", got, connectTimeout)
	}
	if got := pool.Config().MaxConns; got != 3 {
		t.Fatalf("MaxConns = %d, want 3", got)
	}
}

func TestNewPoolRejectsAnEmptyPool(t *testing.T) {
	if _, err := NewPool(t.Context(), "postgres://sport@localhost:5432/sport_challenge", 0); err == nil {
		t.Fatal("NewPool(0) error = nil, want an error")
	}
}

func TestNewPoolInvalidURLHidesThePassword(t *testing.T) {
	const password = "hunter2"
	_, err := NewPool(t.Context(), "postgres://sport:"+password+"@localhost:notaport/x", 1)
	if err == nil {
		t.Fatal("NewPool() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "parse DATABASE_URL") {
		t.Errorf("error = %q, want it to name DATABASE_URL", err)
	}
	if strings.Contains(err.Error(), password) {
		t.Fatalf("error leaks the password: %q", err)
	}
}
