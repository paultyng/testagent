package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestSlashHandler(out *bytes.Buffer) *SlashHandler {
	return &SlashHandler{
		name:        "Test",
		streamDelay: 0,
		sessionID:   "sid-test",
		cwd:         "/tmp",
		hooks:       NewHookSender(nil, "sid-test", "/tmp", "", "default"),
		mcp:         NewMCPClient(nil),
		out:         out,
	}
}

func TestSlash_NotASlash(t *testing.T) {
	t.Parallel()

	out := &bytes.Buffer{}
	h := newTestSlashHandler(out)
	got := h.Dispatch(context.Background(), "regular prompt")
	if got.Handled {
		t.Errorf("non-slash input should not be handled")
	}
	if out.Len() != 0 {
		t.Errorf("non-slash input should not write output, got %q", out.String())
	}
}

func TestSlash_Stream(t *testing.T) {
	t.Parallel()

	out := &bytes.Buffer{}
	h := newTestSlashHandler(out)
	h.Dispatch(context.Background(), "/stream hello world")
	got := strings.TrimRight(out.String(), "\n")
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestSlash_Think(t *testing.T) {
	t.Parallel()

	out := &bytes.Buffer{}
	h := newTestSlashHandler(out)
	h.Dispatch(context.Background(), "/think pondering deeply")
	if !strings.Contains(out.String(), "pondering deeply") {
		t.Errorf("output missing think text: %q", out.String())
	}
}

func TestSlash_Panel(t *testing.T) {
	t.Parallel()

	out := &bytes.Buffer{}
	h := newTestSlashHandler(out)
	h.Dispatch(context.Background(), "/panel a panel message")
	s := out.String()
	// lipgloss uses rounded corners (╭ ╰); in no-color test env still draws box chars.
	hasBorder := strings.Contains(s, "╭") || strings.Contains(s, "─")
	if !hasBorder {
		t.Errorf("panel missing border chars: %q", s)
	}
	if !strings.Contains(s, "a panel message") {
		t.Errorf("panel missing content")
	}
}

func TestSlash_Tool(t *testing.T) {
	t.Parallel()

	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = readAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	out := &bytes.Buffer{}
	h := newTestSlashHandler(out)
	h.hooks = NewHookSender(&Settings{
		Hooks: map[string][]HookMatcher{
			"PostToolUse": {{Hooks: []Hook{{Type: "http", URL: srv.URL, Timeout: 1}}}},
		},
	}, "sid-test", "/tmp", "", "default")

	h.Dispatch(context.Background(), `/tool read_file {"path":"foo.go"}`)

	if !strings.Contains(out.String(), "read_file") {
		t.Errorf("output missing tool name: %q", out.String())
	}
	if !strings.Contains(string(captured), `"tool_name":"read_file"`) {
		t.Errorf("hook payload missing tool_name: %s", captured)
	}
	if !strings.Contains(string(captured), `"path":"foo.go"`) {
		t.Errorf("hook payload missing tool_input: %s", captured)
	}
}

func TestSlash_Result(t *testing.T) {
	t.Parallel()

	out := &bytes.Buffer{}
	h := newTestSlashHandler(out)
	h.Dispatch(context.Background(), `/result {"ok":true}`)
	s := out.String()
	if !strings.Contains(s, "ok") {
		t.Errorf("result missing field: %q", s)
	}
	if !strings.Contains(s, "✓") {
		t.Errorf("result missing checkmark: %q", s)
	}
}

func TestSlash_Exit(t *testing.T) {
	t.Parallel()

	out := &bytes.Buffer{}
	h := newTestSlashHandler(out)

	noCode := h.Dispatch(context.Background(), "/exit")
	if !noCode.Exit || noCode.ExitCode != 0 {
		t.Errorf("/exit got Exit=%v Code=%d, want true/0", noCode.Exit, noCode.ExitCode)
	}

	withCode := h.Dispatch(context.Background(), "/exit 7")
	if !withCode.Exit || withCode.ExitCode != 7 {
		t.Errorf("/exit 7 got Exit=%v Code=%d, want true/7", withCode.Exit, withCode.ExitCode)
	}
}

func TestSlash_UnknownCommand(t *testing.T) {
	t.Parallel()

	out := &bytes.Buffer{}
	h := newTestSlashHandler(out)
	got := h.Dispatch(context.Background(), "/notacommand foo")
	if !got.Handled {
		t.Errorf("unknown slash should still be Handled (consumed); got %+v", got)
	}
}

func TestSplitFirstWord(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in, head, tail string
	}{
		{"foo bar baz", "foo", "bar baz"},
		{"  foo bar", "foo", "bar"},
		{"singleword", "singleword", ""},
		{"", "", ""},
	}
	for _, tc := range cases {
		head, tail := splitFirstWord(tc.in)
		if head != tc.head || tail != tc.tail {
			t.Errorf("splitFirstWord(%q) = (%q, %q), want (%q, %q)", tc.in, head, tail, tc.head, tc.tail)
		}
	}
}

func readAll(r interface {
	Read([]byte) (int, error)
}) ([]byte, error) {
	var buf bytes.Buffer
	tmp := make([]byte, 512)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf.Write(tmp[:n])
		}
		if err != nil {
			if err.Error() == "EOF" {
				return buf.Bytes(), nil
			}
			return buf.Bytes(), err
		}
	}
}
