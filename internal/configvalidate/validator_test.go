package configvalidate

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestIssueFormat(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   Issue
		want string
	}{
		{"with line", Issue{Path: "a.json", Line: 5, Msg: "boom"}, "a.json:5: boom"},
		{"no line", Issue{Path: "a.json", Msg: "boom"}, "a.json: boom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.in.Format(); got != tc.want {
				t.Errorf("Format = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCollectorPrintSorted(t *testing.T) {
	t.Parallel()
	var c Collector
	c.Add(Issue{Path: "b.json", Line: 1, Msg: "z"})
	c.Add(Issue{Path: "a.json", Line: 2, Msg: "y"})
	c.Add(Issue{Path: "a.json", Line: 1, Msg: "x"})

	var buf bytes.Buffer
	n := c.Print(&buf)
	if n != 3 {
		t.Errorf("Print returned %d, want 3", n)
	}
	got := buf.String()
	want := "a.json:1: x\na.json:2: y\nb.json:1: z\n"
	if got != want {
		t.Errorf("Print output =\n%s\nwant\n%s", got, want)
	}
}

func TestExitCode(t *testing.T) {
	t.Parallel()
	var clean Collector
	if got := ExitCode(&clean, nil); got != ExitOK {
		t.Errorf("clean ExitCode = %d, want %d", got, ExitOK)
	}
	var dirty Collector
	dirty.Add(Issue{Path: "x", Msg: "boom"})
	if got := ExitCode(&dirty, nil); got != ExitErrors {
		t.Errorf("dirty ExitCode = %d, want %d", got, ExitErrors)
	}
	if got := ExitCode(&clean, errors.New("usage")); got != ExitUsageError {
		t.Errorf("usage ExitCode = %d, want %d", got, ExitUsageError)
	}
}

func TestLineColAt(t *testing.T) {
	t.Parallel()
	data := []byte("{\n  \"foo\": 1,\n  \"bar\": 2\n}")
	cases := []struct {
		offset    int64
		wantLine  int
		wantCol   int
	}{
		{0, 1, 1},
		{1, 1, 2},
		{2, 2, 1},  // after first \n
		{10, 2, 9}, // mid line 2
		{-1, 1, 1}, // negative clamps
		{99999, 4, 2},
	}
	for _, tc := range cases {
		gotL, gotC := LineColAt(data, tc.offset)
		if gotL != tc.wantLine || gotC != tc.wantCol {
			t.Errorf("LineColAt(%d) = (%d, %d), want (%d, %d)", tc.offset, gotL, gotC, tc.wantLine, tc.wantCol)
		}
	}
}

func TestSuggest(t *testing.T) {
	t.Parallel()
	valid := []string{"PreToolUse", "PostToolUse", "SessionStart", "SessionEnd"}

	// Close typo → "did you mean" suggestion.
	if got := Suggest("PreToolUze", valid); !strings.Contains(got, `did you mean "PreToolUse"`) {
		t.Errorf("close-typo Suggest = %q, want did-you-mean PreToolUse", got)
	}

	// Far-off input → falls back to the listed-valid form.
	got := Suggest("totally-not-a-thing", valid)
	if !strings.HasPrefix(got, "valid: ") {
		t.Errorf("far-off Suggest = %q, want valid: prefix", got)
	}
	// Listed values sorted alphabetically.
	if !strings.Contains(got, "PostToolUse, PreToolUse, SessionEnd, SessionStart") {
		t.Errorf("Suggest valid list = %q, want sorted output", got)
	}

	// Empty valid set returns empty string.
	if got := Suggest("anything", nil); got != "" {
		t.Errorf("Suggest(_, nil) = %q, want \"\"", got)
	}
}

func TestLevenshtein(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"a", "", 1},
		{"", "abc", 3},
		{"kitten", "sitting", 3},
		{"flaw", "lawn", 2},
		{"PreToolUse", "PreToolUse", 0},
		{"PreToolUse", "PreToolUze", 1},
	}
	for _, tc := range cases {
		if got := levenshtein(tc.a, tc.b); got != tc.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
