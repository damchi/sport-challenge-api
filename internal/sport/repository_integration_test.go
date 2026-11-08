//go:build integration

package sport

import (
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
)

// TestListReturnsTheReferenceData reads the catalogue seeded by the
// migrations (README §2.4, decision D9).
func TestListReturnsTheReferenceData(t *testing.T) {
	gdb, err := db.NewGorm(testdb.New(t), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	sports, err := NewRepository(gdb).List(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	type row struct {
		mode   string
		factor float64 // 0 when null
	}
	want := map[string]row{
		"climbing": {"none", 0}, "hike": {"required", 1}, "other": {"optional", 1},
		"ride": {"required", 0.3}, "roller": {"required", 0.5}, "rowing": {"required", 0.5},
		"run": {"required", 1}, "strength": {"none", 0}, "swim": {"required", 1},
		"walk": {"required", 1}, "xc_ski": {"required", 0.5}, "yoga": {"none", 0},
	}
	if len(sports) != len(want) {
		t.Fatalf("%d sports, want %d", len(sports), len(want))
	}
	codes := make([]string, 0, len(sports))
	for _, s := range sports {
		codes = append(codes, s.Code)
		w, ok := want[s.Code]
		if !ok {
			t.Fatalf("unexpected sport %q", s.Code)
		}
		var got float64
		if s.SuggestedFactor != nil {
			got = *s.SuggestedFactor
		}
		if s.DistanceMode != w.mode || got != w.factor || (w.mode == "none") != (s.SuggestedFactor == nil) {
			t.Errorf("%s = %s %v, want %s %v", s.Code, s.DistanceMode, s.SuggestedFactor, w.mode, w.factor)
		}
	}
	if !slices.IsSorted(codes) {
		t.Fatalf("codes %v, want them sorted", codes)
	}
}
