// Package cursorhooks runs Cursor-shaped hooks. Cursor hook handlers are
// shell command strings (or prompt-type entries, which testagent accepts but
// never fires — Cursor's prompt hooks are LLM-evaluated and testagent has no
// LLM). The Runner satisfies the same engine.HookSender / slash.ToolHookSender
// interfaces that internal/hooks and internal/codexhooks satisfy, so vendor
// selection is just choosing which struct to build at the cmd/cursor layer.
//
// Unmodeled Cursor events (sessionStart, sessionEnd, userMessage, …) are
// not yet wired. OnSessionStart, OnSessionEnd, OnPreCompact, OnPostCompact,
// and OnPrompt are no-ops in this implementation.
package cursorhooks

// Gating event names — fire before the operation and return a permission
// decision. All are routed to PreToolUse aggregation in hookresult.
const (
	// EventBeforeShellExecution fires before a shell command runs.
	EventBeforeShellExecution = "beforeShellExecution"

	// EventBeforeReadFile fires before a file is read.
	EventBeforeReadFile = "beforeReadFile"

	// EventBeforeMCPExecution fires before an MCP tool call.
	EventBeforeMCPExecution = "beforeMCPExecution"

	// EventPreToolUse fires before any tool use (catch-all gate).
	EventPreToolUse = "preToolUse"

	// EventSubagentStart fires before a subagent is launched.
	EventSubagentStart = "subagentStart"
)

// Advisory event names — fire after-the-fact; no decision returned.
const (
	// EventAfterShellExecution fires after a shell command completes.
	EventAfterShellExecution = "afterShellExecution"

	// EventAfterFileEdit fires after a file is written or edited.
	EventAfterFileEdit = "afterFileEdit"

	// EventAfterMCPExecution fires after an MCP tool call completes.
	EventAfterMCPExecution = "afterMCPExecution"

	// EventSubagentStop fires after a subagent exits.
	EventSubagentStop = "subagentStop"

	// EventAgentResponse fires when the agent emits a response.
	EventAgentResponse = "agentResponse"

	// EventStop fires when the agent session stops.
	EventStop = "stop"
)
