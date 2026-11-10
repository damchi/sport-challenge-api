package main

import (
	"errors"
	"testing"
)

func TestParseTask(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    string
		wantErr error
	}{
		{name: "close-weeks", args: []string{"close-weeks"}, want: "close-weeks"},
		{name: "no task", wantErr: errUsage},
		{name: "unknown task", args: []string{"sync"}, wantErr: errUsage},
		{name: "two tasks", args: []string{"close-weeks", "close-weeks"}, wantErr: errUsage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTask(tt.args)
			if !errors.Is(err, tt.wantErr) || got != tt.want {
				t.Fatalf("parseTask(%v) = %q, %v; want %q, %v", tt.args, got, err, tt.want, tt.wantErr)
			}
		})
	}
}
