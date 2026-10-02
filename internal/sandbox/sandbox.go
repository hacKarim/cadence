// Package sandbox confines a step item to its jail. cadence claims isolation,
// so it either enforces that claim with a real OS mechanism or says plainly
// that it could not.
package sandbox

import (
	"context"
	"fmt"
	"os"
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

// Policy names paths a process may read and write in addition to its jail.
// It exists because some programs cannot start without their own
// configuration or temporary directories. Granting them here keeps the extra
// reach declared and visible rather than accidental.
type Policy struct {
	ReadWrite []string
}

// Sandbox builds the command that runs one step item inside its jail.
type Sandbox interface {
	// Name is the backend's identifier, shown by cadence doctor.
	Name() Backend
	// Enforced reports whether the kernel actually prevents escape. A false
	// here is a real degradation and is surfaced to the user, never hidden.
	Enforced() bool
	// Wrap builds the command for argv, confined to the jail plus whatever
	// the policy grants.
	Wrap(ctx context.Context, j *jailRef, argv []string, p Policy) (*exec.Cmd, error)
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

// overrideEnv lets a user force a backend. It exists because an enforcing
// sandbox cannot always reach what an agent needs: on macOS the claude CLI
// keeps its credentials in the keychain, which is unreachable from inside a
// seatbelt jail. Forcing "confined" is a real loss of isolation, so it is
// explicit, and doctor and the TUI both say when it is in effect.
const overrideEnv = "CADENCE_SANDBOX"

// Overridden reports whether a backend was forced through the environment.
func Overridden() bool { return os.Getenv(overrideEnv) != "" }

// Detect picks the best backend available on this machine, falling back to
// Confined when no enforcing mechanism is present.
func Detect() Sandbox {
	if os.Getenv(overrideEnv) == string(BackendConfined) {
		return Confined{}
	}
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
