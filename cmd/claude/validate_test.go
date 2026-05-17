package claude

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paultyng/testagent/internal/configvalidate"
)

func TestRunValidate_UsageErrorWhenNoFlags(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	err := runValidate(&stderr, "", "", false)
	var ex *ExitError
	if !errors.As(err, &ex) || ex.Code != configvalidate.ExitUsageError {
		t.Fatalf("err = %v, want ExitError{Code=ExitUsageError}", err)
	}
	if !strings.Contains(stderr.String(), "supply at least one") {
		t.Errorf("stderr = %q, want usage hint", stderr.String())
	}
}

func TestRunValidate_Settings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		body        string
		strict      bool
		wantCode    int
		wantSubstrs []string
	}{
		{
			name:     "valid lax",
			body:     `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"http","url":"http://example/h"}]}]}}`,
			strict:   false,
			wantCode: configvalidate.ExitOK,
		},
		{
			name:     "valid strict",
			body:     `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"http","url":"http://example/h"}]}]}}`,
			strict:   true,
			wantCode: configvalidate.ExitOK,
		},
		{
			name:        "malformed JSON",
			body:        `{"hooks":`,
			strict:      false,
			wantCode:    configvalidate.ExitErrors,
			wantSubstrs: []string{":1:", "unexpected end"},
		},
		{
			name:        "strict rejects unknown top-level field",
			body:        `{"thingies":{},"hooks":{}}`,
			strict:      true,
			wantCode:    configvalidate.ExitErrors,
			wantSubstrs: []string{"thingies"},
		},
		{
			name:        "strict rejects unknown event with did-you-mean",
			body:        `{"hooks":{"PreToolUze":[{"matcher":"","hooks":[{"type":"http","url":"u"}]}]}}`,
			strict:      true,
			wantCode:    configvalidate.ExitErrors,
			wantSubstrs: []string{`unknown hook event "PreToolUze"`, `did you mean "PreToolUse"`},
		},
		{
			// Events Claude Code documents but testagent doesn't fire
			// (subagent / task / elicitation lifecycle) must still pass
			// --strict: the validate subcommand's contract is schema
			// conformance, not runtime support.
			name:     "strict accepts documented-but-unmodeled events",
			body:     `{"hooks":{"SubagentStart":[{"matcher":"","hooks":[{"type":"http","url":"u"}]}],"ElicitationResult":[{"matcher":"","hooks":[{"type":"http","url":"u"}]}]}}`,
			strict:   true,
			wantCode: configvalidate.ExitOK,
		},
		{
			name:        "strict rejects unknown event with far-off name (lists valid)",
			body:        `{"hooks":{"NoSuchEvent":[{"matcher":"","hooks":[{"type":"http","url":"u"}]}]}}`,
			strict:      true,
			wantCode:    configvalidate.ExitErrors,
			wantSubstrs: []string{`unknown hook event "NoSuchEvent"`, "valid:", "PreToolUse"},
		},
		{
			name:        "strict rejects unknown hook type",
			body:        `{"hooks":{"PreToolUse":[{"matcher":"","hooks":[{"type":"htttp","url":"u"}]}]}}`,
			strict:      true,
			wantCode:    configvalidate.ExitErrors,
			wantSubstrs: []string{`unknown type "htttp"`, `did you mean "http"`},
		},
		{
			name:        "strict rejects http hook missing url",
			body:        `{"hooks":{"PreToolUse":[{"matcher":"","hooks":[{"type":"http"}]}]}}`,
			strict:      true,
			wantCode:    configvalidate.ExitErrors,
			wantSubstrs: []string{"type=http requires url"},
		},
		{
			name:        "strict rejects command hook missing command",
			body:        `{"hooks":{"PreToolUse":[{"matcher":"","hooks":[{"type":"command"}]}]}}`,
			strict:      true,
			wantCode:    configvalidate.ExitErrors,
			wantSubstrs: []string{"type=command requires command"},
		},
		{
			name:        "strict rejects matcher with zero hooks",
			body:        `{"hooks":{"PreToolUse":[{"matcher":"","hooks":[]}]}}`,
			strict:      true,
			wantCode:    configvalidate.ExitErrors,
			wantSubstrs: []string{"has zero hooks"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			settings := filepath.Join(dir, "settings.json")
			if err := os.WriteFile(settings, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			err := runValidate(&stderr, settings, "", tc.strict)
			gotCode := configvalidate.ExitOK
			var ex *ExitError
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

func TestRunValidate_MCPConfigMalformed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bad := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(bad, []byte(`{"mcpServers":`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	err := runValidate(&stderr, "", bad, false)
	var ex *ExitError
	if !errors.As(err, &ex) || ex.Code != configvalidate.ExitErrors {
		t.Fatalf("err = %v, want ExitError{Code=ExitErrors}", err)
	}
	if !strings.Contains(stderr.String(), ":1:") {
		t.Errorf("stderr missing line info; got %q", stderr.String())
	}
}

func TestRunValidate_FileNotFound(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	err := runValidate(&stderr, "/nonexistent/path/settings.json", "", false)
	var ex *ExitError
	if !errors.As(err, &ex) || ex.Code != configvalidate.ExitUsageError {
		t.Fatalf("err = %v, want ExitError{Code=ExitUsageError}", err)
	}
}
