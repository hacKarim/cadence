package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hacKarim/cadence/internal/runner"
	"github.com/hacKarim/cadence/internal/sandbox"
	"github.com/hacKarim/cadence/internal/spec"
	"github.com/hacKarim/cadence/internal/store"
	"github.com/hacKarim/cadence/internal/template"
)

func harness(t *testing.T, script map[string]runner.FakeResponse) (*Engine, *runner.Fake, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "cadence.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	f := runner.NewFake(script)
	e := New(st, map[string]runner.Runner{"shell": f, "agent": f}, sandbox.Confined{},
		Options{MaxParallel: 4, RunRoot: filepath.Join(dir, "runs")})
	return e, f, st
}

func parse(t *testing.T, y string) *spec.Workflow {
	t.Helper()
	wf, err := spec.Parse([]byte(y))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := wf.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return wf
}

func TestLinearWorkflowRunsInOrder(t *testing.T) {
	e, f, _ := harness(t, nil)
	wf := parse(t, `
name: linear
steps:
  - id: a
    run: echo a
  - id: b
    needs: [a]
    run: echo b
  - id: c
    needs: [b]
    run: echo c
`)
	run, err := e.Run(context.Background(), wf, "hash")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.State != store.StateSucceeded {
		t.Errorf("run state = %q, want succeeded", run.State)
	}
	if got := strings.Join(f.Order(), ","); got != "a,b,c" {
		t.Errorf("order = %q, want a,b,c", got)
	}
}

func TestStepOutputsReachDownstreamPrompt(t *testing.T) {
	e, f, _ := harness(t, map[string]runner.FakeResponse{"a": {Stdout: "THE-VALUE"}})
	wf := parse(t, `
name: flow
steps:
  - id: a
    run: echo a
  - id: b
    needs: [a]
    agent:
      prompt: 'upstream said {{ .steps.a.result }}'
`)
	if _, err := e.Run(context.Background(), wf, "h"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	calls := f.CallsFor("b")
	if len(calls) != 1 {
		t.Fatalf("step b ran %d times", len(calls))
	}
	if !strings.Contains(calls[0].Prompt, "THE-VALUE") {
		t.Errorf("prompt = %q, want the upstream result", calls[0].Prompt)
	}
}

func TestDeclaredOutputsReachDownstream(t *testing.T) {
	e, f, _ := harness(t, map[string]runner.FakeResponse{"a": {Stdout: `{"verdict":"undervalued"}`}})
	wf := parse(t, `
name: flow
steps:
  - id: a
    run: echo a
    outputs:
      verdict:
        from: stdout
        as: json
        select: "$.verdict"
  - id: b
    needs: [a]
    agent:
      prompt: 'verdict is {{ .steps.a.outputs.verdict }}'
`)
	if _, err := e.Run(context.Background(), wf, "h"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if p := f.CallsFor("b")[0].Prompt; !strings.Contains(p, "undervalued") {
		t.Errorf("prompt = %q", p)
	}
}

func TestFailedStepMarksDownstreamSkippedAndRunFailed(t *testing.T) {
	e, f, st := harness(t, map[string]runner.FakeResponse{"a": {ExitCode: 2, Stderr: "boom"}})
	wf := parse(t, `
name: flow
steps:
  - id: a
    run: echo a
  - id: b
    needs: [a]
    run: echo b
  - id: c
    needs: [b]
    run: echo c
`)
	run, err := e.Run(context.Background(), wf, "h")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.State != store.StateFailed {
		t.Errorf("run state = %q, want failed", run.State)
	}
	if f.CallCount("b") != 0 || f.CallCount("c") != 0 {
		t.Error("downstream steps ran despite an upstream failure")
	}
	tree, _ := st.RunTree(run.ID)
	states := map[string]store.State{}
	for _, s := range tree.Steps {
		states[s.StepID] = s.State
	}
	if states["a"] != store.StateFailed {
		t.Errorf("a = %q, want failed", states["a"])
	}
	for _, id := range []string{"b", "c"} {
		if states[id] != store.StateSkipped {
			t.Errorf("%s = %q, want skipped", id, states[id])
		}
	}
}

func TestRequireSandboxRefusesOnConfined(t *testing.T) {
	e, f, _ := harness(t, nil)
	wf := parse(t, `
name: flow
require_sandbox: true
steps:
  - id: a
    run: echo a
`)
	_, err := e.Run(context.Background(), wf, "h")
	if err == nil {
		t.Fatal("Run succeeded although the backend cannot enforce isolation")
	}
	if !strings.Contains(err.Error(), "confined") {
		t.Errorf("error %q should name the active backend", err)
	}
	if f.CallCount("a") != 0 {
		t.Error("a step ran despite require_sandbox being unsatisfiable")
	}
}

// Cancelling mid-run must not look like success, and must leave a run that can
// be resumed rather than one that silently half-finished.
func TestCancelMarksRunCancelled(t *testing.T) {
	e, _, st := harness(t, map[string]runner.FakeResponse{"slow": {Delay: 2 * time.Second}})
	wf := parse(t, `
name: flow
steps:
  - id: slow
    run: sleep 10
  - id: after
    needs: [slow]
    run: echo after
`)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()

	run, err := e.Run(ctx, wf, "h")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.State != store.StateCancelled {
		t.Errorf("run state = %q, want cancelled", run.State)
	}
	tree, _ := st.RunTree(run.ID)
	for _, s := range tree.Steps {
		if s.State == store.StateSucceeded {
			t.Errorf("step %s reported success during a cancelled run", s.StepID)
		}
	}
}

func TestRunDirectoryIsCreatedPerItem(t *testing.T) {
	e, _, st := harness(t, nil)
	wf := parse(t, "name: flow\nsteps:\n  - id: a\n    run: echo a\n")
	run, err := e.Run(context.Background(), wf, "h")
	if err != nil {
		t.Fatal(err)
	}
	tree, _ := st.RunTree(run.ID)
	jd := tree.Steps[0].Items[0].JailDir
	if jd == "" || !strings.Contains(jd, run.RunDir) {
		t.Errorf("item jail dir = %q, want one under %q", jd, run.RunDir)
	}
}

// The store is the single source of truth. If a write fails and the engine
// discards the error, the run reports a state whose steps were never recorded.
func TestStepFailsWhenTheStoreCannotRecordIt(t *testing.T) {
	e, f, st := harness(t, nil)
	wf := parse(t, "name: w\nsteps:\n  - id: a\n    run: echo a\n")

	run := &store.Run{
		Workflow: wf.Name, WorkflowHash: "h", State: store.StateRunning,
		Trigger: "manual", RunDir: t.TempDir(), StartedAt: time.Now(),
	}
	if err := st.CreateRun(run); err != nil {
		t.Fatal(err)
	}
	rs := &runState{
		run: run, views: map[string]template.StepView{},
		done: map[string]bool{}, failed: map[string]bool{},
		stepRows: map[string]*store.StepRun{},
	}
	st.Close() // every subsequent write now fails

	step, _ := wf.Step("a")
	state, err := e.runStep(context.Background(), wf, step, rs)
	if err == nil && state == store.StateSucceeded {
		t.Error("the step reported success although nothing about it could be recorded")
	}
	_ = f
}
