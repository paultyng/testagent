package mcp

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// fixturePath is set by TestMain to the absolute path of the pre-built
// stdio fixture binary. Tests reference it via stdioFixtureBinary(t).
var fixturePath string

// TestMain pre-builds the testdata/stdio-server binary once per package
// test run into a tempdir, populates fixturePath, then runs the test
// suite. Builds fail loudly: a missing fixture is a setup error, not a
// per-test skip.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "mcp-fixture-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: mkdir tempdir: %v\n", err)
		os.Exit(2)
	}
	defer os.RemoveAll(dir)

	bin := filepath.Join(dir, "stdio-server")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "./testdata/stdio-server/")
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: build fixture: %v\n", err)
		os.Exit(2)
	}
	fixturePath = bin

	os.Exit(m.Run())
}

// stdioFixtureBinary returns the path to the pre-built fixture binary.
// Fails the test if TestMain didn't populate it (should never happen).
func stdioFixtureBinary(t *testing.T) string {
	t.Helper()
	if fixturePath == "" {
		t.Fatal("stdioFixtureBinary: fixture not built (TestMain init failed)")
	}
	return fixturePath
}

// TestStdioFixture_BuildsAndResponds asserts the fixture binary exists,
// is executable, and runs without panicking when invoked. Acts as a
// smoke test for the build helper itself.
func TestStdioFixture_BuildsAndResponds(t *testing.T) {
	bin := stdioFixtureBinary(t)
	info, err := os.Stat(bin)
	if err != nil {
		t.Fatalf("stat fixture: %v", err)
	}
	if info.Mode()&0o111 == 0 && runtime.GOOS != "windows" {
		t.Errorf("fixture not executable: mode=%v", info.Mode())
	}
}
