package cursor

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/paultyng/testagent/internal/rootflags"
)

func TestStubSubcommands_AllRegistered(t *testing.T) {
	t.Parallel()

	want := map[string]bool{
		"login":       false,
		"logout":      false,
		"status":      false,
		"about":       false,
		"models":      false,
		"update":      false,
		"create-chat": false,
		"resume":      false,
		"ls":          false,
	}

	cmd := NewCommand(&rootflags.Flags{})
	for _, sub := range cmd.Commands() {
		if _, ok := want[sub.Name()]; ok {
			want[sub.Name()] = true
		}
	}

	for name, found := range want {
		if !found {
			t.Errorf("subcommand %q not registered", name)
		}
	}
}

func TestStubSubcommands(t *testing.T) {
	t.Parallel()

	rf := &rootflags.Flags{}

	tests := []struct {
		name     string
		args     []string
		wantOut  string
		wantErr  bool
		jsonKeys []string // if set, parse JSON and assert these keys exist
	}{
		{
			name:    "login",
			args:    []string{"login"},
			wantOut: "Cursor authentication is stubbed in testagent. No real login performed.\n",
		},
		{
			name:    "logout",
			args:    []string{"logout"},
			wantOut: "Cursor session cleared (stub).\n",
		},
		{
			name:    "status/text",
			args:    []string{"status"},
			wantOut: "signed in: testagent-stub\nuser: testagent\n",
		},
		{
			name:     "status/json",
			args:     []string{"status", "--format", "json"},
			jsonKeys: []string{"signed_in", "user"},
		},
		{
			name:    "status/unknown-format",
			args:    []string{"status", "--format", "xml"},
			wantErr: true,
		},
		{
			name:    "about/text",
			args:    []string{"about"},
			wantOut: "cursor agent (testagent stub)\nversion: 3.2.16-stub\n",
		},
		{
			name:     "about/json",
			args:     []string{"about", "--format", "json"},
			jsonKeys: []string{"name", "version"},
		},
		{
			name:    "about/unknown-format",
			args:    []string{"about", "--format", "toml"},
			wantErr: true,
		},
		{
			name:    "models",
			args:    []string{"models"},
			wantOut: "auto\ngrok-fast-stub\ngpt-5-stub\nclaude-sonnet-4-stub\nsonic-stub\n",
		},
		{
			name:    "update",
			args:    []string{"update"},
			wantOut: "Already up to date (testagent stub).\n",
		},
		{
			name:    "create-chat",
			args:    []string{"create-chat"},
			wantOut: "Created chat: stub-chat-id-001\n",
		},
		{
			name:    "resume/no-arg",
			args:    []string{"resume"},
			wantOut: "Resuming most recent chat: stub-chat-id-001\n",
		},
		{
			name:    "resume/with-arg",
			args:    []string{"resume", "my-chat-42"},
			wantOut: "Resuming chat: my-chat-42\n",
		},
		{
			name:    "ls",
			args:    []string{"ls"},
			wantOut: "stub-chat-id-001  2026-05-17  cursor stub chat\nstub-chat-id-002  2026-05-16  earlier stub chat\n",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			cmd := NewCommand(rf)
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs(tc.args)

			err := cmd.ExecuteContext(t.Context())

			if tc.wantErr {
				if err == nil {
					t.Fatalf("args %v: expected error, got nil", tc.args)
				}
				return
			}

			if err != nil {
				t.Fatalf("args %v: unexpected error: %v", tc.args, err)
			}

			if tc.jsonKeys != nil {
				var got map[string]any
				if jsonErr := json.Unmarshal(buf.Bytes(), &got); jsonErr != nil {
					t.Fatalf("args %v: output is not valid JSON: %v\noutput: %s", tc.args, jsonErr, buf.String())
				}
				for _, k := range tc.jsonKeys {
					if _, ok := got[k]; !ok {
						t.Errorf("args %v: JSON missing key %q", tc.args, k)
					}
				}
				return
			}

			if got := buf.String(); got != tc.wantOut {
				t.Errorf("args %v:\ngot:  %q\nwant: %q", tc.args, got, tc.wantOut)
			}
		})
	}
}
