// Package buildinfo tells which build is running: its version, the commit it
// was built from and when. The linker sets the three values (-ldflags -X, see
// deploy/Dockerfile); a build that skipped it says "dev" and "unknown".
package buildinfo

// The linker writes these variables: -X only reaches package-level strings,
// and nothing else changes them.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// Info identifies a build.
type Info struct {
	// Version is the semantic version tag (v1.2.3) the build was released as.
	Version string
	// Commit is the full SHA of the Git commit.
	Commit string
	// Date is the commit date (RFC 3339).
	Date string
}

// Get returns the build the process was made from.
func Get() Info {
	return Info{Version: version, Commit: commit, Date: date}
}
