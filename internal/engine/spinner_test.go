package engine

import (
	"bytes"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// TestShowThinking_TotalDuration uses testing/synctest so the per-frame
// time.Sleep calls inside showThinking advance virtual time instead of
// real wall-clock. Total elapsed virtual time should match the requested
// duration; the rendered output should end with the "Thought for Ns"
// marker.
func TestShowThinking_TotalDuration(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var buf bytes.Buffer
		start := time.Now()
		showThinking(&buf, 3*time.Second)
		elapsed := time.Since(start)

		if elapsed != 3*time.Second {
			t.Errorf("elapsed virtual time = %s, want 3s", elapsed)
		}
		out := buf.String()
		if !strings.Contains(out, "Thought for") {
			t.Errorf("output missing 'Thought for' marker:\n%q", out)
		}
	})
}

// TestShowThinking_ShortDelaySkipsAnimation verifies the sub-200ms branch:
// total <= short threshold sleeps then emits the marker without spinner
// frames.
func TestShowThinking_ShortDelaySkipsAnimation(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var buf bytes.Buffer
		start := time.Now()
		showThinking(&buf, 50*time.Millisecond)
		elapsed := time.Since(start)

		if elapsed != 50*time.Millisecond {
			t.Errorf("elapsed virtual time = %s, want 50ms", elapsed)
		}
		out := buf.String()
		if !strings.Contains(out, "Thought for") {
			t.Errorf("output missing 'Thought for' marker:\n%q", out)
		}
		if strings.Contains(out, "Thinking…") {
			t.Errorf("short delay should skip the spinner frames; got: %q", out)
		}
	})
}

// TestShowThinking_ZeroIsNoop verifies total<=0 returns immediately with
// no output and no virtual time advance.
func TestShowThinking_ZeroIsNoop(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var buf bytes.Buffer
		start := time.Now()
		showThinking(&buf, 0)
		if time.Since(start) != 0 {
			t.Errorf("expected zero elapsed virtual time")
		}
		if buf.Len() != 0 {
			t.Errorf("expected no output; got %q", buf.String())
		}
	})
}
