package main

import (
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/hacKarim/cadence/internal/sandbox"
	"github.com/hacKarim/cadence/internal/version"
)

func cmdDoctor(args []string, stdout, stderr io.Writer) int {
	_ = args
	fmt.Fprintf(stdout, "%s\n\n", version.String())
	fmt.Fprintf(stdout, "data directory:  %s\n", dataDir())

	sb := sandbox.Detect()
	fmt.Fprintf(stdout, "sandbox backend: %s\n", sb.Name())
	if sandbox.Overridden() {
		fmt.Fprintln(stdout, "                 (forced through CADENCE_SANDBOX)")
	}
	if sb.Enforced() {
		fmt.Fprintln(stdout, "                 isolation is enforced by the kernel")
	} else {
		fmt.Fprintln(stdout, "                 WARNING: not enforced. Steps run with their working")
		fmt.Fprintln(stdout, "                 directory set, but nothing stops a step reading or")
		fmt.Fprintln(stdout, "                 writing elsewhere. Install bubblewrap on Linux, or")
		fmt.Fprintln(stdout, "                 run on macOS, for a real sandbox. Workflows that set")
		fmt.Fprintln(stdout, "                 require_sandbox will refuse to run.")
	}

	bin := claudeBin()
	if p, err := exec.LookPath(bin); err != nil {
		fmt.Fprintf(stdout, "claude CLI:      not found (%s). Agent steps will fail.\n", bin)
	} else {
		fmt.Fprintf(stdout, "claude CLI:      %s\n", p)
		ctxBin := exec.Command(p, "--version")
		done := make(chan struct{})
		var out []byte
		go func() { out, _ = ctxBin.Output(); close(done) }()
		select {
		case <-done:
			if len(out) > 0 {
				fmt.Fprintf(stdout, "                 %s", out)
			}
		case <-time.After(3 * time.Second):
			_ = ctxBin.Process.Kill()
		}
	}
	return 0
}

func cmdVersion(stdout io.Writer) int {
	fmt.Fprintln(stdout, version.String())
	return 0
}
