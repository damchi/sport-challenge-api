package sport

import (
	"errors"
	"maps"
	"testing"
)

func TestServiceSuggestedFactors(t *testing.T) {
	got, err := NewService(fakeRepository{sports: []Sport{
		{Code: "other", DistanceMode: "optional", SuggestedFactor: factor(1)},
		{Code: "ride", DistanceMode: "required", SuggestedFactor: factor(0.3)},
		{Code: "yoga", DistanceMode: "none"},
	}}).SuggestedFactors(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Sports without distance are left out: no factor applies to them.
	if want := map[string]float64{"other": 1, "ride": 0.3}; !maps.Equal(got, want) {
		t.Fatalf("SuggestedFactors() = %v, want %v", got, want)
	}

	storage := errors.New("connection refused")
	if _, err := NewService(fakeRepository{err: storage}).SuggestedFactors(t.Context()); !errors.Is(err, storage) {
		t.Fatalf("error = %v, want it to wrap %v", err, storage)
	}
}

func TestServiceDistanceMode(t *testing.T) {
	svc := NewService(fakeRepository{sports: []Sport{
		{Code: "run", DistanceMode: "required", SuggestedFactor: factor(1)},
		{Code: "yoga", DistanceMode: "none"},
	}})
	tests := []struct {
		code  string
		mode  string
		found bool
	}{
		{code: "run", mode: "required", found: true},
		{code: "yoga", mode: "none", found: true},
		{code: "chess"},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			mode, found, err := svc.DistanceMode(t.Context(), tt.code)
			if err != nil || mode != tt.mode || found != tt.found {
				t.Fatalf("DistanceMode(%q) = %q, %v, %v, want %q, %v", tt.code, mode, found, err, tt.mode, tt.found)
			}
		})
	}

	storage := errors.New("connection refused")
	if _, _, err := NewService(fakeRepository{err: storage}).DistanceMode(t.Context(), "run"); !errors.Is(err, storage) {
		t.Fatalf("error = %v, want it to wrap %v", err, storage)
	}
}
