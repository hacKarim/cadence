package sandbox

import (
	"context"
	"strings"
	"testing"

	"github.com/hacKarim/cadence/internal/jail"
)

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
		cmd, err := sb.Wrap(context.Background(), j, []string{"/bin/echo", "hi"})
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
		if !strings.Contains(p, `(subpath "`+dir+`")`) {
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

func TestSeatbeltWrapInvokesSandboxExec(t *testing.T) {
	j := newJail(t)
	cmd, err := Seatbelt{}.Wrap(context.Background(), j, []string{"/bin/echo", "hi"})
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
