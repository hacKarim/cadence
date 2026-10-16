package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hacKarim/cadence/internal/runner"
	"github.com/hacKarim/cadence/internal/sandbox"
	"github.com/hacKarim/cadence/internal/store"
)

// fakeClock records what the engine would have slept, so backoff is asserted
// without the suite actually waiting.
type fakeClock struct{ slept []time.Duration }

func (c *fakeClock) Now() time.Time        { return time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC) }
func (c *fakeClock) Sleep(d time.Duration) { c.slept = append(c.slept, d) }

func harnessClock(t *testing.T, script map[string]runner.FakeResponse) (*Engine, *runner.Fake, *store.Store, *fakeClock) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "cadence.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	f := runner.NewFake(script)
	c := &fakeClock{}
	e := New(st, map[string]runner.Runner{"shell": f, "agent": f}, sandbox.Confined{},
		Options{MaxParallel: 4, RunRoot: filepath.Join(dir, "runs"), Clock: c})
	return e, f, st, c
}

func TestItemRetriesUpToLimit(t *testing.T) {
	e, f, _, _ := harnessClock(t, map[string]runner.FakeResponse{"a": {FailTimes: 2, ExitCode: 1}})
	wf := parse(t, "name: w\ndefaults:\n  retries: 2\nsteps:\n  - id: a\n    run: echo a\n")
	run, err := e.Run(context.Background(), wf, "h")
	if err != nil {
		t.Fatal(err)
	}
	if run.State != store.StateSucceeded {
		t.Errorf("state = %q (%s), want succeeded on the third attempt", run.State, run.Error)
	}
	if f.CallCount("a") != 3 {
		t.Errorf("ran %d times, want 3 (one attempt plus two retries)", f.CallCount("a"))
	}
}

func TestRetriesGiveUpAfterLimit(t *testing.T) {
	e, f, _, _ := harnessClock(t, map[string]runner.FakeResponse{"a": {FailTimes: 9, ExitCode: 1}})
	wf := parse(t, "name: w\ndefaults:\n  retries: 1\nsteps:\n  - id: a\n    run: echo a\n")
	run, _ := e.Run(context.Background(), wf, "h")
	if run.State != store.StateFailed {
		t.Errorf("state = %q, want failed", run.State)
	}
	if f.CallCount("a") != 2 {
		t.Errorf("ran %d times, want 2", f.CallCount("a"))
	}
}

// retries: 1 on a three-item step means each item may retry once, not that the
// step as a whole gets one more go.
func TestRetriesArePerItemNotPerStep(t *testing.T) {
	e, f, _, _ := harnessClock(t, map[string]runner.FakeResponse{
		"screen":  {Stdout: `[{"t":1},{"t":2},{"t":3}]`},
		"analyse": {FailTimes: 9, ExitCode: 1},
	})
	wf := parse(t, `
name: w
defaults:
  retries: 1
steps:
  - id: screen
    run: echo s
    outputs:
      items: { from: stdout, as: json }
  - id: analyse
    needs: [screen]
    for_each: '{{ .steps.screen.outputs.items }}'
    agent:
      prompt: 'x {{ .item.t }}'
`)
	e.Run(context.Background(), wf, "h")
	if got := f.CallCount("analyse"); got != 6 {
		t.Errorf("analyse ran %d times, want 6 (3 items x 2 attempts)", got)
	}
}

func TestBackoffDelaysGrow(t *testing.T) {
	e, _, _, c := harnessClock(t, map[string]runner.FakeResponse{"a": {FailTimes: 9, ExitCode: 1}})
	wf := parse(t, `
name: w
defaults:
  retries: 3
  backoff: { base: 2s, factor: 2, max: 5s }
steps:
  - id: a
    run: echo a
`)
	e.Run(context.Background(), wf, "h")
	want := []time.Duration{2 * time.Second, 4 * time.Second, 5 * time.Second}
	if len(c.slept) != len(want) {
		t.Fatalf("slept %v, want %v", c.slept, want)
	}
	for i := range want {
		if c.slept[i] != want[i] {
			t.Errorf("sleep %d = %v, want %v (capped at max)", i, c.slept[i], want[i])
		}
	}
}

func TestTimeoutMarksItemFailed(t *testing.T) {
	e, _, st, _ := harnessClock(t, map[string]runner.FakeResponse{"a": {Delay: 2 * time.Second}})
	wf := parse(t, "name: w\nsteps:\n  - id: a\n    timeout: 100ms\n    run: sleep 5\n")
	run, _ := e.Run(context.Background(), wf, "h")
	if run.State != store.StateFailed {
		t.Errorf("state = %q, want failed", run.State)
	}
	tree, _ := st.RunTree(run.ID)
	if !strings.Contains(tree.Steps[0].Items[0].Error, "timed out") {
		t.Errorf("item error = %q, want a timeout message", tree.Steps[0].Items[0].Error)
	}
}

// An extraction failure is deterministic: rerunning the same agent output
// would fail identically, so a retry would only waste a model call.
func TestExtractionFailureIsNotRetried(t *testing.T) {
	e, f, _, _ := harnessClock(t, map[string]runner.FakeResponse{"a": {Stdout: "no marker in here"}})
	wf := parse(t, `
name: w
defaults:
  retries: 3
steps:
  - id: a
    agent:
      prompt: go
      extract: { after: "=====REPORT=====" }
`)
	run, _ := e.Run(context.Background(), wf, "h")
	if run.State != store.StateFailed {
		t.Errorf("state = %q, want failed", run.State)
	}
	if f.CallCount("a") != 1 {
		t.Errorf("ran %d times, want 1: extraction failures are not retried", f.CallCount("a"))
	}
}

func TestOutputExtractionFailureIsNotRetried(t *testing.T) {
	e, f, _, _ := harnessClock(t, map[string]runner.FakeResponse{"a": {Stdout: "not json"}})
	wf := parse(t, `
name: w
defaults:
  retries: 3
steps:
  - id: a
    run: echo a
    outputs:
      v: { from: stdout, as: json, select: "$.v" }
`)
	e.Run(context.Background(), wf, "h")
	if f.CallCount("a") != 1 {
		t.Errorf("ran %d times, want 1", f.CallCount("a"))
	}
}

const cacheWF = `
name: cached
steps:
  - id: a
    cache: always
    agent:
      prompt: analyse
`

func TestCacheHitSkipsRunner(t *testing.T) {
	e, f, st, _ := harnessClock(t, map[string]runner.FakeResponse{"a": {Stdout: "the result"}})
	wf := parse(t, cacheWF)
	if _, err := e.Run(context.Background(), wf, "h"); err != nil {
		t.Fatal(err)
	}
	if f.CallCount("a") != 1 {
		t.Fatalf("first run made %d calls", f.CallCount("a"))
	}
	run2, err := e.Run(context.Background(), wf, "h")
	if err != nil {
		t.Fatal(err)
	}
	if f.CallCount("a") != 1 {
		t.Errorf("second run made %d calls in total, want the cache to serve it", f.CallCount("a"))
	}
	if run2.State != store.StateSucceeded {
		t.Errorf("cached run state = %q (%s)", run2.State, run2.Error)
	}
	tree, _ := st.RunTree(run2.ID)
	it := tree.Steps[0].Items[0]
	if it.State != store.StateCached {
		t.Errorf("item state = %q, want cached", it.State)
	}
	if it.Result != "the result" {
		t.Errorf("cached result = %q, want the original", it.Result)
	}
}

func TestCacheMissWhenWorkflowHashChanges(t *testing.T) {
	e, f, _, _ := harnessClock(t, map[string]runner.FakeResponse{"a": {Stdout: "r"}})
	wf := parse(t, cacheWF)
	e.Run(context.Background(), wf, "hash-one")
	e.Run(context.Background(), wf, "hash-two")
	if f.CallCount("a") != 2 {
		t.Errorf("made %d calls, want 2: a changed workflow must not reuse the cache", f.CallCount("a"))
	}
}

func TestForceIgnoresCache(t *testing.T) {
	e, f, _, _ := harnessClock(t, map[string]runner.FakeResponse{"a": {Stdout: "r"}})
	wf := parse(t, cacheWF)
	e.Run(context.Background(), wf, "h")
	e.opt.Force = true
	e.Run(context.Background(), wf, "h")
	if f.CallCount("a") != 2 {
		t.Errorf("made %d calls, want 2 with --force", f.CallCount("a"))
	}
}

func TestNeverCachedByDefault(t *testing.T) {
	e, f, _, _ := harnessClock(t, map[string]runner.FakeResponse{"a": {Stdout: "r"}})
	wf := parse(t, "name: w\nsteps:\n  - id: a\n    run: echo a\n")
	e.Run(context.Background(), wf, "h")
	e.Run(context.Background(), wf, "h")
	if f.CallCount("a") != 2 {
		t.Errorf("made %d calls, want 2: caching is opt-in", f.CallCount("a"))
	}
}
