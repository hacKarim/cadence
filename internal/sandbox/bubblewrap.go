package sandbox

import (
	"context"
	"os"
	"os/exec"
)

// Bubblewrap confines a step with bwrap: a fresh mount namespace where the
// only writable paths are the jail's work and out directories.
type Bubblewrap struct{}

// Name identifies the backend.
func (Bubblewrap) Name() Backend { return BackendBubblewrap }

// Enforced is true: the namespace is applied by the kernel.
func (Bubblewrap) Enforced() bool { return true }

// bwrapArgs builds the argument list. The item root and its meta directory are
// never bound, so a step cannot reach the engine's logs or a sibling's output.
func bwrapArgs(j *jailRef, argv []string) []string {
	args := []string{
		"--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts",
		"--share-net",
		"--die-with-parent",
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
	}
	for _, p := range []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc", "/opt"} {
		if _, err := os.Stat(p); err == nil {
			args = append(args, "--ro-bind", p, p)
		}
	}
	args = append(args,
		"--bind", j.Work, j.Work,
		"--bind", j.Out, j.Out,
		"--chdir", j.Work,
		"--",
	)
	return append(args, argv...)
}

// Wrap runs argv under bwrap.
func (Bubblewrap) Wrap(ctx context.Context, j *jailRef, argv []string) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, "bwrap", bwrapArgs(j, argv)...)
	cmd.Dir = j.Work
	return cmd, nil
}
