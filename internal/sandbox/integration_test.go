//go:build integration

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

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
