package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hacKarim/cadence/internal/jail"
	"github.com/hacKarim/cadence/internal/sandbox"
	"github.com/hacKarim/cadence/internal/spec"
)

func newJail(t *testing.T) *jail.Jail {
	t.Helper()
	j, err := jail.Create(t.TempDir(), "step", 0)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func shellReq(t *testing.T, cmd string) Request {
	return Request{
		Step:    spec.Step{ID: "s", Run: cmd},
		Jail:    newJail(t),
		Env:     []string{"PATH=" + os.Getenv("PATH")},
		Timeout: 30 * time.Second,
	}
}

func TestShellCapturesStdout(t *testing.T) {
	r := shellReq(t, "echo hello")
	res, err := NewShell(sandbox.Confined{}).Run(context.Background(), r)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(res.Stdout) != "hello" {
		t.Errorf("Stdout = %q", res.Stdout)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", res.ExitCode)
	}
}

func TestShellWritesLogsToMeta(t *testing.T) {
	r := shellReq(t, "echo out; echo err 1>&2")
	if _, err := NewShell(sandbox.Confined{}).Run(context.Background(), r); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, f := range []string{"stdout.log", "stderr.log"} {
		if _, err := os.Stat(filepath.Join(r.Jail.Meta, f)); err != nil {
			t.Errorf("%s not written: %v", f, err)
		}
	}
}

func TestShellNonZeroExitIsReported(t *testing.T) {
	r := shellReq(t, "echo boom 1>&2; exit 3")
	res, err := NewShell(sandbox.Confined{}).Run(context.Background(), r)
	if err != nil {
		t.Fatalf("Run returned a transport error: %v", err)
	}
	if res.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "boom") {
		t.Errorf("Stderr = %q", res.Stderr)
	}
}

func TestShellTimeoutMarksTimedOut(t *testing.T) {
	r := shellReq(t, "sleep 5")
	r.Timeout = 150 * time.Millisecond
	res, err := NewShell(sandbox.Confined{}).Run(context.Background(), r)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.TimedOut {
		t.Error("TimedOut = false, want true")
	}
}

// A step that prints far more than fits in memory must still run: the full
// output belongs in the log file, and only a capped excerpt is kept in RAM.
func TestShellOutputIsCappedAndStreamed(t *testing.T) {
	r := shellReq(t, "dd if=/dev/zero bs=1024 count=20000 2>/dev/null | tr '\\0' 'x'")
	res, err := NewShell(sandbox.Confined{}).Run(context.Background(), r)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Stdout) > MaxCaptureBytes+256 {
		t.Errorf("captured %d bytes in memory, cap is %d", len(res.Stdout), MaxCaptureBytes)
	}
	if !strings.Contains(res.Stdout, "truncated") {
		t.Error("capped output does not say it was truncated")
	}
	st, err := os.Stat(filepath.Join(r.Jail.Meta, "stdout.log"))
	if err != nil {
		t.Fatalf("stdout.log: %v", err)
	}
	if st.Size() < 20_000_000 {
		t.Errorf("stdout.log is %d bytes, want the full ~20MB stream", st.Size())
	}
}
