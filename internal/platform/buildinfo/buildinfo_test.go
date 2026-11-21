package buildinfo

import (
	"strings"
	"testing"
)

func TestStringCarriesVersionAndCommit(t *testing.T) {
	oldV, oldC := Version, Commit
	t.Cleanup(func() { Version, Commit = oldV, oldC })
	Version, Commit = "1.2.3-alpha.x.4", "abc1234"

	got := String()
	for _, want := range []string{"cicada 1.2.3-alpha.x.4", "(abc1234)"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, want it to contain %q", got, want)
		}
	}
}
