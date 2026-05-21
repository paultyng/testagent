package cursor

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

func TestRunValidate_Cursor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		mcpBody     string
		hooksBody   string
		writeMCP    bool
		writeHooks  bool
		strict      bool
		wantCode    int
		wantSubstrs []string
	}{
		{
			name: "valid lax",
			hooksBody: `{
  "version": 1,
  "hooks": {
    "beforeShellExecution": [
      {"command": "echo hi", "type": "command"}
    ]
  }
}`,
			mcpBody: `{
  "mcpServers": {
    "myserver": {"type": "http", "url": "http://localhost:3000"}
  }
}`,
			writeMCP:   true,
			writeHooks: true,
			strict:     false,
			wantCode:   configvalidate.ExitOK,
		},
		{
			name: "valid strict",
			hooksBody: `{
  "version": 1,
  "hooks": {
    "preToolUse": [
      {"command": "echo hi", "type": "command"}
    ]
  }
}`,
			mcpBody: `{
  "mcpServers": {
    "myserver": {"type": "http", "url": "http://localhost:3000"}
  }
}`,
			writeMCP:   true,
			writeHooks: true,
			strict:     true,
			wantCode:   configvalidate.ExitOK,
		},
		{
			name:        "malformed JSON hooks.json",
			hooksBody:   `{"version": 1, "hooks": {not valid json`,
			writeHooks:  true,
			strict:      false,
			wantCode:    configvalidate.ExitErrors,
			wantSubstrs: []string{"parsing cursor hooks.json"},
		},
		{
			name: "strict rejects unknown event with did-you-mean",
			hooksBody: `{
  "version": 1,
  "hooks": {
    "beforeShellExecuton": [
      {"command": "echo hi", "type": "command"}
    ]
  }
}`,
			writeHooks:  true,
			strict:      true,
			wantCode:    configvalidate.ExitErrors,
			wantSubstrs: []string{`unknown hook event "beforeShellExecuton"`, `did you mean "beforeShellExecution"`},
		},
		{
			name: "strict rejects unknown hook type with did-you-mean",
			hooksBody: `{
  "version": 1,
  "hooks": {
    "beforeShellExecution": [
      {"command": "echo hi", "type": "promppt"}
    ]
  }
}`,
			writeHooks:  true,
			strict:      true,
			wantCode:    configvalidate.ExitErrors,
			wantSubstrs: []string{`unknown type "promppt"`, `did you mean "prompt"`},
		},
		{
			name: "strict rejects zero entries under an event",
			hooksBody: `{
  "version": 1,
  "hooks": {
    "beforeShellExecution": []
  }
}`,
			writeHooks:  true,
			strict:      true,
			wantCode:    configvalidate.ExitErrors,
			wantSubstrs: []string{`hooks.beforeShellExecution has zero entries`},
		},
		{
			name: "strict rejects command-type entry with empty command",
			hooksBody: `{
  "version": 1,
  "hooks": {
    "beforeShellExecution": [
      {"type": "command"}
    ]
  }
}`,
			writeHooks:  true,
			strict:      true,
			wantCode:    configvalidate.ExitErrors,
			wantSubstrs: []string{`hooks.beforeShellExecution[0] type=command requires command`},
		},
		{
			name: "strict accepts prompt-type entry without command",
			hooksBody: `{
  "version": 1,
  "hooks": {
    "beforeShellExecution": [
      {"type": "prompt"}
    ]
  }
}`,
			writeHooks: true,
			strict:     true,
			wantCode:   configvalidate.ExitOK,
		},
		{
			name:     "missing config files OK",
			strict:   false,
			wantCode: configvalidate.ExitOK,
		},
		{
			name:     "missing config files OK strict",
			strict:   true,
			wantCode: configvalidate.ExitOK,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			cursorDir := filepath.Join(dir, ".cursor")
			if err := os.MkdirAll(cursorDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if tc.writeHooks {
				if err := os.WriteFile(filepath.Join(cursorDir, "hooks.json"), []byte(tc.hooksBody), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.writeMCP {
				if err := os.WriteFile(filepath.Join(cursorDir, "mcp.json"), []byte(tc.mcpBody), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			var stderr bytes.Buffer
			err := runValidate(&stderr, dir, tc.strict)
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

func TestRunValidate_CursorUpstreamExamples(t *testing.T) {
	t.Parallel()
	fixtures, err := filepath.Glob("../../testdata/upstream-examples/cursor/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no fixtures found in testdata/upstream-examples/cursor/")
	}
	for _, path := range fixtures {
		path := path
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			cursorDir := filepath.Join(dir, ".cursor")
			if err := os.MkdirAll(cursorDir, 0o700); err != nil {
				t.Fatal(err)
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatalf("reading fixture %s: %v", path, readErr)
			}
			var dest string
			switch {
			case strings.HasPrefix(name, "hooks-"):
				dest = filepath.Join(cursorDir, "hooks.json")
			case strings.HasPrefix(name, "mcp-"):
				dest = filepath.Join(cursorDir, "mcp.json")
			default:
				t.Fatalf("fixture %s: cannot determine target file (no hooks- or mcp- prefix)", name)
			}
			if err := os.WriteFile(dest, data, 0o600); err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			runErr := runValidate(&stderr, dir, true)
			var ex *claude.ExitError
			if errors.As(runErr, &ex) {
				t.Errorf("fixture %s: exit code = %d, want ExitOK; stderr=%q", path, ex.Code, stderr.String())
			} else if runErr != nil {
				t.Errorf("fixture %s: unexpected error: %v; stderr=%q", path, runErr, stderr.String())
			}
			if stderr.Len() != 0 {
				t.Errorf("fixture %s: expected empty stderr, got %q", path, stderr.String())
			}
		})
	}
}
