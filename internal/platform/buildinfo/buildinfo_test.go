package buildinfo

import "testing"

// Under go test the linker has set nothing: the build is told apart from a
// real release by its placeholders.
func TestGetWithoutLinkerFlags(t *testing.T) {
	want := Info{Version: "dev", Commit: "unknown", Date: "unknown"}
	if got := Get(); got != want {
		t.Fatalf("Get() = %+v, want %+v", got, want)
	}
}
