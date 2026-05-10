// Package ptytest is a tiny PTY harness for testagent end-to-end tests that
// must exercise the real terminal input path — chiefly the OSC-reply
// regression where stdin under a PTY receives escape sequences (background
// color queries, cursor reports) from the emulator that must not pollute the
// keyboard input buffer.
//
// Production callers should not import this package; it is test-scope only.

package ptytest

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// Session wraps a child process running under a pseudo-terminal. The exposed
// shape is deliberately narrow — Write raw bytes, Write an OSC 11 reply,
// ExpectContains until a substring appears, Close. That's all the OSC-reply
// regression test needs and keeps the helper auditable.
type Session struct {
	t      *testing.T
	cmd    *exec.Cmd
	pty    *os.File
	buf    *circBuf
	doneCh chan struct{}
}

// Spawn starts the testagent binary at binPath with the given args under a
// PTY of size 80x24. The PTY is wired to stdin/stdout/stderr of the child
// and the returned Session captures all stdout/stderr bytes into an in-memory
// rolling buffer that ExpectContains can scan.
//
// Cleanup (kill + pty close) is registered via t.Cleanup; callers may still
// call Close explicitly to assert exit ordering.
func Spawn(t *testing.T, binPath string, args ...string) *Session {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatalf("ptytest: pty.StartWithSize: %v", err)
	}

	s := &Session{
		t:      t,
		cmd:    cmd,
		pty:    ptmx,
		buf:    newCircBuf(1 << 20), // 1 MiB rolling capture is plenty for these tests
		doneCh: make(chan struct{}),
	}

	go s.readLoop()

	t.Cleanup(func() {
		_ = s.Close()
	})

	return s
}

// Write sends raw bytes to the child's stdin via the PTY master.
func (s *Session) Write(p []byte) error {
	s.t.Helper()
	_, err := s.pty.Write(p)
	return err
}

// WriteOSC11 writes an OSC 11 (background-color query) reply of the form
// `\x1b]11;rgb:0000/0000/0000<terminator>`. Real emulators terminate with
// either ST (`\x1b\\`) or BEL (`\x07`); both should be consumed cleanly by
// a v2 bubbletea input parser. The caller picks the terminator.
func (s *Session) WriteOSC11(terminator string) error {
	s.t.Helper()
	return s.Write([]byte("\x1b]11;rgb:0000/0000/0000" + terminator))
}

// ExpectContains polls the in-memory capture until substr appears or
// timeout elapses. Returns an error with a snapshot of the captured output
// when the deadline expires (truncated to the trailing 4 KiB so test logs
// stay readable).
func (s *Session) ExpectContains(substr string, timeout time.Duration) error {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(s.buf.String(), substr) {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	got := s.buf.String()
	if len(got) > 4096 {
		got = got[len(got)-4096:]
	}
	return errors.New("ptytest: timeout waiting for " + strconv.Quote(substr) + "\n--- tail of capture ---\n" + got)
}

// Close kills the child (if still running), closes the PTY, and waits for
// the read loop to exit. Safe to call more than once.
func (s *Session) Close() error {
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.pty.Close()
	select {
	case <-s.doneCh:
	case <-time.After(2 * time.Second):
	}
	return nil
}

func (s *Session) readLoop() {
	defer close(s.doneCh)
	tmp := make([]byte, 4096)
	for {
		n, err := s.pty.Read(tmp)
		if n > 0 {
			s.buf.Write(tmp[:n])
		}
		if err != nil {
			return
		}
	}
}

// circBuf is a tiny thread-safe rolling byte buffer. Writes append; once the
// cap is exceeded the oldest bytes are evicted. String() returns the current
// contents under a lock.
type circBuf struct {
	mu  sync.Mutex
	buf []byte
	cap int
}

func newCircBuf(cap int) *circBuf {
	return &circBuf{cap: cap}
}

func (b *circBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.cap {
		b.buf = b.buf[len(b.buf)-b.cap:]
	}
	return len(p), nil
}

func (b *circBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
