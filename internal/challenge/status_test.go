package challenge

import (
	"testing"
	"time"
)

func TestStatusAt(t *testing.T) {
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 2, 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 21)
	tests := []struct {
		now  time.Time
		want string
	}{
		{start.Add(-time.Second), StatusUpcoming},
		{start, StatusOngoing},
		{end.Add(-time.Second), StatusOngoing},
		{end, StatusFinished},
	}
	for _, tt := range tests {
		if got := StatusAt(tt.now, start, end); got != tt.want {
			t.Errorf("StatusAt(%s) = %s, want %s", tt.now, got, tt.want)
		}
	}
}
