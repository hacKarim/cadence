package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hacKarim/cadence/internal/store"
)

func writeWF(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "wf.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func invoke(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv("CADENCE_DATA", t.TempDir())
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

const goodWF = `
name: demo
steps:
  - id: greet
    run: echo hello
`

func TestValidateCommandOnGoodFileExitsZero(t *testing.T) {
	code, out, errs := invoke(t, "validate", writeWF(t, goodWF))
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errs)
	}
	if !strings.Contains(out, "demo") {
		t.Errorf("stdout %q should name the workflow", out)
	}
}

func TestValidateCommandReportsErrorsAndExitsNonZero(t *testing.T) {
	bad := `
name: broken
steps:
  - id: a
    needs: [ghost]
    run: echo a
  - id: a
    run: echo a
`
	code, _, errs := invoke(t, "validate", writeWF(t, bad))
	if code == 0 {
		t.Fatal("exit 0 on an invalid workflow")
	}
	for _, want := range []string{"ghost", "duplicate"} {
		if !strings.Contains(errs, want) {
			t.Errorf("stderr %q should mention %q", errs, want)
		}
	}
}

func TestValidateMissingFileIsAnError(t *testing.T) {
	code, _, errs := invoke(t, "validate", "/no/such/file.yaml")
	if code == 0 || errs == "" {
		t.Errorf("exit %d stderr %q, want a readable error", code, errs)
	}
}

func TestDoctorNamesSandboxBackend(t *testing.T) {
	code, out, _ := invoke(t, "doctor")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "sandbox") {
		t.Errorf("doctor output %q should report the sandbox backend", out)
	}
}

func TestDoctorWarnsWhenClaudeMissing(t *testing.T) {
	t.Setenv("CADENCE_CLAUDE_BIN", "/definitely/not/here/claude")
	_, out, _ := invoke(t, "doctor")
	if !strings.Contains(strings.ToLower(out), "not found") {
		t.Errorf("doctor output %q should say the claude CLI was not found", out)
	}
}

func TestRunCommandExecutesAndReportsSteps(t *testing.T) {
	code, out, errs := invoke(t, "run", writeWF(t, goodWF))
	if code != 0 {
		t.Fatalf("exit %d, stdout %q stderr %q", code, out, errs)
	}
	if !strings.Contains(out, "greet") {
		t.Errorf("stdout %q should name the step", out)
	}
	if !strings.Contains(strings.ToLower(out), "succeeded") {
		t.Errorf("stdout %q should report the outcome", out)
	}
}

func TestRunCommandExitsNonZeroOnFailure(t *testing.T) {
	failing := "name: demo\nsteps:\n  - id: boom\n    run: exit 3\n"
	code, _, _ := invoke(t, "run", writeWF(t, failing))
	if code == 0 {
		t.Error("exit 0 although the workflow failed")
	}
}

func TestRunsAndLogsInspectAPreviousRun(t *testing.T) {
	data := t.TempDir()
	t.Setenv("CADENCE_DATA", data)
	wf := writeWF(t, goodWF)

	var out bytes.Buffer
	if code := run([]string{"run", wf}, &out, &out); code != 0 {
		t.Fatalf("run failed: %s", out.String())
	}
	out.Reset()
	if code := run([]string{"runs"}, &out, &out); code != 0 {
		t.Fatalf("runs failed: %s", out.String())
	}
	if !strings.Contains(out.String(), "demo") {
		t.Errorf("runs output %q should list the run", out.String())
	}
	out.Reset()
	if code := run([]string{"logs", "1"}, &out, &out); code != 0 {
		t.Fatalf("logs failed: %s", out.String())
	}
	if !strings.Contains(out.String(), "greet") {
		t.Errorf("logs output %q should show the step", out.String())
	}
}

func TestUnknownCommandShowsUsage(t *testing.T) {
	code, _, errs := invoke(t, "frobnicate")
	if code == 0 {
		t.Error("exit 0 on an unknown command")
	}
	if !strings.Contains(errs, "usage") {
		t.Errorf("stderr %q should show usage", errs)
	}
}

func TestVersionPrintsBuildInfo(t *testing.T) {
	code, out, _ := invoke(t, "version")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "cadence") {
		t.Errorf("version output %q", out)
	}
}

func TestDaemonOnceTicksAndExits(t *testing.T) {
	dir := t.TempDir()
	body := "name: nightly\nschedule: \"0 0 7 * * *\"\nsteps:\n  - id: a\n    run: echo a\n"
	if err := os.WriteFile(filepath.Join(dir, "nightly.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errs := invoke(t, "daemon", "--dir", dir, "--once")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errs)
	}
	if !strings.Contains(out, "nightly") {
		t.Errorf("daemon output %q should mention the workflow it is watching", out)
	}
}

func TestDaemonRejectsMissingDirectory(t *testing.T) {
	code, _, errs := invoke(t, "daemon", "--dir", "/no/such/dir", "--once")
	if code == 0 || errs == "" {
		t.Errorf("exit %d stderr %q, want an error", code, errs)
	}
}

func TestRunSendsNoNotificationWithoutANotifyBlock(t *testing.T) {
	// A workflow with no notify block must not attempt any webhook, which
	// would otherwise show up as an error on an offline machine.
	code, _, errs := invoke(t, "run", writeWF(t, goodWF))
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errs)
	}
	if strings.Contains(errs, "notify") {
		t.Errorf("stderr mentions notify for a workflow that configures none: %q", errs)
	}
}

// The README says the lock lives in the database so a manual run and the
// daemon cannot start the same workflow twice.
func TestRunRespectsConcurrencySkipAgainstARunningWorkflow(t *testing.T) {
	data := t.TempDir()
	t.Setenv("CADENCE_DATA", data)
	wf := writeWF(t, "name: demo\nconcurrency: skip\nsteps:\n  - id: greet\n    run: echo hello\n")

	st, err := store.Open(filepath.Join(data, "cadence.db"))
	if err != nil {
		t.Fatal(err)
	}
	ok, err := st.AcquireWorkflowLock("demo", 999)
	if err != nil || !ok {
		t.Fatalf("could not take the lock: %v %v", ok, err)
	}
	st.Close()

	var out bytes.Buffer
	code := run([]string{"run", wf}, &out, &out)
	if code == 0 {
		t.Errorf("manual run proceeded while the workflow was locked: %s", out.String())
	}
	if !strings.Contains(strings.ToLower(out.String()), "skip") {
		t.Errorf("output %q should say the run was skipped and why", out.String())
	}
}

func TestRunProceedsWhenNothingHoldsTheLock(t *testing.T) {
	code, out, _ := invoke(t, "run", writeWF(t, goodWF))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
}
