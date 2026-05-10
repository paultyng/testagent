package codex

import (
	"errors"
	"fmt"
	"os"
)

// loadAgentsMD reads `AGENTS.md` from cwd and returns a one-line summary
// for the status line. Returns an empty string if the file doesn't exist
// (the common case). Errors other than "not exists" are returned so the
// user sees them at startup.
//
// Real codex feeds AGENTS.md content to the model. testagent has no
// model; the file's presence is surfaced for orchestrator parity but its
// content is not interpreted. A larger integration is tracked
// post-MVP.
func loadAgentsMD(cwd string) (string, error) {
	path := cwd + "/AGENTS.md"
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("stat %s: %w", path, err)
	}
	return fmt.Sprintf("AGENTS.md: %d bytes", info.Size()), nil
}
