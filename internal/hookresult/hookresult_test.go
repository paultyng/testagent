package hookresult

import (
	"testing"
)

func TestParseBody(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want Result
	}{
		{
			name: "empty body",
			body: "",
			want: Result{},
		},
		{
			name: "whitespace only",
			body: "   \n",
			want: Result{},
		},
		{
			name: "malformed JSON",
			body: `{"decision":`,
			want: Result{},
		},
		{
			name: "permissionDecision allow",
			body: `{"hookSpecificOutput":{"permissionDecision":"allow"}}`,
			want: Result{Allow: true},
		},
		{
			name: "permissionDecision deny with reason",
			body: `{"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"not in allowlist"}}`,
			want: Result{Block: true, Reason: "not in allowlist"},
		},
		{
			name: "permissionDecision ask with reason",
			body: `{"hookSpecificOutput":{"permissionDecision":"ask","permissionDecisionReason":"please confirm"}}`,
			want: Result{Ask: true, Reason: "please confirm"},
		},
		{
			name: "permissionDecision defer falls back to legacy",
			body: `{"hookSpecificOutput":{"permissionDecision":"defer"},"decision":"block","reason":"deferred and blocked"}`,
			want: Result{Block: true, Reason: "deferred and blocked"},
		},
		{
			name: "PermissionRequest allow",
			body: `{"hookSpecificOutput":{"decision":{"behavior":"allow"}}}`,
			want: Result{Allow: true},
		},
		{
			name: "PermissionRequest allow with message",
			body: `{"hookSpecificOutput":{"decision":{"behavior":"allow","message":"approved by alice"}}}`,
			want: Result{Allow: true, Reason: "approved by alice"},
		},
		{
			name: "PermissionRequest deny with message",
			body: `{"hookSpecificOutput":{"decision":{"behavior":"deny","message":"timed out"}}}`,
			want: Result{Block: true, Reason: "timed out"},
		},
		{
			name: "legacy decision block",
			body: `{"decision":"block","reason":"nope"}`,
			want: Result{Block: true, Reason: "nope"},
		},
		{
			name: "legacy decision approve",
			body: `{"decision":"approve"}`,
			want: Result{Allow: true},
		},
		{
			name: "unknown decision string is no-op",
			body: `{"decision":"maybe"}`,
			want: Result{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ParseBody([]byte(tc.body))
			// Raw always echoes the input body — assert it unconditionally
			// so misaligned expectations surface (e.g. nil-vs-empty drift).
			if string(got.Raw) != tc.body {
				t.Errorf("Raw = %q, want %q", got.Raw, tc.body)
			}
			if got.Block != tc.want.Block || got.Ask != tc.want.Ask || got.Allow != tc.want.Allow || got.Reason != tc.want.Reason {
				t.Errorf("decision = {Block:%v Ask:%v Allow:%v Reason:%q}, want {Block:%v Ask:%v Allow:%v Reason:%q}",
					got.Block, got.Ask, got.Allow, got.Reason,
					tc.want.Block, tc.want.Ask, tc.want.Allow, tc.want.Reason)
			}
		})
	}
}

func TestParseCommand(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		exitCode int
		stdout   string
		stderr   string
		want     Result
	}{
		{
			name:     "exit 0 with allow body",
			exitCode: 0,
			stdout:   `{"hookSpecificOutput":{"permissionDecision":"allow"}}`,
			want:     Result{Allow: true},
		},
		{
			name:     "exit 0 empty stdout",
			exitCode: 0,
			stdout:   "",
			want:     Result{},
		},
		{
			name:     "exit 2 produces block with stderr reason",
			exitCode: 2,
			stdout:   "ignored",
			stderr:   "blocked: dangerous command\n",
			want:     Result{Block: true, Reason: "blocked: dangerous command"},
		},
		{
			name:     "exit 2 with empty stderr still blocks",
			exitCode: 2,
			stdout:   "",
			stderr:   "",
			want:     Result{Block: true, Reason: ""},
		},
		{
			name:     "non-zero non-2 exit is non-blocking",
			exitCode: 1,
			stdout:   "garbage",
			stderr:   "oops",
			want:     Result{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ParseCommand(tc.exitCode, []byte(tc.stdout), []byte(tc.stderr))
			// Raw is documented to echo stdout regardless of exit code —
			// in the exit-2 path the reason comes from stderr but Raw
			// preserves stdout for diagnostic visibility.
			if string(got.Raw) != tc.stdout {
				t.Errorf("Raw = %q, want %q (stdout)", got.Raw, tc.stdout)
			}
			if got.Block != tc.want.Block || got.Ask != tc.want.Ask || got.Allow != tc.want.Allow || got.Reason != tc.want.Reason {
				t.Errorf("decision = {Block:%v Ask:%v Allow:%v Reason:%q}, want {Block:%v Ask:%v Allow:%v Reason:%q}",
					got.Block, got.Ask, got.Allow, got.Reason,
					tc.want.Block, tc.want.Ask, tc.want.Allow, tc.want.Reason)
			}
		})
	}
}

func TestAggregate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		event   string
		results []Result
		want    Result
	}{
		{
			name:  "PreToolUse empty",
			event: "PreToolUse",
			want:  Result{},
		},
		{
			name:    "PreToolUse all allow propagates allow",
			event:   "PreToolUse",
			results: []Result{{Allow: true}, {Allow: true}},
			want:    Result{Allow: true},
		},
		{
			name:    "PreToolUse any block wins, first reason carries",
			event:   "PreToolUse",
			results: []Result{{Allow: true}, {Block: true, Reason: "first"}, {Block: true, Reason: "second"}},
			want:    Result{Block: true, Reason: "first"},
		},
		{
			name:    "PreToolUse ask beats allow",
			event:   "PreToolUse",
			results: []Result{{Allow: true}, {Ask: true, Reason: "confirm please"}},
			want:    Result{Ask: true, Reason: "confirm please"},
		},
		{
			name:    "PreToolUse block beats ask",
			event:   "PreToolUse",
			results: []Result{{Ask: true, Reason: "ask"}, {Block: true, Reason: "nope"}},
			want:    Result{Block: true, Reason: "nope"},
		},
		{
			name:  "codex pre_tool_use uses same rule",
			event: "pre_tool_use",
			results: []Result{
				{Allow: true},
				{Block: true, Reason: "denied via exit 2"},
			},
			want: Result{Block: true, Reason: "denied via exit 2"},
		},
		{
			name:  "PermissionRequest any deny wins, returns first denier",
			event: "PermissionRequest",
			results: []Result{
				{Allow: true, Reason: "first allow"},
				{Block: true, Reason: "first deny"},
				{Allow: true, Reason: "third allow"},
			},
			want: Result{Block: true, Reason: "first deny"},
		},
		{
			name:  "PermissionRequest last allow wins when no deny",
			event: "PermissionRequest",
			results: []Result{
				{Allow: true, Reason: "first"},
				{Allow: true, Reason: "second"},
				{Allow: true, Reason: "third"},
			},
			want: Result{Allow: true, Reason: "third"},
		},
		{
			name:    "PermissionRequest empty results",
			event:   "PermissionRequest",
			results: []Result{{}, {}},
			want:    Result{},
		},
		{
			name:    "non-decision event returns zero result",
			event:   "Stop",
			results: []Result{{Block: true, Reason: "ignored"}},
			want:    Result{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Aggregate(tc.event, tc.results)
			if got.Block != tc.want.Block || got.Ask != tc.want.Ask || got.Allow != tc.want.Allow || got.Reason != tc.want.Reason {
				t.Errorf("aggregate = {Block:%v Ask:%v Allow:%v Reason:%q}, want {Block:%v Ask:%v Allow:%v Reason:%q}",
					got.Block, got.Ask, got.Allow, got.Reason,
					tc.want.Block, tc.want.Ask, tc.want.Allow, tc.want.Reason)
			}
		})
	}
}
