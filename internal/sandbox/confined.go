package sandbox

import (
	"context"
	"os/exec"
)

// Confined is the fallback backend. It sets the working directory and the
// environment, which stops accidents, but nothing stops a determined process
// from reading or writing elsewhere. cadence reports this state rather than
// implying a guarantee it cannot keep.
type Confined struct{}

// Name identifies the backend.
func (Confined) Name() Backend { return BackendConfined }

// Enforced is false: there is no kernel enforcement here.
func (Confined) Enforced() bool { return false }

// Wrap runs the command directly, with the jail as its working directory.
func (Confined) Wrap(ctx context.Context, j *jailRef, argv []string) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = j.Work
	return cmd, nil
}
