// Package buildinfo holds the version stamped into the binary at build time.
package buildinfo

import (
	"fmt"
	"runtime"
)

// Version and Commit are set with -ldflags at build time (see the Makefile).
// The defaults mark a build made without the stamp, such as `go run`.
var (
	Version = "0.0.0-dev"
	Commit  = "unknown"
)

// String returns the one-line version shown by `cicada version`.
func String() string {
	return fmt.Sprintf("cicada %s (%s) %s/%s %s", Version, Commit, runtime.GOOS, runtime.GOARCH, runtime.Version())
}
