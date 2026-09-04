//go:build integration

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A sandbox that blocks escapes but also blocks the step's own work is not a
// sandbox, it is a broken runner.
func TestEnforcedBackendAllowsWriteInsideJail(t *testing.T) {
	sb := Detect()
	if !sb.Enforced() {
		t.Skipf("no enforcing sandbox available (backend %q)", sb.Name())
	}
	j := newJail(t)
	cmd, err := sb.Wrap(context.Background(), j, []string{"/bin/sh", "-c", "echo inside > ok.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("a step could not write inside its own jail: %v: %s", err, out)
	}
	b, err := os.ReadFile(filepath.Join(j.Work, "ok.txt"))
	if err != nil || string(b) != "inside\n" {
		t.Errorf("in-jail write did not land: %q %v", b, err)
	}
}

// The invariant the project exists for, enforced by the kernel.
func TestEnforcedBackendBlocksReadingNeighbourJail(t *testing.T) {
	sb := Detect()
	if !sb.Enforced() {
		t.Skipf("no enforcing sandbox available (backend %q)", sb.Name())
	}
	runDir := t.TempDir()
	analyst, _ := jailCreate(t, runDir, "analyse", 0)
	secret := filepath.Join(analyst.Out, "report.md")
	if err := os.WriteFile(secret, []byte("I think it is undervalued"), 0o600); err != nil {
		t.Fatal(err)
	}
	sceptic, _ := jailCreate(t, runDir, "challenge", 0)

	cmd, err := sb.Wrap(context.Background(), sceptic, []string{"/bin/sh", "-c", "cat " + secret})
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the sceptic read the analyst's report: %s", out)
	}
}

// TestEnforcedBackendBlocksWriteOutsideJail is the only test that proves the
// sandbox actually sandboxes. It needs the real mechanism, so it is tagged and
// skipped where that mechanism is unavailable.
func TestEnforcedBackendBlocksWriteOutsideJail(t *testing.T) {
	sb := Detect()
	if !sb.Enforced() {
		t.Skipf("no enforcing sandbox available (backend %q)", sb.Name())
	}
	j := newJail(t)
	escape := filepath.Join(j.Root, "escape")
	cmd, err := sb.Wrap(context.Background(), j, []string{"/bin/sh", "-c", "echo x > ../escape"})
	if err != nil {
		t.Fatal(err)
	}
	_ = cmd.Run()
	if _, err := os.Stat(escape); err == nil {
		t.Fatalf("%s allowed a write outside the jail", sb.Name())
	}
}
