package cursor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadRules_FullFixture covers the four documented activation modes
// in a single workspace: always / glob / intelligent / manual.
func TestLoadRules_FullFixture(t *testing.T) {
	t.Parallel()

	ws := testdataPath(t, "rules-good")
	rules, err := loadRules(ws)
	if err != nil {
		t.Fatalf("loadRules: %v", err)
	}
	if got, want := len(rules), 4; got != want {
		t.Fatalf("len(rules) = %d, want %d", got, want)
	}

	byName := make(map[string]RuleFile, len(rules))
	for _, r := range rules {
		byName[filepath.Base(r.Path)] = r
	}

	cases := []struct {
		file     string
		wantMode string
		wantDesc string
		wantGlob string
		wantAlw  bool
	}{
		{"always.mdc", "always", "Hard-coded constants live in const blocks", "", true},
		{"glob-only.mdc", "glob", "", "src/**/*.ts", false},
		{"intelligent.mdc", "intelligent", "RPC service conventions", "", false},
		{"manual.mdc", "manual", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			r, ok := byName[tc.file]
			if !ok {
				t.Fatalf("missing %q", tc.file)
			}
			if r.Mode() != tc.wantMode {
				t.Errorf("Mode = %q, want %q", r.Mode(), tc.wantMode)
			}
			if r.Description != tc.wantDesc {
				t.Errorf("Description = %q, want %q", r.Description, tc.wantDesc)
			}
			if r.Globs != tc.wantGlob {
				t.Errorf("Globs = %q, want %q", r.Globs, tc.wantGlob)
			}
			if r.AlwaysApply != tc.wantAlw {
				t.Errorf("AlwaysApply = %v, want %v", r.AlwaysApply, tc.wantAlw)
			}
		})
	}
}

func TestLoadRules_NoDir(t *testing.T) {
	t.Parallel()

	rules, err := loadRules(t.TempDir())
	if err != nil {
		t.Fatalf("loadRules: %v", err)
	}
	if rules != nil {
		t.Errorf("rules = %v, want nil for missing .cursor/rules/", rules)
	}
}

func TestLoadRules_BadAlwaysApply(t *testing.T) {
	t.Parallel()

	ws := testdataPath(t, "rules-bad")
	_, err := loadRules(ws)
	if err == nil {
		t.Fatal("want error from invalid alwaysApply, got nil")
	}
	if !strings.Contains(err.Error(), "alwaysApply") {
		t.Errorf("error %q does not mention alwaysApply", err.Error())
	}
}

func TestLoadRules_IgnoresNonMDC(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	dir := filepath.Join(ws, ".cursor", "rules")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// One valid .mdc + one stray file with the wrong extension.
	if err := os.WriteFile(filepath.Join(dir, "real.mdc"), []byte("---\nalwaysApply: true\n---\n"), 0o644); err != nil {
		t.Fatalf("writing real.mdc: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# notes"), 0o644); err != nil {
		t.Fatalf("writing README.md: %v", err)
	}

	rules, err := loadRules(ws)
	if err != nil {
		t.Fatalf("loadRules: %v", err)
	}
	if got, want := len(rules), 1; got != want {
		t.Fatalf("len(rules) = %d, want %d (non-mdc files must be skipped)", got, want)
	}
}

func TestRulesStatusLine(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		rules []RuleFile
		want  string
	}{
		{
			name:  "empty",
			rules: nil,
			want:  "",
		},
		{
			name: "one of each",
			rules: []RuleFile{
				{AlwaysApply: true},
				{Globs: "*.ts"},
				{Description: "smart"},
				{},
			},
			want: "rules: 4 (1 always, 1 glob, 1 intelligent, 1 manual)",
		},
		{
			name: "only always",
			rules: []RuleFile{
				{AlwaysApply: true},
				{AlwaysApply: true},
			},
			want: "rules: 2 (2 always)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := rulesStatusLine(tc.rules)
			if got != tc.want {
				t.Errorf("rulesStatusLine = %q, want %q", got, tc.want)
			}
		})
	}
}
