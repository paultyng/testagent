package cursor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// loadAgentsMD reads `AGENTS.md` from cwd and returns a one-line summary
// for the status line. Cursor reads AGENTS.md the same way codex does; we
// surface the file's presence to orchestrators without interpreting its
// content (testagent has no model). Returns the empty string when the file
// is absent — that's the common case.
func loadAgentsMD(cwd string) (string, error) {
	path := filepath.Join(cwd, "AGENTS.md")
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("stat %s: %w", path, err)
	}
	return fmt.Sprintf("AGENTS.md: %d bytes", info.Size()), nil
}
