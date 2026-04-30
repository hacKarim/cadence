// Package sandbox confines a step item to its jail. cadence claims isolation,
// so it either enforces that claim with a real OS mechanism or says plainly
// that it could not.
package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
)

// Backend names an isolation mechanism.
type Backend string

// The available backends.
const (
	BackendSeatbelt   Backend = "seatbelt"
	BackendBubblewrap Backend = "bubblewrap"
	BackendConfined   Backend = "confined"
)

// Sandbox builds the command that runs one step item inside its jail.
type Sandbox interface {
	// Name is the backend's identifier, shown by cadence doctor.
	Name() Backend
	// Enforced reports whether the kernel actually prevents escape. A false
	// here is a real degradation and is surfaced to the user, never hidden.
	Enforced() bool
	// Wrap builds the command for argv, confined to the jail.
	Wrap(ctx context.Context, j *jailRef, argv []string) (*exec.Cmd, error)
}

// Require returns an error when the backend cannot actually enforce
// isolation, for workflows that set require_sandbox.
func Require(s Sandbox) error {
	if s.Enforced() {
		return nil
	}
	return fmt.Errorf(
		"this workflow sets require_sandbox, but the active backend is %q, which cannot enforce isolation; "+
			"install bubblewrap on Linux, or run on macOS where sandbox-exec is available",
		s.Name())
}

// Detect picks the best backend available on this machine, falling back to
// Confined when no enforcing mechanism is present.
func Detect() Sandbox {
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("sandbox-exec"); err == nil {
			return Seatbelt{}
		}
	case "linux":
		if _, err := exec.LookPath("bwrap"); err == nil {
			return Bubblewrap{}
		}
	}
	return Confined{}
}
