//go:build integration

package db_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
)

func TestGormLogsQueriesWithoutValues(t *testing.T) {
	var logs bytes.Buffer
	gdb, err := db.NewGorm(testdb.New(t), slog.New(slog.NewJSONHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}

	// A failing query is logged: its value must not be.
	const secret = "someone@example.com"
	if err := gdb.WithContext(t.Context()).Exec("SELECT * FROM no_such_table WHERE email = ?", secret).Error; err == nil {
		t.Fatal("query on a missing table succeeded")
	}
	if !strings.Contains(logs.String(), "no_such_table") {
		t.Fatalf("failed query not logged: %s", logs.String())
	}
	if strings.Contains(logs.String(), secret) {
		t.Fatalf("log contains the query value: %s", logs.String())
	}
	if !strings.HasPrefix(strings.TrimSpace(logs.String()), "{") {
		t.Fatalf("log is not JSON: %s", logs.String())
	}
}
