// Package configvalidate provides shared primitives for the `validate`
// subcommands across vendors: an error collector, line/column resolution
// from a JSON decoder offset, exit-code conventions, and a tiny
// Levenshtein helper for closest-match suggestions on unknown enum
// values (event names, hook types, etc.).
//
// Per-vendor rule sets live in cmd/<vendor>/validate.go.
package configvalidate

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Exit codes per the validate contract:
//
//   - 0: all files passed.
//   - 1: at least one validation error reported.
//   - 2: usage error (no flags supplied, file not found, etc.).
const (
	ExitOK         = 0
	ExitErrors     = 1
	ExitUsageError = 2
)

// ErrUsage is the sentinel for misuse (no inputs, bad flag values). The
// caller exits with ExitUsageError and prints usage. Validation rule
// failures do not use this — they're aggregated into a Collector.
var ErrUsage = errors.New("validate: usage error")

// Issue is one problem found in a config file. Line/Col are 1-indexed
// and may both be zero when the decoder doesn't surface position info
// (TOML's BurntSushi decoder, primarily). Format: `<Path>:<Line>: <Msg>`
// when Line > 0, otherwise `<Path>: <Msg>`.
type Issue struct {
	Path string
	Line int
	Col  int
	Msg  string
}

// Format renders Issue per the documented `<path>[:line]: <message>`
// shape. Stable across runs so test fixtures can string-compare.
func (i Issue) Format() string {
	if i.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", i.Path, i.Line, i.Msg)
	}
	return fmt.Sprintf("%s: %s", i.Path, i.Msg)
}

// Collector accumulates Issues from multiple files in a single run.
// Per the issue spec, the validate subcommand prints every issue rather
// than first-fail; the caller drives ordering by appending in
// vendor-specific traversal order. Print sorts by (path, line, col, msg)
// so output is deterministic.
type Collector struct {
	issues []Issue
}

// Add records an issue. Empty Path / Msg is allowed (caller wraps).
func (c *Collector) Add(i Issue) {
	c.issues = append(c.issues, i)
}

// Addf is the printf shorthand for Add — convenient when wiring rules
// that build the message inline.
func (c *Collector) Addf(path string, line int, format string, args ...any) {
	c.Add(Issue{Path: path, Line: line, Msg: fmt.Sprintf(format, args...)})
}

// Len reports how many issues have been accumulated.
func (c *Collector) Len() int { return len(c.issues) }

// Print writes every issue to w, sorted deterministically. Returns the
// number of issues printed.
func (c *Collector) Print(w io.Writer) int {
	sorted := make([]Issue, len(c.issues))
	copy(sorted, c.issues)
	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		switch {
		case a.Path != b.Path:
			return a.Path < b.Path
		case a.Line != b.Line:
			return a.Line < b.Line
		case a.Col != b.Col:
			return a.Col < b.Col
		default:
			return a.Msg < b.Msg
		}
	})
	for _, i := range sorted {
		fmt.Fprintln(w, i.Format())
	}
	return len(sorted)
}

// ExitCode maps the collected issues + caller error to the documented
// exit codes. usageErr is non-nil when the invocation failed before
// validation could start (e.g. ErrUsage, file-not-found).
func ExitCode(c *Collector, usageErr error) int {
	if usageErr != nil {
		return ExitUsageError
	}
	if c.Len() > 0 {
		return ExitErrors
	}
	return ExitOK
}

// LineColAt converts a byte offset within data (typically the Offset
// field on a json.SyntaxError or json.UnmarshalTypeError) into a
// 1-indexed (line, col). Treats \n as a line break; tabs count as one
// column. Returns (1, 1) when the offset is at the start.
func LineColAt(data []byte, offset int64) (int, int) {
	if offset < 0 {
		return 1, 1
	}
	if int(offset) > len(data) {
		offset = int64(len(data))
	}
	line, col := 1, 1
	for i := int64(0); i < offset; i++ {
		if data[i] == '\n' {
			line++
			col = 1
			continue
		}
		col++
	}
	return line, col
}

// Suggest returns a short hint for an unknown enum value. When the
// closest valid value is within editDistanceThreshold (max(2, len/3)),
// the hint is `did you mean "<closest>"?`; otherwise it falls back to
// listing all valid values comma-separated. valid must be non-empty.
func Suggest(input string, valid []string) string {
	if len(valid) == 0 {
		return ""
	}
	best := valid[0]
	bestDist := levenshtein(input, valid[0])
	for _, v := range valid[1:] {
		if d := levenshtein(input, v); d < bestDist {
			best = v
			bestDist = d
		}
	}
	threshold := max(2, len(input)/3)
	if bestDist <= threshold {
		return fmt.Sprintf("did you mean %q?", best)
	}
	sorted := make([]string, len(valid))
	copy(sorted, valid)
	sort.Strings(sorted)
	return "valid: " + strings.Join(sorted, ", ")
}

// levenshtein returns the edit distance between a and b using the
// standard two-row dynamic programming approach. Hand-rolled rather
// than depending on a third-party package; this is the only consumer
// in the repo.
func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}
