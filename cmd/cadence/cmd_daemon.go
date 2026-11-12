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
	"time"

	"github.com/hacKarim/cadence/internal/engine"
	"github.com/hacKarim/cadence/internal/notify"
	"github.com/hacKarim/cadence/internal/sandbox"
	"github.com/hacKarim/cadence/internal/schedule"
	"github.com/hacKarim/cadence/internal/spec"
	"github.com/hacKarim/cadence/internal/store"
)

// engineExecutor adapts the engine to the daemon's Executor interface and
// sends the run's notification once it finishes.
type engineExecutor struct {
	store *store.Store
	out   io.Writer
}

func (x engineExecutor) Execute(ctx context.Context, wf *spec.Workflow, hash string) error {
	e := newEngine(x.store, engine.Options{
		MaxParallel: 4,
		RunRoot:     filepath.Join(dataDir(), "runs"),
		Trigger:     "schedule",
		Log:         x.out,
	})
	run, err := e.Run(ctx, wf, hash)
	if err != nil {
		return err
	}
	fmt.Fprintf(x.out, "cadence: %s run %d %s\n", wf.Name, run.ID, run.State)
	return notifyRun(ctx, x.store, wf, run, x.out)
}

// notifyRun posts the outcome if the workflow asked for it. A webhook failure
// is reported but never changes the run's own result.
func notifyRun(ctx context.Context, st *store.Store, wf *spec.Workflow, run *store.Run, out io.Writer) error {
	if wf.Notify == nil {
		return nil
	}
	tree, err := st.RunTree(run.ID)
	if err != nil {
		tree = nil
	}
	if err := notify.Send(ctx, wf.Notify, *run, tree); err != nil {
		fmt.Fprintf(out, "cadence: %v\n", err)
	}
	return nil
}

func cmdDaemon(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", filepath.Join(dataDir(), "workflows"), "directory of workflow files to watch")
	interval := fs.Duration("interval", 10*time.Second, "how often to check for due workflows")
	once := fs.Bool("once", false, "evaluate every workflow once and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if _, err := os.Stat(*dir); err != nil {
		fmt.Fprintf(stderr, "workflow directory: %v\n", err)
		return 1
	}
	st, err := openStore()
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return 1
	}
	defer st.Close()

	d := &schedule.Daemon{
		Dir:      *dir,
		Store:    st,
		Launcher: &schedule.Launcher{Store: st, Exec: engineExecutor{store: st, out: stdout}},
		Log:      stdout,
	}

	sb := sandbox.Detect()
	fmt.Fprintf(stdout, "cadence daemon watching %s (sandbox: %s)\n", *dir, sb.Name())
	if !sb.Enforced() {
		fmt.Fprintln(stdout, "cadence: WARNING isolation is not enforced on this machine; see cadence doctor")
	}
	for _, p := range listWorkflowNames(*dir) {
		fmt.Fprintf(stdout, "cadence: watching %s\n", p)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *once {
		if err := d.Tick(ctx); err != nil {
			fmt.Fprintf(stderr, "cadence: %v\n", err)
		}
		return 0
	}
	return boolExit(d.Run(ctx, *interval), stderr)
}

func boolExit(err error, stderr io.Writer) int {
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	return 0
}

// listWorkflowNames reports the workflows the daemon will watch, so a user
// who mistyped a filename sees it immediately rather than waiting for a
// schedule that never fires.
func listWorkflowNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if ext := filepath.Ext(e.Name()); ext != ".yaml" && ext != ".yml" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		wf, err := spec.Parse(b)
		if err != nil {
			out = append(out, e.Name()+" (does not parse)")
			continue
		}
		if wf.Schedule == "" {
			out = append(out, fmt.Sprintf("%s (no schedule, manual only)", wf.Name))
			continue
		}
		out = append(out, fmt.Sprintf("%s %s", wf.Name, wf.Schedule))
	}
	return out
}
