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
