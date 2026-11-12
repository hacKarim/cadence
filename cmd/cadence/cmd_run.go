package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/hacKarim/cadence/internal/schedule"
	"github.com/hacKarim/cadence/internal/spec"

	"github.com/hacKarim/cadence/internal/engine"
	"github.com/hacKarim/cadence/internal/runner"
	"github.com/hacKarim/cadence/internal/sandbox"
	"github.com/hacKarim/cadence/internal/store"
)

// claudeBin is the agent binary, overridable so a test or an unusual install
// can point cadence at a different path.
func claudeBin() string {
	if b := os.Getenv("CADENCE_CLAUDE_BIN"); b != "" {
		return b
	}
	return "claude"
}

func newEngine(st *store.Store, opt engine.Options) *engine.Engine {
	sb := sandbox.Detect()
	return engine.New(st, map[string]runner.Runner{
		"shell": runner.NewShell(sb),
		"agent": runner.NewAgent(sb, claudeBin()),
	}, sb, opt)
}

func cmdRun(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	resume := fs.Int64("resume", 0, "resume a previous run by id")
	force := fs.Bool("force", false, "ignore cached results and re-run everything")
	maxPar := fs.Int("max-parallel", 4, "how many step items may run at once")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: cadence run <workflow.yaml> [--resume ID] [--force] [--max-parallel N]")
		return 2
	}

	wf, hash, err := loadWorkflow(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	st, err := openStore()
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return 1
	}
	defer st.Close()

	e := newEngine(st, engine.Options{
		MaxParallel: *maxPar,
		Force:       *force,
		ResumeFrom:  *resume,
		RunRoot:     filepath.Join(dataDir(), "runs"),
		Log:         stderr,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(stdout, "running %s (sandbox: %s)\n", wf.Name, e.Backend())

	// A manual run obeys the same concurrency policy as the daemon. The lock
	// is in the database, so the two cannot start the same workflow twice.
	exec := &manualExecutor{engine: e, store: st, wf: wf, out: stdout}
	launched, err := (&schedule.Launcher{Store: st, Exec: exec}).Launch(ctx, wf, hash)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	if !launched {
		fmt.Fprintf(stdout, "skipped: another run of %q is still going (concurrency: %s)\n", wf.Name, wf.Concurrency)
		return 1
	}

	run := exec.run
	if run == nil {
		return 1
	}
	_ = notifyRun(ctx, st, wf, run, stdout)
	printRunTree(stdout, st, run.ID)
	fmt.Fprintf(stdout, "\nrun %d %s\n", run.ID, run.State)
	if run.Error != "" {
		fmt.Fprintf(stdout, "%s\n", run.Error)
	}
	if run.State != store.StateSucceeded {
		return 1
	}
	return 0
}

// manualExecutor runs the workflow once and keeps the resulting run so the
// command can report it after the launcher returns.
type manualExecutor struct {
	engine *engine.Engine
	store  *store.Store
	wf     *spec.Workflow
	out    io.Writer
	run    *store.Run
}

func (m *manualExecutor) Execute(ctx context.Context, wf *spec.Workflow, hash string) error {
	run, err := m.engine.Run(ctx, wf, hash)
	if err != nil {
		return err
	}
	m.run = run
	return nil
}
