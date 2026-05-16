package codex

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paultyng/testagent/cmd/claude"
	"github.com/paultyng/testagent/internal/configvalidate"
)

func TestRunValidate_Codex(t *testing.T) {
	// Test cases drive the validator over a per-test CODEX_HOME so
	// runValidate's $CODEX_HOME-or-~/.codex resolver picks up the
	// fixture. The env var is set per-subtest (no t.Parallel — env
	// is process-global) but each test stands alone.
	cases := []struct {
		name        string
		body        string
		writeFile   bool
		strict      bool
		wantCode    int
		wantSubstrs []string
	}{
		{
			name: "valid lax",
			body: `
[[hooks.session_start]]
matcher = "*"
[[hooks.session_start.hooks]]
type = "command"
command = "echo hi"
`,
			writeFile: true,
			strict:    false,
			wantCode:  configvalidate.ExitOK,
		},
		{
			name: "valid strict",
			body: `
[[hooks.pre_tool_use]]
matcher = "shell"
[[hooks.pre_tool_use.hooks]]
type = "command"
command = "echo hi"
`,
			writeFile: true,
			strict:    true,
			wantCode:  configvalidate.ExitOK,
		},
		{
			name:        "malformed TOML",
			body:        `[[hooks.session_start`,
			writeFile:   true,
			strict:      false,
			wantCode:    configvalidate.ExitErrors,
			wantSubstrs: []string{"toml"},
		},
		{
			name: "strict rejects unknown event with did-you-mean",
			body: `
[[hooks.session_strat]]
[[hooks.session_strat.hooks]]
type = "command"
command = "echo"
`,
			writeFile:   true,
			strict:      true,
			wantCode:    configvalidate.ExitErrors,
			wantSubstrs: []string{`unknown hook event "session_strat"`, `did you mean "session_start"`},
		},
		{
			name: "strict rejects unknown hook type",
			body: `
[[hooks.session_start]]
[[hooks.session_start.hooks]]
type = "comand"
command = "echo"
`,
			writeFile:   true,
			strict:      true,
			wantCode:    configvalidate.ExitErrors,
			wantSubstrs: []string{`unknown type "comand"`, `did you mean "command"`},
		},
		{
			name: "strict rejects command hook missing command",
			body: `
[[hooks.session_start]]
[[hooks.session_start.hooks]]
type = "command"
`,
			writeFile:   true,
			strict:      true,
			wantCode:    configvalidate.ExitErrors,
			wantSubstrs: []string{"type=command requires command"},
		},
		{
			name: "strict rejects unknown key",
			body: `
not_a_real_key = "x"
[[hooks.session_start]]
[[hooks.session_start.hooks]]
type = "command"
command = "echo"
`,
			writeFile:   true,
			strict:      true,
			wantCode:    configvalidate.ExitErrors,
			wantSubstrs: []string{`unknown key "not_a_real_key"`},
		},
		{
			name:        "missing config file is usage error",
			writeFile:   false,
			wantCode:    configvalidate.ExitUsageError,
			wantSubstrs: []string{"does not exist"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("CODEX_HOME", dir)
			if tc.writeFile {
				if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(tc.body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var stderr bytes.Buffer
			err := runValidate(&stderr, tc.strict)
			gotCode := configvalidate.ExitOK
			var ex *claude.ExitError
			if errors.As(err, &ex) {
				gotCode = ex.Code
			} else if err != nil {
				t.Fatalf("unexpected non-ExitError: %v", err)
			}
			if gotCode != tc.wantCode {
				t.Errorf("exit code = %d, want %d; stderr=%q", gotCode, tc.wantCode, stderr.String())
			}
			for _, s := range tc.wantSubstrs {
				if !strings.Contains(stderr.String(), s) {
					t.Errorf("stderr missing %q; got %q", s, stderr.String())
				}
			}
		})
	}
}
