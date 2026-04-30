package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Seatbelt confines a step with macOS sandbox-exec. The profile denies
// everything by default and then allows the reads a program needs to start,
// plus reads and writes inside the jail's work and out directories only.
type Seatbelt struct{}

// Name identifies the backend.
func (Seatbelt) Name() Backend { return BackendSeatbelt }

// Enforced is true: the kernel applies the profile.
func (Seatbelt) Enforced() bool { return true }

// readOnlySystemPaths are what a typical interpreter or binary needs to start.
var readOnlySystemPaths = []string{
	"/usr", "/bin", "/sbin", "/opt", "/System", "/Library", "/private/var/db/timezone", "/etc", "/dev",
}

// seatbeltProfile builds the sandbox profile for one jail. The item root and
// its meta directory are deliberately absent: meta holds the engine's own logs
// of this and sibling steps, and a step has no business reading them.
func seatbeltProfile(j *jailRef) string {
	var b strings.Builder
	b.WriteString("(version 1)\n")
	b.WriteString("(deny default)\n")
	b.WriteString("(allow process-exec)\n")
	b.WriteString("(allow process-fork)\n")
	b.WriteString("(allow sysctl-read)\n")
	b.WriteString("(allow mach-lookup)\n")
	b.WriteString("(allow network*)\n")
	b.WriteString("(allow signal (target self))\n")
	for _, p := range readOnlySystemPaths {
		fmt.Fprintf(&b, "(allow file-read* (subpath %q))\n", p)
	}
	for _, p := range []string{j.Work, j.Out} {
		fmt.Fprintf(&b, "(allow file-read* (subpath %q))\n", p)
		fmt.Fprintf(&b, "(allow file-write* (subpath %q))\n", p)
	}
	return b.String()
}

// Wrap runs argv under sandbox-exec with a generated profile.
func (Seatbelt) Wrap(ctx context.Context, j *jailRef, argv []string) (*exec.Cmd, error) {
	args := append([]string{"-p", seatbeltProfile(j), "--"}, argv...)
	cmd := exec.CommandContext(ctx, "sandbox-exec", args...)
	cmd.Dir = j.Work
	return cmd, nil
}
