package main

import (
	"errors"
	"strings"
	"testing"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    command
		wantErr error
	}{
		{name: "up", args: []string{"up"}, want: command{name: "up"}},
		{name: "down one", args: []string{"down", "1"}, want: command{name: "down", n: 1}},
		{name: "down three", args: []string{"down", "3"}, want: command{name: "down", n: 3}},
		{name: "force", args: []string{"force", "8"}, want: command{name: "force", n: 8}},
		{name: "force zero", args: []string{"force", "0"}, want: command{name: "force", n: 0}},
		{name: "no command", wantErr: errUsage},
		{name: "unknown command", args: []string{"drop"}, wantErr: errUsage},
		{name: "up with argument", args: []string{"up", "1"}, wantErr: errUsage},
		{name: "down without count", args: []string{"down"}, wantErr: errUsage},
		{name: "down zero", args: []string{"down", "0"}, wantErr: errUsage},
		{name: "down at the limit", args: []string{"down", "3"}, want: command{name: "down", n: 3}},
		{name: "down above the limit", args: []string{"down", "4"}, wantErr: errUsage},
		{name: "down everything", args: []string{"down", "999"}, wantErr: errUsage},
		{name: "down negative", args: []string{"down", "-1"}, wantErr: errUsage},
		{name: "down not a number", args: []string{"down", "all"}, wantErr: errUsage},
		{name: "force without version", args: []string{"force"}, wantErr: errUsage},
		{name: "force negative", args: []string{"force", "-1"}, wantErr: errUsage},
		{name: "force not a number", args: []string{"force", "latest"}, wantErr: errUsage},
		{name: "too many arguments", args: []string{"down", "1", "2"}, wantErr: errUsage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCommand(tt.args)
			if !errors.Is(err, tt.wantErr) || got != tt.want {
				t.Fatalf("parseCommand(%v) = %+v, %v; want %+v, %v", tt.args, got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestRunRequiresDatabaseURL(t *testing.T) {
	err := run([]string{"up"}, func(string) string { return "  " })
	if !errors.Is(err, errDatabaseURL) {
		t.Fatalf("run without DATABASE_URL = %v, want %v", err, errDatabaseURL)
	}
}

func TestRunRejectsUsageBeforeConnecting(t *testing.T) {
	// The database URL is invalid on purpose: a bad command must fail first.
	err := run([]string{"drop"}, func(string) string { return "not a url" })
	if !errors.Is(err, errUsage) {
		t.Fatalf("run with an unknown command = %v, want %v", err, errUsage)
	}
}

func TestRunNeverShowsThePasswordOfTheDatabaseURL(t *testing.T) {
	// Nothing listens on port 1: the connection fails with the driver's error.
	url := "postgres://sport:s3cr3t-p4ss@127.0.0.1:1/sport_challenge?sslmode=disable" //nolint:gosec // a dummy password, to prove it is not shown
	err := run([]string{"up"}, func(string) string { return url })
	if err == nil {
		t.Fatal("run against an unreachable database succeeded")
	}
	if strings.Contains(err.Error(), "s3cr3t-p4ss") {
		t.Fatalf("the error shows the password: %v", err)
	}
}
