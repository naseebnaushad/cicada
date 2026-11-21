// Command cicada turns a machine into a pool of single-use GitHub Actions runners.
//
// Milestone 0 ships only the version command; the real commands arrive in Milestone 1
// (see documents/Action Plan.md).
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/naseebnaushad/cicada/internal/platform/buildinfo"
)

const usage = `cicada: simple self-hosted CI/CD runners

Usage:
  cicada version   Print the version
  cicada help      Show this help

More commands (up, status, doctor, ...) arrive in Milestone 1.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes one command and returns the process exit code. It takes its streams as
// arguments so tests can drive it without touching the real ones.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return 0
	}
	switch args[0] {
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, buildinfo.String())
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "cicada: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}
