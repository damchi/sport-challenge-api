package db

import (
	"strings"
	"testing"
)

func TestNewPool(t *testing.T) {
	// Creating the pool does not connect: no database is needed here.
	pool, err := NewPool(t.Context(), "postgres://sport@localhost:5432/sport_challenge?sslmode=disable")
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	t.Cleanup(pool.Close)

	if got := pool.Config().ConnConfig.ConnectTimeout; got != connectTimeout {
		t.Fatalf("ConnectTimeout = %v, want %v", got, connectTimeout)
	}
}

func TestNewPoolInvalidURLHidesThePassword(t *testing.T) {
	const password = "hunter2"
	_, err := NewPool(t.Context(), "postgres://sport:"+password+"@localhost:notaport/x")
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
