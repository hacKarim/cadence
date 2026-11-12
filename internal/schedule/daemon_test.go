package schedule

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hacKarim/cadence/internal/spec"
	"github.com/hacKarim/cadence/internal/store"
)

type fakeExec struct {
	mu      sync.Mutex
	calls   int
	hold    chan struct{}
	started chan struct{}
}

func newFakeExec() *fakeExec {
	return &fakeExec{started: make(chan struct{}, 16)}
}

func (f *fakeExec) Execute(ctx context.Context, wf *spec.Workflow, hash string) error {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	f.started <- struct{}{}
	if f.hold != nil {
		<-f.hold
	}
	return nil
}

func (f *fakeExec) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func wf(t *testing.T, body string) *spec.Workflow {
	t.Helper()
	w, err := spec.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Validate(); err != nil {
		t.Fatal(err)
	}
	return w
}

const simpleWF = "name: nightly\nsteps:\n  - id: a\n    run: /bin/true\n"

// launchAsync starts a launch and reports when it has finished, so a test
// never returns while a goroutine is still writing into its temp directory.
func launchAsync(l *Launcher, w *spec.Workflow) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		l.Launch(context.Background(), w, "h")
	}()
	return done
}

func TestConcurrencyAllowRunsBoth(t *testing.T) {
	st := openStore(t)
	ex := newFakeExec()
	ex.hold = make(chan struct{})
	l := &Launcher{Store: st, Exec: ex}
	w := wf(t, simpleWF)
	w.Concurrency = spec.ConcurrencyAllow

	first := launchAsync(l, w)
	<-ex.started
	second := launchAsync(l, w)
	<-ex.started
	close(ex.hold)
	<-first
	<-second

	if ex.count() != 2 {
		t.Errorf("ran %d times, want 2 with concurrency: allow", ex.count())
	}
}

func TestConcurrencySkipRecordsSkippedRun(t *testing.T) {
	st := openStore(t)
	ex := newFakeExec()
	ex.hold = make(chan struct{})
	l := &Launcher{Store: st, Exec: ex}
	w := wf(t, simpleWF)
	w.Concurrency = spec.ConcurrencySkip

	first := launchAsync(l, w)
	<-ex.started

	launched, err := l.Launch(context.Background(), w, "h")
	if err != nil {
		t.Fatal(err)
	}
	if launched {
		t.Error("second launch ran while the first still held the lock")
	}
	close(ex.hold)
	<-first

	runs, _ := st.ListRuns("nightly", 10)
	var skipped int
	for _, r := range runs {
		if r.State == store.StateSkipped {
			skipped++
			if r.Error == "" {
				t.Error("skipped run records no reason")
			}
		}
	}
	if skipped != 1 {
		t.Errorf("recorded %d skipped runs, want 1: a skip must leave a trace", skipped)
	}
}

func TestConcurrencyQueueRunsAfterLockFrees(t *testing.T) {
	st := openStore(t)
	ex := newFakeExec()
	ex.hold = make(chan struct{})
	l := &Launcher{Store: st, Exec: ex, PollInterval: 5 * time.Millisecond}
	w := wf(t, simpleWF)
	w.Concurrency = spec.ConcurrencyQueue

	first := launchAsync(l, w)
	<-ex.started

	done := make(chan bool, 1)
	go func() {
		launched, _ := l.Launch(context.Background(), w, "h")
		done <- launched
	}()

	select {
	case <-ex.started:
		t.Fatal("queued launch ran before the lock was free")
	case <-time.After(40 * time.Millisecond):
	}

	close(ex.hold)
	<-first
	select {
	case launched := <-done:
		if !launched {
			t.Error("queued launch never ran")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued launch timed out")
	}
}

func TestLockIsReleasedAfterRun(t *testing.T) {
	st := openStore(t)
	l := &Launcher{Store: st, Exec: newFakeExec()}
	w := wf(t, simpleWF)
	if _, err := l.Launch(context.Background(), w, "h"); err != nil {
		t.Fatal(err)
	}
	ok, err := st.AcquireWorkflowLock("nightly", 1)
	if err != nil || !ok {
		t.Error("the workflow lock was not released after the run finished")
	}
}

func TestDaemonTriggersOnScheduleOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	body := "name: nightly\nschedule: \"0 0 7 * * *\"\ntimezone: Europe/Paris\nsteps:\n  - id: a\n    run: /bin/true\n"
	if err := os.WriteFile(filepath.Join(dir, "nightly.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	st := openStore(t)
	ex := newFakeExec()
	now := at("2026-03-05 07:00:01")
	d := &Daemon{
		Dir:      dir,
		Store:    st,
		Launcher: &Launcher{Store: st, Exec: ex},
		Now:      func() time.Time { return now },
	}
	d.Seed("nightly", at("2026-03-04 07:00:00"))

	if err := d.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	d.Wait()
	if ex.count() != 1 {
		t.Fatalf("ran %d times on the first tick, want 1", ex.count())
	}
	if err := d.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	d.Wait()
	if ex.count() != 1 {
		t.Errorf("ran %d times after a second tick at the same instant, want 1", ex.count())
	}
}

func TestDaemonIgnoresWorkflowsWithoutSchedule(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "manual.yaml"), []byte(simpleWF), 0o600)
	st := openStore(t)
	ex := newFakeExec()
	d := &Daemon{Dir: dir, Store: st, Launcher: &Launcher{Store: st, Exec: ex}, Now: time.Now}
	if err := d.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	d.Wait()
	if ex.count() != 0 {
		t.Errorf("ran an unscheduled workflow %d times", ex.count())
	}
}

// One broken file in the directory must not stop every other schedule.
func TestDaemonReportsBadFileAndKeepsGoing(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte("name: x\nsteps: [ {id: 'bad id'} ]\n"), 0o600)
	good := "name: nightly\nschedule: \"0 0 7 * * *\"\nsteps:\n  - id: a\n    run: /bin/true\n"
	os.WriteFile(filepath.Join(dir, "good.yaml"), []byte(good), 0o600)

	st := openStore(t)
	ex := newFakeExec()
	now := at("2026-03-05 07:00:01")
	d := &Daemon{Dir: dir, Store: st, Launcher: &Launcher{Store: st, Exec: ex}, Now: func() time.Time { return now }}
	d.Seed("nightly", at("2026-03-04 07:00:00"))

	err := d.Tick(context.Background())
	if err == nil {
		t.Error("Tick did not report the broken workflow file")
	}
	d.Wait()
	if ex.count() != 1 {
		t.Errorf("the good workflow ran %d times, want 1 despite the broken neighbour", ex.count())
	}
}

// A workflow with a long agent step must not stop every other schedule from
// even being evaluated.
func TestOneSlowWorkflowDoesNotBlockTheOthers(t *testing.T) {
	dir := t.TempDir()
	slow := "name: slow\nschedule: \"0 0 7 * * *\"\nsteps:\n  - id: a\n    run: /bin/true\n"
	quick := "name: quick\nschedule: \"0 0 7 * * *\"\nsteps:\n  - id: a\n    run: /bin/true\n"
	os.WriteFile(filepath.Join(dir, "a-slow.yaml"), []byte(slow), 0o600)
	os.WriteFile(filepath.Join(dir, "b-quick.yaml"), []byte(quick), 0o600)

	st := openStore(t)
	ex := newFakeExec()
	ex.hold = make(chan struct{})
	now := at("2026-03-05 07:00:01")
	d := &Daemon{
		Dir: dir, Store: st,
		Launcher: &Launcher{Store: st, Exec: ex},
		Now:      func() time.Time { return now },
	}
	d.Seed("slow", at("2026-03-04 07:00:00"))
	d.Seed("quick", at("2026-03-04 07:00:00"))

	if err := d.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(3 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case <-ex.started:
		case <-deadline:
			close(ex.hold)
			d.Wait()
			t.Fatalf("only %d of 2 workflows started: a slow run blocked the scheduler", i)
		}
	}
	close(ex.hold)
	d.Wait()
}

// Tick must not stack a second launch of a workflow whose previous launch is
// still going, or a slow workflow accumulates goroutines every interval.
func TestTickDoesNotStackLaunchesOfTheSameWorkflow(t *testing.T) {
	dir := t.TempDir()
	body := "name: nightly\nschedule: \"* * * * * *\"\nconcurrency: allow\nsteps:\n  - id: a\n    run: /bin/true\n"
	os.WriteFile(filepath.Join(dir, "n.yaml"), []byte(body), 0o600)

	st := openStore(t)
	ex := newFakeExec()
	ex.hold = make(chan struct{})
	base := at("2026-03-05 07:00:00")
	n := 0
	d := &Daemon{
		Dir: dir, Store: st,
		Launcher: &Launcher{Store: st, Exec: ex},
		// One boundary per tick, so each tick is genuinely due rather than
		// being a missed slot that catchup: false correctly drops.
		Now: func() time.Time { n++; return base.Add(time.Duration(n) * time.Second) },
	}
	d.Seed("nightly", base)

	for i := 0; i < 4; i++ {
		if err := d.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	<-ex.started
	time.Sleep(100 * time.Millisecond)
	if got := ex.count(); got != 1 {
		t.Errorf("launched %d times while one launch was still in flight, want 1", got)
	}
	close(ex.hold)
	d.Wait()
}
