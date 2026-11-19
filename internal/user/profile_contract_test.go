package user

import (
	"testing"

	"github.com/damchi/sport-challenge-api/internal/api"
)

// TestPseudoRuleMatchesTheContract keeps the service rule and the pattern of
// api/openapi.yaml, checked by the request validator, identical.
func TestPseudoRuleMatchesTheContract(t *testing.T) {
	spec, err := api.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := spec.Components.Schemas["Pseudo"]
	if !ok || ref.Value == nil {
		t.Fatal("schema Pseudo missing from the contract")
	}
	if got, want := ref.Value.Pattern, pseudoPattern.String(); got != want {
		t.Fatalf("contract pattern %q, service rule %q: keep them identical", got, want)
	}
}

// TestNameRuleMatchesTheContract does the same for the first and last names.
func TestNameRuleMatchesTheContract(t *testing.T) {
	spec, err := api.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := spec.Components.Schemas["PersonName"]
	if !ok || ref.Value == nil {
		t.Fatal("schema PersonName missing from the contract")
	}
	if got, want := ref.Value.Pattern, namePattern.String(); got != want {
		t.Fatalf("contract pattern %q, service rule %q: keep them identical", got, want)
	}
	if ref.Value.MaxLength == nil || *ref.Value.MaxLength != maxNameLength {
		t.Fatalf("contract maxLength %v, service rule %d: keep them identical", ref.Value.MaxLength, maxNameLength)
	}
}
