// Package hookresult parses and aggregates hook responses across vendors.
// Three vendor packages call into this package after invoking a hook target,
// so the wire shapes are decoded in one place:
//   - internal/hooks       claude HTTP + command
//   - internal/codexhooks  codex command-only
//   - internal/cursorhooks cursor command (and "prompt" no-op)
//
// Four wire shapes are handled:
//
//  1. PreToolUse, two paths in one body (claude/codex):
//     - preferred: {"hookSpecificOutput":{"permissionDecision":"allow|deny|ask|defer","permissionDecisionReason":"..."}}
//     - legacy:    {"decision":"approve|block","reason":"..."}
//  2. PermissionRequest, nested (claude/codex):
//     {"hookSpecificOutput":{"decision":{"behavior":"allow|deny","message":"..."}}}
//  3. Cursor top-level (cursor.com/docs/hooks):
//     {"permission":"allow|deny|ask","user_message":"...","agent_message":"..."}
//     Reason takes agent_message if non-empty, else user_message.
//  4. Command-hook exit codes (per the documented contract on all three vendors):
//     - exit 0: parse stdout as a JSON body using (1)-(3) above
//     - exit 2: blocking denial, message taken from stderr
//     - other:  non-blocking error, logged by caller, returns zero result
//
// Aggregation rules differ per event and are owned here. Callers pass the
// event name plus a slice of per-matcher Result values; Aggregate returns
// the per-event reduction.
package hookresult

import (
	"bytes"
	"encoding/json"
)

// Result is the parsed outcome of one hook invocation. Zero value means
// the hook did not return a structured decision (advisory event, malformed
// body, or non-blocking exit code).
type Result struct {
	// Block is true when the hook asked to deny or block the operation.
	// Source: top-level decision="block", hookSpecificOutput.permissionDecision="deny",
	// hookSpecificOutput.decision.behavior="deny", or command exit code 2.
	Block bool

	// Ask is true when the hook asked the user to resolve the decision.
	// Source: hookSpecificOutput.permissionDecision="ask". Claude PreToolUse
	// only; codex has no ask state.
	Ask bool

	// Allow is true when the hook explicitly allowed the operation. Distinct
	// from "no decision" (zero value) for PermissionRequest aggregation,
	// which uses last-allow-wins semantics.
	Allow bool

	// Reason is the human-readable message accompanying the decision.
	// Source: permissionDecisionReason / decision.message / stderr / reason.
	Reason string

	// Raw echoes the response body (HTTP) or stdout bytes (command) the
	// result was parsed from. Useful for tests, debug traces, and future
	// fields not yet modeled.
	Raw []byte
}

// ParseBody decodes a JSON response body returned by an HTTP hook or
// emitted on stdout by a command hook (exit 0). It tries the
// hookSpecificOutput paths first, then falls back to the legacy top-level
// decision/reason shape. Unparseable bodies return the zero Result with
// Raw set so callers can surface the bytes in debug output.
func ParseBody(body []byte) Result {
	r := Result{Raw: body}
	if len(bytes.TrimSpace(body)) == 0 {
		return r
	}
	var wire wireBody
	if err := json.Unmarshal(body, &wire); err != nil {
		return r
	}

	// Path 0: cursor top-level. Distinct field name ("permission") means
	// claude/codex bodies never collide here. agent_message wins over
	// user_message for Reason — agent_message is what the model sees.
	switch wire.Permission {
	case "deny":
		r.Block = true
		r.Reason = cursorReason(wire.AgentMessage, wire.UserMessage)
		return r
	case "allow":
		r.Allow = true
		r.Reason = cursorReason(wire.AgentMessage, wire.UserMessage)
		return r
	case "ask":
		r.Ask = true
		r.Reason = cursorReason(wire.AgentMessage, wire.UserMessage)
		return r
	}

	// Path 1: hookSpecificOutput.decision.behavior (PermissionRequest).
	if wire.HookSpecificOutput != nil {
		hso := wire.HookSpecificOutput
		if hso.Decision != nil {
			switch hso.Decision.Behavior {
			case "deny":
				r.Block = true
				r.Reason = hso.Decision.Message
				return r
			case "allow":
				r.Allow = true
				r.Reason = hso.Decision.Message
				return r
			}
		}
		// Path 2: hookSpecificOutput.permissionDecision (PreToolUse).
		switch hso.PermissionDecision {
		case "deny":
			r.Block = true
			r.Reason = hso.PermissionDecisionReason
			return r
		case "ask":
			r.Ask = true
			r.Reason = hso.PermissionDecisionReason
			return r
		case "allow":
			r.Allow = true
			r.Reason = hso.PermissionDecisionReason
			return r
			// "defer" intentionally falls through to the legacy path below.
		}
	}

	// Path 3: legacy top-level decision/reason.
	switch wire.Decision {
	case "block":
		r.Block = true
		r.Reason = wire.Reason
	case "approve":
		r.Allow = true
	}
	return r
}

// ParseCommand decodes a command-hook outcome by exit code. stdout is
// parsed via ParseBody when exitCode is 0; exit code 2 produces a blocking
// result with stderr (trimmed) as the reason. Other non-zero exit codes
// return the zero Result — they're non-blocking errors that the caller
// surfaces via its debug writer.
func ParseCommand(exitCode int, stdout, stderr []byte) Result {
	switch exitCode {
	case 0:
		return ParseBody(stdout)
	case 2:
		return Result{
			Block:  true,
			Reason: string(bytes.TrimSpace(stderr)),
			Raw:    stdout,
		}
	default:
		return Result{Raw: stdout}
	}
}

// Aggregate reduces per-matcher results to a single decision using the
// event's documented aggregation rule. The PermissionRequest rule (any
// deny wins; otherwise last allow wins) is distinct from the PreToolUse
// rule (any deny/block wins; ask beats allow; otherwise the latest
// reason carries). Events with no decision semantics (UserPromptSubmit,
// Stop, Notification, after*, etc.) return the zero Result.
//
// The event argument is the wire event name. All three vendor vocabularies
// are recognized:
//   - claude: "PreToolUse" / "PermissionRequest"
//   - codex:  "pre_tool_use" / "permission_request"
//   - cursor: "beforeShellExecution" / "beforeReadFile" / "beforeMCPExecution" /
//     "preToolUse" / "subagentStart" — all gating events route to PreToolUse
//     aggregation.
func Aggregate(event string, results []Result) Result {
	if len(results) == 0 {
		return Result{}
	}
	switch event {
	case "PreToolUse", "pre_tool_use",
		"beforeShellExecution", "beforeReadFile", "beforeMCPExecution",
		"preToolUse", "subagentStart":
		return aggregatePreToolUse(results)
	case "PermissionRequest", "permission_request":
		return aggregatePermissionRequest(results)
	default:
		return Result{}
	}
}

func aggregatePreToolUse(results []Result) Result {
	var out Result
	for _, r := range results {
		if r.Block {
			// Any block wins. First blocker's reason carries.
			if !out.Block {
				out.Block = true
				out.Reason = r.Reason
			}
		}
	}
	if out.Block {
		return out
	}
	for _, r := range results {
		if r.Ask {
			out.Ask = true
			out.Reason = r.Reason
			return out
		}
	}
	for _, r := range results {
		if r.Allow {
			out.Allow = true
			out.Reason = r.Reason
			return out
		}
	}
	return out
}

func aggregatePermissionRequest(results []Result) Result {
	var out Result
	for _, r := range results {
		if r.Block {
			// Any deny wins immediately; first denier's message carries.
			return Result{Block: true, Reason: r.Reason}
		}
		if r.Allow {
			// Latest allow carries (per codex's resolve_permission_request_decision).
			out = Result{Allow: true, Reason: r.Reason}
		}
	}
	return out
}

// cursorReason returns agentMessage when non-empty, else userMessage.
// Mirrors cursor.com/docs/hooks' rule that agent_message is the model-facing
// reason and user_message is the human-facing one; testagent surfaces the
// model-facing message into the engine since that's what the run consumes.
func cursorReason(agentMessage, userMessage string) string {
	if agentMessage != "" {
		return agentMessage
	}
	return userMessage
}

// wireBody is the union of all decision-bearing fields across the supported
// wire shapes. Optional pointers distinguish absent-vs-zero on the nested
// objects.
type wireBody struct {
	// Top-level legacy shape (claude/codex).
	Decision string `json:"decision"`
	Reason   string `json:"reason"`

	// Nested shape (claude/codex).
	HookSpecificOutput *hookSpecificOutput `json:"hookSpecificOutput"`

	// Cursor top-level shape.
	Permission   string `json:"permission"`
	UserMessage  string `json:"user_message"`
	AgentMessage string `json:"agent_message"`
}

type hookSpecificOutput struct {
	// PreToolUse path.
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason"`

	// PermissionRequest path.
	Decision *permissionDecision `json:"decision"`
}

type permissionDecision struct {
	Behavior string `json:"behavior"`
	Message  string `json:"message"`
}
