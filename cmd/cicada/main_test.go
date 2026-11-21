package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"no args shows help", nil, 0, "Usage:", ""},
		{"version", []string{"version"}, 0, "cicada ", ""},
		{"help", []string{"help"}, 0, "Usage:", ""},
		{"unknown command", []string{"nope"}, 2, "", `unknown command "nope"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if code := run(tc.args, &out, &errOut); code != tc.wantCode {
				t.Errorf("exit code = %d, want %d", code, tc.wantCode)
			}
			if !strings.Contains(out.String(), tc.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", out.String(), tc.wantStdout)
			}
			if !strings.Contains(errOut.String(), tc.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut.String(), tc.wantStderr)
			}
		})
	}
}
