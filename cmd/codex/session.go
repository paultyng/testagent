package codex

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// newSessionID generates a UUID-v4-shaped session identifier. Codex's
// session IDs in the wild are also UUID-shaped; the value is opaque to
// orchestrators but the format keeps fixtures readable.
//
// Mirrors cmd/claude/settings.go's newSessionID — the helpers were left
// per-vendor rather than shared so each subcommand owns the format
// independently if upstream codex ever diverges from UUID.
func newSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]))
}
