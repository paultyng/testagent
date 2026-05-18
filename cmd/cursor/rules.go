package cursor

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// RuleFile is the parsed shape of one .cursor/rules/*.mdc entry.
// Activation mode is derived per cursor.com/docs/rules:
//
//   - AlwaysApply=true            → "always" (loaded into every chat)
//   - Globs non-empty             → "glob"   (auto-attached when a match is in context)
//   - Description non-empty       → "intelligent" (agent decides)
//   - (none of the above)         → "manual" (only via @rule-name mention)
//
// Mode priority is the order listed above; alwaysApply wins over a glob rule
// that also sets it, matching cursor's described activation precedence.
type RuleFile struct {
	Path        string
	Description string
	AlwaysApply bool
	Globs       string
}

// Mode returns the activation mode for r per the docs' four-category model.
func (r RuleFile) Mode() string {
	switch {
	case r.AlwaysApply:
		return "always"
	case r.Globs != "":
		return "glob"
	case r.Description != "":
		return "intelligent"
	default:
		return "manual"
	}
}

// loadRules walks workspace/.cursor/rules/ for *.mdc files, parses each
// frontmatter block, and returns the resulting RuleFile slice sorted by
// path. Returns (nil, nil) when the directory doesn't exist.
//
// Frontmatter parsing is intentionally narrow: only the three documented
// fields (description, alwaysApply, globs) are read. Unknown keys are
// ignored; missing frontmatter is treated as an empty RuleFile (manual
// activation). Malformed files surface as errors so the user catches
// schema drift at startup.
func loadRules(workspace string) ([]RuleFile, error) {
	dir := filepath.Join(workspace, ".cursor", "rules")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	var out []RuleFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) != ".mdc" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		r, err := parseRuleFile(path)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// parseRuleFile reads one .mdc file and extracts the optional YAML
// frontmatter (delimited by `---` lines at the top). Body content is
// discarded — testagent surfaces the rule's existence + activation mode,
// not the rule body (which is the model's concern).
func parseRuleFile(path string) (RuleFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return RuleFile{}, fmt.Errorf("opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	r := RuleFile{Path: path}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)

	// Frontmatter starts with `---` on the first non-blank line. If the
	// first non-blank line isn't `---`, the file has no frontmatter and
	// we treat all fields as zero.
	inFrontmatter := false
	started := false
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if !started {
			if trimmed == "" {
				continue
			}
			if trimmed != "---" {
				return r, nil
			}
			started = true
			inFrontmatter = true
			continue
		}
		if inFrontmatter && trimmed == "---" {
			return r, nil
		}
		if err := applyFrontmatterLine(&r, trimmed); err != nil {
			return RuleFile{}, fmt.Errorf("parsing %s: %w", path, err)
		}
	}
	if err := sc.Err(); err != nil {
		return RuleFile{}, fmt.Errorf("reading %s: %w", path, err)
	}
	// Reached EOF without a closing `---`. Cursor accepts this loosely;
	// surface what we parsed.
	return r, nil
}

// applyFrontmatterLine mutates r based on a single `key: value` line.
// Lines that don't match a known key are silently skipped to keep the
// parser tolerant of new cursor frontmatter fields. Bool values must
// be parseable by strconv.ParseBool.
func applyFrontmatterLine(r *RuleFile, line string) error {
	if line == "" || strings.HasPrefix(line, "#") {
		return nil
	}
	colon := strings.Index(line, ":")
	if colon < 0 {
		return nil
	}
	key := strings.TrimSpace(line[:colon])
	raw := strings.TrimSpace(line[colon+1:])
	val := unquoteFrontmatterValue(raw)

	switch key {
	case "description":
		r.Description = val
	case "alwaysApply":
		b, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("alwaysApply = %q: %w", val, err)
		}
		r.AlwaysApply = b
	case "globs":
		r.Globs = val
	}
	return nil
}

// unquoteFrontmatterValue strips matching surrounding double- or single-
// quotes from raw. Returns raw unchanged when there are no surrounding
// quotes. Multi-line strings, YAML anchors, and block scalars are not
// supported — the documented frontmatter shape doesn't use them.
func unquoteFrontmatterValue(raw string) string {
	if len(raw) >= 2 {
		first := raw[0]
		last := raw[len(raw)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return raw[1 : len(raw)-1]
		}
	}
	return raw
}

// rulesStatusLine returns a one-line summary of the loaded rule set for
// the status line. Empty when rules is empty.
//
// Format: `rules: <count> (<a> always, <g> glob, <i> intelligent, <m> manual)`.
// Categories with zero rules are omitted.
func rulesStatusLine(rules []RuleFile) string {
	if len(rules) == 0 {
		return ""
	}
	var n struct{ always, glob, intelligent, manual int }
	for _, r := range rules {
		switch r.Mode() {
		case "always":
			n.always++
		case "glob":
			n.glob++
		case "intelligent":
			n.intelligent++
		case "manual":
			n.manual++
		}
	}
	var parts []string
	if n.always > 0 {
		parts = append(parts, fmt.Sprintf("%d always", n.always))
	}
	if n.glob > 0 {
		parts = append(parts, fmt.Sprintf("%d glob", n.glob))
	}
	if n.intelligent > 0 {
		parts = append(parts, fmt.Sprintf("%d intelligent", n.intelligent))
	}
	if n.manual > 0 {
		parts = append(parts, fmt.Sprintf("%d manual", n.manual))
	}
	return fmt.Sprintf("rules: %d (%s)", len(rules), strings.Join(parts, ", "))
}
