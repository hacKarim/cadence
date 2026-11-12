package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
func bwrapArgs(j *jailRef, argv []string) []string { return bwrapArgsWithPolicy(j, argv, Policy{}) }

func bwrapArgsWithPolicy(j *jailRef, argv []string, pol Policy) []string {
	args := []string{
		"--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts",
		"--share-net",
		"--die-with-parent",
		// Without a new session a sandboxed process can push characters into
		// the controlling terminal; bubblewrap warns about this itself.
		"--new-session",
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
	}
	for _, p := range []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc", "/opt"} {
		if _, err := os.Stat(p); err == nil {
			args = append(args, "--ro-bind", p, p)
		}
	}
	// On a systemd-resolved host /etc/resolv.conf is a symlink into /run, so
	// binding /etc alone leaves the sandbox unable to resolve a name, and
	// agent steps have to reach the API.
	for _, p := range []string{"/run/systemd/resolve", "/etc/resolv.conf"} {
		args = append(args, "--ro-bind-try", p, p)
	}
	args = append(args, "--bind", j.Work, j.Work, "--bind", j.Out, j.Out)
	for _, p := range pol.ReadWrite {
		// Binding /tmp itself would mount straight over the private tmpfs
		// above and hand every step the shared host directory. A specific
		// path inside it is still grantable, and lands inside the tmpfs.
		if filepath.Clean(p) == "/tmp" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			args = append(args, "--bind", p, p)
		}
	}
	args = append(args, "--chdir", j.Work, "--")
	return append(args, argv...)
}

// Wrap runs argv under bwrap.
func (Bubblewrap) Wrap(ctx context.Context, j *jailRef, argv []string, pol Policy) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, "bwrap", bwrapArgsWithPolicy(j, argv, pol)...)
	cmd.Dir = j.Work
	return cmd, nil
}
