package schedule

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hacKarim/cadence/internal/spec"
	"github.com/hacKarim/cadence/internal/store"
)

// Executor runs one workflow. The engine satisfies it; tests substitute a fake.
type Executor interface {
	Execute(ctx context.Context, wf *spec.Workflow, hash string) error
}

// Launcher applies a workflow's concurrency policy and then runs it. The lock
// lives in the database, so the policy holds across processes: a manual
// cadence run and the daemon cannot start the same workflow twice.
type Launcher struct {
	Store        *store.Store
	Exec         Executor
	PollInterval time.Duration
}

func (l *Launcher) poll() time.Duration {
	if l.PollInterval > 0 {
		return l.PollInterval
	}
	return time.Second
}

// Launch runs the workflow unless its concurrency policy says otherwise. It
// reports whether the workflow actually ran.
func (l *Launcher) Launch(ctx context.Context, wf *spec.Workflow, hash string) (bool, error) {
	switch wf.Concurrency {
	case spec.ConcurrencyAllow:
		return true, l.Exec.Execute(ctx, wf, hash)

	case spec.ConcurrencyQueue:
		for {
			ok, err := l.Store.AcquireWorkflowLock(wf.Name, 0)
			if err != nil {
				return false, err
			}
			if ok {
				defer l.Store.ReleaseWorkflowLock(wf.Name)
				return true, l.Exec.Execute(ctx, wf, hash)
			}
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case <-time.After(l.poll()):
			}
		}

	default: // skip
		ok, err := l.Store.AcquireWorkflowLock(wf.Name, 0)
		if err != nil {
			return false, err
		}
		if !ok {
			// A skip leaves a record. A scheduled run that silently did not
			// happen is the kind of thing nobody notices until it matters.
			return false, l.recordSkip(wf)
		}
		defer l.Store.ReleaseWorkflowLock(wf.Name)
		return true, l.Exec.Execute(ctx, wf, hash)
	}
}

func (l *Launcher) recordSkip(wf *spec.Workflow) error {
	now := time.Now()
	r := &store.Run{
		Workflow: wf.Name, State: store.StateSkipped, Trigger: "schedule",
		StartedAt: now, EndedAt: &now,
		Error: "skipped: a previous run of this workflow was still going (concurrency: skip)",
	}
	return l.Store.CreateRun(r)
}

// Daemon watches a directory of workflow files and launches the ones that
// become due.
type Daemon struct {
	Dir      string
	Store    *store.Store
	Launcher *Launcher
	Now      func() time.Time
	Log      io.Writer

	mu        sync.Mutex
	baselines map[string]time.Time
}

// Seed sets the instant a workflow is considered to have last run, used when
// the daemon starts and when tests drive it directly.
func (d *Daemon) Seed(workflow string, t time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.baselines == nil {
		d.baselines = map[string]time.Time{}
	}
	d.baselines[workflow] = t
}

func (d *Daemon) baseline(workflow string, fallback time.Time) time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	if t, ok := d.baselines[workflow]; ok {
		return t
	}
	return fallback
}

func (d *Daemon) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *Daemon) logf(format string, args ...any) {
	if d.Log != nil {
		fmt.Fprintf(d.Log, format+"\n", args...)
	}
}

// Tick evaluates every workflow in the directory once. A file that does not
// parse is reported but never stops the others: one bad edit should not
// silence every other schedule.
func (d *Daemon) Tick(ctx context.Context) error {
	paths, err := workflowFiles(d.Dir)
	if err != nil {
		return err
	}
	var problems []string
	for _, p := range paths {
		if err := d.tickOne(ctx, p); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", filepath.Base(p), err))
			d.logf("cadence: %s: %v", filepath.Base(p), err)
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return nil
}

func (d *Daemon) tickOne(ctx context.Context, path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	wf, err := spec.Parse(b)
	if err != nil {
		return err
	}
	if err := wf.Validate(); err != nil {
		return err
	}
	if wf.Schedule == "" {
		return nil
	}

	loc := time.Local
	if wf.Timezone != "" {
		if l, err := time.LoadLocation(wf.Timezone); err == nil {
			loc = l
		}
	}

	now := d.now()
	last := d.baseline(wf.Name, d.lastRunAt(wf.Name, now))
	fire, next, err := Due(wf.Schedule, loc, last, now, wf.Catchup)
	if err != nil {
		return err
	}
	d.Seed(wf.Name, next)
	if !fire {
		return nil
	}

	sum := sha256.Sum256(b)
	d.logf("cadence: launching %s", wf.Name)
	_, err = d.Launcher.Launch(ctx, wf, hex.EncodeToString(sum[:]))
	return err
}

// lastRunAt returns when this workflow last started, so a restarted daemon
// does not immediately re-fire a schedule that already ran.
func (d *Daemon) lastRunAt(name string, fallback time.Time) time.Time {
	runs, err := d.Store.ListRuns(name, 1)
	if err != nil || len(runs) == 0 {
		return fallback
	}
	return runs[0].StartedAt
}

// Run evaluates the directory on an interval until the context is cancelled.
func (d *Daemon) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			_ = d.Tick(ctx)
		}
	}
}

func workflowFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if ext := filepath.Ext(e.Name()); ext == ".yaml" || ext == ".yml" {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}
