// Package version carries build metadata stamped in at link time.
package version

// Values are overridden with -ldflags -X by the Makefile.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String renders the full build stamp.
func String() string {
	return Version + " (" + Commit + ", built " + Date + ")"
}
