package engine

import (
	"bytes"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// TestStreamEcho_Pacing uses testing/synctest so the time.Sleep between
// tokens advances virtual time rather than real wall-clock. With three
// tokens and a 100ms per-token delay, total elapsed virtual time should
// be 200ms (sleeps fire BETWEEN tokens, not after the last one).
func TestStreamEcho_Pacing(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var buf bytes.Buffer
		start := time.Now()
		streamEcho(&buf, "agent", "alpha beta gamma", 100*time.Millisecond)
		elapsed := time.Since(start)

		if elapsed != 200*time.Millisecond {
			t.Errorf("elapsed virtual time = %s, want 200ms (3 tokens, 2 inter-token sleeps)", elapsed)
		}
		out := buf.String()
		for _, want := range []string{"[agent]", "alpha", "beta", "gamma"} {
			if !strings.Contains(out, want) {
				t.Errorf("output missing %q:\n%q", want, out)
			}
		}
		if !strings.HasSuffix(out, "\n") {
			t.Errorf("output should end with newline; got %q", out)
		}
	})
}

// TestStreamEcho_ZeroDelaySkipsSleeps confirms perToken<=0 emits all
// tokens without advancing virtual time.
func TestStreamEcho_ZeroDelaySkipsSleeps(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var buf bytes.Buffer
		start := time.Now()
		streamEcho(&buf, "agent", "alpha beta gamma", 0)
		if time.Since(start) != 0 {
			t.Errorf("expected zero elapsed virtual time with perToken=0")
		}
		if !strings.Contains(buf.String(), "alpha beta gamma") {
			t.Errorf("output missing message body:\n%q", buf.String())
		}
	})
}

// TestStreamEcho_EmptyMessage verifies an empty message body produces the
// header + newline only, with no per-token sleeps.
func TestStreamEcho_EmptyMessage(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var buf bytes.Buffer
		start := time.Now()
		streamEcho(&buf, "agent", "", 100*time.Millisecond)
		if time.Since(start) != 0 {
			t.Errorf("expected zero elapsed virtual time for empty body")
		}
		out := buf.String()
		if !strings.Contains(out, "[agent]") {
			t.Errorf("output missing header for empty body:\n%q", out)
		}
	})
}
