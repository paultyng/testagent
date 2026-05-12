// Non-interactive `codex exec [prompt]` mode. Reads a single prompt from
// positional args (or stdin if none), produces an echo response, and emits
// it in the configured --output-format: text, json, or stream-json.
//
// Output shapes mirror upstream codex-rs/exec's JSONL ThreadEvent schema
// (see codex-rs/exec/src/exec_events.rs at rust-v0.130.0) so consumers
// parsing stream-json from the real codex binary can parse testagent's
// output the same way. Fields that require a real model
// (reasoning_summary, tool_calls, cached tokens) are emitted as zero /
// empty per COMPATIBILITY.md's honest-unmodeled-fields rule.
//
// Lifecycle: session_start → read prompt → user_prompt_submit → produce
// echo → emit per --output-format → stop. Codex has no session_end event;
// final-process teardown happens in Runner.Close.

package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/paultyng/testagent/internal/codexhooks"
	"github.com/paultyng/testagent/internal/rootflags"
)

// execFlags bundles `codex exec`-specific flag values. Parent persistent
// flags (cf *flags) carry the global codex knobs (cd, model, sandbox, …);
// this struct only holds knobs scoped to `exec`.
type execFlags struct {
	OutputFormat string // "text" | "json" | "stream-json"
}

// execOptions bundles inputs runExec needs from the cobra RunE closure.
// Pure-input shape (no cobra coupling) so the function is straightforward
// to drive from tests.
type execOptions struct {
	name         string
	sessionID    string
	cwd          string
	model        string
	outputFormat string
	positional   []string
	hooks        *codexhooks.Runner
}

// newExecCommand returns the `codex exec [prompt]` subcommand. Non-
// interactive one-shot — codex's analog of `claude --print`.
func newExecCommand(rf *rootflags.Flags, cf *flags) *cobra.Command {
	ef := &execFlags{}
	cmd := &cobra.Command{
		Use:          "exec [prompt]",
		Short:        "Run Codex non-interactively (one-shot)",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cf.CD != "" {
				if err := os.Chdir(cf.CD); err != nil {
					return fmt.Errorf("--cd %s: %w", cf.CD, err)
				}
			}
			switch ef.OutputFormat {
			case "", "text", "json", "stream-json":
			default:
				return fmt.Errorf("--output-format must be text|json|stream-json, got %q", ef.OutputFormat)
			}

			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			cwd, _ := os.Getwd()
			sid := newSessionID()
			transcriptPath := fmt.Sprintf("/tmp/testagent-transcript-%s.jsonl", sid)
			const permissionMode = "default"

			var debugW io.Writer
			if rf.Verbose {
				debugW = os.Stderr
			}
			runner := codexhooks.NewRunner(matchersFromConfig(cfg), sid, cwd, transcriptPath, permissionMode, debugW)
			defer func() { _ = runner.Close(ctxOrBackground(cmd)) }()

			format := ef.OutputFormat
			if format == "" {
				format = "text"
			}
			return runExec(ctxOrBackground(cmd), execOptions{
				name:         "session",
				sessionID:    sid,
				cwd:          cwd,
				model:        cf.Model,
				outputFormat: format,
				positional:   args,
				hooks:        runner,
			}, os.Stdin, os.Stdout)
		},
	}
	cmd.Flags().StringVar(&ef.OutputFormat, "output-format", "", "output format: text|json|stream-json (default text)")
	return cmd
}

// runExec executes one non-interactive turn. Returns nil on success or an
// error suitable for cobra's RunE return (errors.New for missing prompt,
// wrapped for I/O failures). Hook errors are logged to stderr and do not
// affect the exit code, matching cmd/claude/print.go.
//
// SessionStart fires only after the prompt is resolved so an early
// "missing prompt" exit doesn't leave orchestrators with a dangling
// session_start that never pairs with a Stop.
func runExec(ctx context.Context, opt execOptions, stdin io.Reader, stdout io.Writer) error {
	prompt := strings.TrimSpace(strings.Join(opt.positional, " "))
	if prompt == "" {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return fmt.Errorf("read prompt from stdin: %w", err)
		}
		prompt = strings.TrimSpace(string(b))
	}
	if prompt == "" {
		return errors.New("codex exec requires a prompt (positional arg or stdin)")
	}

	if err := opt.hooks.OnSessionStart(ctx, "startup"); err != nil {
		fmt.Fprintf(os.Stderr, "testagent: hook OnSessionStart: %v\n", err)
	}
	if err := opt.hooks.OnPrompt(ctx, prompt, opt.name); err != nil {
		fmt.Fprintf(os.Stderr, "testagent: hook OnPrompt: %v\n", err)
	}

	response := fmt.Sprintf("[%s] %s", opt.name, prompt)

	switch opt.outputFormat {
	case "json":
		emitExecJSON(stdout, opt.sessionID, prompt, response)
	case "stream-json":
		emitExecStreamJSON(stdout, opt.sessionID, prompt, response)
	default: // "text" or unset
		fmt.Fprintln(stdout, response)
	}

	if err := opt.hooks.OnStop(ctx, response, false); err != nil {
		fmt.Fprintf(os.Stderr, "testagent: hook OnStop: %v\n", err)
	}
	return nil
}

// emitExecJSON writes a single JSON summary object for the turn. This
// shape is testagent-specific — upstream codex exec has no equivalent
// (it only ships `--json` for JSONL output). Provided as a convenience
// for orchestrators that want one parse rather than a JSONL stream;
// shape mirrors stream-json's terminal `turn.completed` plus the agent
// message text and thread id.
func emitExecJSON(w io.Writer, threadID, prompt, finalMessage string) {
	out := map[string]any{
		"type":          "turn.completed",
		"thread_id":     threadID,
		"final_message": finalMessage,
		"usage":         execUsage(prompt, finalMessage),
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(out)
}

// emitExecStreamJSON writes the upstream codex-rs/exec ThreadEvent
// sequence as JSONL: thread.started → turn.started → item.started
// (agent_message in_progress) → item.completed (agent_message done) →
// turn.completed. One JSON object per line.
//
// Fields tied to a real model (reasoning_output_tokens, cached tokens,
// tool_calls, file changes) are zero/empty — see COMPATIBILITY.md.
func emitExecStreamJSON(w io.Writer, threadID, prompt, finalMessage string) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	// 1. thread.started — emitted as the first event when a new thread starts.
	_ = enc.Encode(map[string]any{
		"type":      "thread.started",
		"thread_id": threadID,
	})

	// 2. turn.started — encompasses all events for the prompt.
	_ = enc.Encode(map[string]any{
		"type": "turn.started",
	})

	// 3. item.started — assistant message in-progress. Upstream emits
	// this frame with empty/partial text since the model hasn't finished
	// streaming; testagent has no streaming so we emit an empty text and
	// reserve the final string for item.completed. Codex item ids are
	// opaque; "item_0" matches the upstream EventProcessor's
	// next_item_id format.
	const itemID = "item_0"
	_ = enc.Encode(map[string]any{
		"type": "item.started",
		"item": map[string]any{
			"id":   itemID,
			"type": "agent_message",
			"text": "",
		},
	})

	// 4. item.completed — assistant message terminal; carries the final text.
	_ = enc.Encode(map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"id":   itemID,
			"type": "agent_message",
			"text": finalMessage,
		},
	})

	// 5. turn.completed — usage summary.
	_ = enc.Encode(map[string]any{
		"type":  "turn.completed",
		"usage": execUsage(prompt, finalMessage),
	})
}

// execUsage returns a Usage object matching codex-rs/exec/exec_events.rs's
// Usage struct: input_tokens / cached_input_tokens / output_tokens /
// reasoning_output_tokens. Token counts are approximate (~4 chars/token)
// since testagent doesn't run a real tokenizer; cached and reasoning
// counts are zero (no real model).
func execUsage(prompt, response string) map[string]any {
	return map[string]any{
		"input_tokens":            approxTokens(prompt),
		"cached_input_tokens":     0,
		"output_tokens":           approxTokens(response),
		"reasoning_output_tokens": 0,
	}
}

// approxTokens is a rough token estimate (~4 chars/token) for usage stats.
// testagent doesn't run a real tokenizer; this is fixture-quality only.
// Mirrors cmd/claude/print.go's helper.
func approxTokens(s string) int {
	if len(s) == 0 {
		return 0
	}
	n := len(s) / 4
	if n == 0 {
		return 1
	}
	return n
}
