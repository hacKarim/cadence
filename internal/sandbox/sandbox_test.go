package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hacKarim/cadence/internal/jail"
)

// policyPathInTmp creates a real directory inside /tmp and returns its path.
// A policy path has to exist for bubblewrap to bind it, so a test that asserts
// on one cannot name a path it did not create: an earlier version of this test
// named a developer's own scratch directory and passed on that machine while
// failing on a clean one.
func policyPathInTmp(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "cadence-policy-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func newJail(t *testing.T) *jail.Jail {
	t.Helper()
	j, err := jail.Create(t.TempDir(), "step", 0)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestDetectReturnsNonNil(t *testing.T) {
	if s := Detect(); s == nil {
		t.Fatal("Detect returned nil")
	}
}

func TestConfinedReportsNotEnforced(t *testing.T) {
	c := Confined{}
	if c.Enforced() {
		t.Error("Confined claims kernel enforcement")
	}
	if c.Name() != BackendConfined {
		t.Errorf("Name = %q, want %q", c.Name(), BackendConfined)
	}
}

func TestRequireRejectsUnenforced(t *testing.T) {
	err := Require(Confined{})
	if err == nil {
		t.Fatal("Require accepted an unenforced backend")
	}
	if !strings.Contains(err.Error(), string(BackendConfined)) {
		t.Errorf("error %q should name the active backend", err)
	}
}

func TestRequireAcceptsEnforced(t *testing.T) {
	if err := Require(Seatbelt{}); err != nil {
		t.Errorf("Require rejected an enforced backend: %v", err)
	}
}

func TestWrapSetsCwdToWork(t *testing.T) {
	j := newJail(t)
	for _, sb := range []Sandbox{Confined{}, Seatbelt{}, Bubblewrap{}} {
		cmd, err := sb.Wrap(context.Background(), j, []string{"/bin/echo", "hi"}, Policy{})
		if err != nil {
			t.Fatalf("%s Wrap: %v", sb.Name(), err)
		}
		if cmd.Dir != j.Work {
			t.Errorf("%s cmd.Dir = %q, want %q", sb.Name(), cmd.Dir, j.Work)
		}
	}
}

func TestSeatbeltProfileDeniesOutsideWrites(t *testing.T) {
	j := newJail(t)
	p := seatbeltProfile(j)
	if !strings.Contains(p, "(deny default)") {
		t.Error("profile does not deny by default")
	}
	for _, dir := range []string{j.Work, j.Out} {
		if !strings.Contains(p, `(subpath "`+resolve(dir)+`")`) {
			t.Errorf("profile does not allow %q", dir)
		}
	}
	if strings.Contains(p, `(subpath "`+j.Root+`")`) {
		t.Error("profile allows the whole item root, which exposes meta/")
	}
	if strings.Contains(p, `(subpath "`+j.Meta+`")`) {
		t.Error("profile exposes the engine-owned meta directory to the step")
	}
}

// Without the system base profile, dyld cannot start a process at all and
// every sandboxed step aborts before running.
func TestSeatbeltProfileImportsSystemBase(t *testing.T) {
	if p := seatbeltProfile(newJail(t)); !strings.Contains(p, `(import "bsd.sb")`) {
		t.Error("profile does not import the system base profile")
	}
}

// On macOS the temporary and home directories are reached through symlinks.
// A profile written in terms of the unresolved path matches nothing, so the
// step cannot even write inside its own jail.
func TestSeatbeltProfileUsesResolvedPaths(t *testing.T) {
	j := newJail(t)
	real, err := filepath.EvalSymlinks(j.Work)
	if err != nil {
		t.Skip("cannot resolve the temp dir on this platform")
	}
	if real == j.Work {
		t.Skip("temp dir is not reached through a symlink here")
	}
	p := seatbeltProfile(j)
	if !strings.Contains(p, `(subpath "`+real+`")`) {
		t.Errorf("profile uses the unresolved path; it must use %q", real)
	}
}

func TestSeatbeltWrapInvokesSandboxExec(t *testing.T) {
	j := newJail(t)
	cmd, err := Seatbelt{}.Wrap(context.Background(), j, []string{"/bin/echo", "hi"}, Policy{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cmd.Path, "sandbox-exec") && cmd.Args[0] != "sandbox-exec" {
		t.Errorf("seatbelt did not invoke sandbox-exec: %v", cmd.Args)
	}
	if cmd.Args[len(cmd.Args)-2] != "/bin/echo" {
		t.Errorf("command not appended: %v", cmd.Args)
	}
}

func TestBubblewrapBindsWorkAndOutOnly(t *testing.T) {
	j := newJail(t)
	args := bwrapArgs(j, []string{"/bin/echo", "hi"})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--bind "+j.Work+" "+j.Work) {
		t.Errorf("work not bound: %v", joined)
	}
	if !strings.Contains(joined, "--bind "+j.Out+" "+j.Out) {
		t.Errorf("out not bound: %v", joined)
	}
	if strings.Contains(joined, j.Meta) {
		t.Error("meta directory bound into the sandbox")
	}
	if !strings.Contains(joined, "--chdir "+j.Work) {
		t.Error("chdir not set to work")
	}
}

// Some programs cannot run at all without their own configuration and
// temporary directories. The claude CLI is one: it opens a per-user temp
// directory before it does anything. A policy grants those paths explicitly,
// so what a step can reach stays declared rather than accidental.
func TestSeatbeltProfileAllowsPolicyPaths(t *testing.T) {
	j := newJail(t)
	granted := policyPathInTmp(t)
	p := seatbeltProfileWithPolicy(j, Policy{ReadWrite: []string{granted}})
	if !strings.Contains(p, `(subpath "`+resolve(granted)+`")`) {
		t.Errorf("profile does not grant the policy path:\n%s", p)
	}
}

func TestEmptyPolicyGrantsNothingExtra(t *testing.T) {
	j := newJail(t)
	withPolicy := seatbeltProfileWithPolicy(j, Policy{})
	plain := seatbeltProfile(j)
	if withPolicy != plain {
		t.Error("an empty policy changed the profile")
	}
}

func TestBubblewrapBindsPolicyPaths(t *testing.T) {
	j := newJail(t)
	grant := t.TempDir()
	args := bwrapArgsWithPolicy(j, []string{"/bin/true"}, Policy{ReadWrite: []string{grant}})
	if !strings.Contains(strings.Join(args, " "), "--bind "+grant+" "+grant) {
		t.Errorf("policy path not bound: %v", args)
	}
}

// Some setups cannot run an agent under an enforcing sandbox: on macOS the
// CLI's keychain credentials are unreachable from inside the jail. The escape
// hatch is explicit and visible rather than a silent downgrade.
func TestDetectHonoursExplicitOverride(t *testing.T) {
	t.Setenv("CADENCE_SANDBOX", "confined")
	if got := Detect().Name(); got != BackendConfined {
		t.Errorf("Detect = %q, want the override to be honoured", got)
	}
}

func TestDetectIgnoresUnknownOverride(t *testing.T) {
	t.Setenv("CADENCE_SANDBOX", "nonsense")
	if got := Detect().Name(); got == "" {
		t.Error("Detect returned no backend for an unknown override")
	}
}

func TestOverriddenReportsWhetherTheUserForcedIt(t *testing.T) {
	t.Setenv("CADENCE_SANDBOX", "confined")
	if !Overridden() {
		t.Error("Overridden = false although CADENCE_SANDBOX is set")
	}
}

// bubblewrap gives the step a private /tmp. Binding the host's /tmp after
// that would mount straight over it and hand every agent step the shared
// directory, which is a channel between the two agents the design separates.
func TestBubblewrapNeverBindsHostTmpOverItsOwnTmpfs(t *testing.T) {
	j := newJail(t)
	granted := policyPathInTmp(t)
	args := bwrapArgsWithPolicy(j, []string{"/bin/true"}, Policy{ReadWrite: []string{"/tmp", granted}})
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--bind /tmp /tmp") {
		t.Errorf("host /tmp bound over the private tmpfs: %v", joined)
	}
	if !strings.Contains(joined, "--tmpfs /tmp") {
		t.Error("no private tmpfs")
	}
	if !strings.Contains(joined, "--bind "+granted+" "+granted) {
		t.Error("a specific path inside /tmp should still be grantable")
	}
	if i, k := strings.Index(joined, "--tmpfs /tmp"), strings.Index(joined, "--bind "+granted); i > k {
		t.Error("the tmpfs must be mounted before anything is bound inside it")
	}
}

// bubblewrap warns about this itself: without a new session a sandboxed
// process can push characters into the controlling terminal.
func TestBubblewrapStartsANewSession(t *testing.T) {
	args := bwrapArgs(newJail(t), []string{"/bin/true"})
	if !strings.Contains(strings.Join(args, " "), "--new-session") {
		t.Error("bwrap is not given --new-session")
	}
}

// On a systemd-resolved host /etc/resolv.conf is a symlink into /run. Without
// it bound, nothing in the sandbox can resolve a name, and agent steps have
// to reach the API.
func TestBubblewrapCanResolveNames(t *testing.T) {
	joined := strings.Join(bwrapArgs(newJail(t), []string{"/bin/true"}), " ")
	if !strings.Contains(joined, "/run/systemd/resolve") && !strings.Contains(joined, "/etc/resolv.conf") {
		t.Errorf("no DNS configuration is reachable inside the sandbox: %v", joined)
	}
}
