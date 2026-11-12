package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hacKarim/cadence/internal/runner"
	"github.com/hacKarim/cadence/internal/store"
	"github.com/hacKarim/cadence/internal/template"
)

const fanoutWF = `
name: fan
steps:
  - id: screen
    run: echo screen
    outputs:
      items:
        from: stdout
        as: json
  - id: analyse
    needs: [screen]
    for_each: '{{ .steps.screen.outputs.items }}'
    agent:
      prompt: 'analyse {{ .item.ticker }}'
`

func TestForEachExpandsToItems(t *testing.T) {
	e, f, st := harness(t, map[string]runner.FakeResponse{
		"screen": {Stdout: `[{"ticker":"AAPL"},{"ticker":"MSFT"},{"ticker":"NVDA"}]`},
	})
	run, err := e.Run(context.Background(), parse(t, fanoutWF), "h")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.State != store.StateSucceeded {
		t.Fatalf("run state = %q (%s)", run.State, run.Error)
	}
	if f.CallCount("analyse") != 3 {
		t.Errorf("analyse ran %d times, want 3", f.CallCount("analyse"))
	}
	var prompts []string
	for _, c := range f.CallsFor("analyse") {
		prompts = append(prompts, c.Prompt)
	}
	joined := strings.Join(prompts, "|")
	for _, want := range []string{"AAPL", "MSFT", "NVDA"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no item prompt mentions %s: %q", want, joined)
		}
	}
	tree, _ := st.RunTree(run.ID)
	for _, s := range tree.Steps {
		if s.StepID == "analyse" && len(s.Items) != 3 {
			t.Errorf("stored %d items, want 3", len(s.Items))
		}
	}
}

// A for_each that resolves to something other than a list must fail the run
// with a message naming the step, never panic on a type assertion.
func TestForEachNonArrayIsRunError(t *testing.T) {
	for _, out := range []string{`{"a":1}`, `"scalar"`, `null`, `42`} {
		e, f, _ := harness(t, map[string]runner.FakeResponse{"screen": {Stdout: out}})
		run, err := e.Run(context.Background(), parse(t, fanoutWF), "h")
		if err != nil {
			t.Fatalf("Run returned a transport error for %s: %v", out, err)
		}
		if run.State != store.StateFailed {
			t.Errorf("for_each over %s gave state %q, want failed", out, run.State)
		}
		if !strings.Contains(run.Error, "analyse") {
			t.Errorf("error %q should name the step", run.Error)
		}
		if f.CallCount("analyse") != 0 {
			t.Errorf("analyse ran despite an unusable for_each")
		}
	}
}

func TestForEachEmptyArraySucceedsWithZeroItems(t *testing.T) {
	e, f, _ := harness(t, map[string]runner.FakeResponse{"screen": {Stdout: `[]`}})
	run, err := e.Run(context.Background(), parse(t, fanoutWF), "h")
	if err != nil {
		t.Fatal(err)
	}
	if run.State != store.StateSucceeded {
		t.Errorf("state = %q (%s), want succeeded", run.State, run.Error)
	}
	if f.CallCount("analyse") != 0 {
		t.Error("analyse ran for an empty list")
	}
}

func TestMaxParallelBoundIsRespected(t *testing.T) {
	e, f, _ := harness(t, map[string]runner.FakeResponse{
		"screen":  {Stdout: `[{"t":1},{"t":2},{"t":3},{"t":4},{"t":5},{"t":6},{"t":7},{"t":8}]`},
		"analyse": {Delay: 60 * time.Millisecond},
	})
	wf := parse(t, `
name: fan
steps:
  - id: screen
    run: echo s
    outputs:
      items: { from: stdout, as: json }
  - id: analyse
    needs: [screen]
    for_each: '{{ .steps.screen.outputs.items }}'
    max_parallel: 2
    agent:
      prompt: 'x {{ .item.t }}'
`)
	if _, err := e.Run(context.Background(), wf, "h"); err != nil {
		t.Fatal(err)
	}
	if f.Peak() > 2 {
		t.Errorf("peak concurrency %d, want at most 2", f.Peak())
	}
	if f.Peak() < 2 {
		t.Errorf("peak concurrency %d, want the bound to actually be used", f.Peak())
	}
}

func TestGlobalMaxParallelCapsAcrossSteps(t *testing.T) {
	e, f, _ := harness(t, map[string]runner.FakeResponse{
		"a": {Delay: 60 * time.Millisecond}, "b": {Delay: 60 * time.Millisecond},
		"c": {Delay: 60 * time.Millisecond}, "d": {Delay: 60 * time.Millisecond},
	})
	e.opt.MaxParallel = 2
	wf := parse(t, `
name: wide
steps:
  - id: a
    run: echo a
  - id: b
    run: echo b
  - id: c
    run: echo c
  - id: d
    run: echo d
`)
	if _, err := e.Run(context.Background(), wf, "h"); err != nil {
		t.Fatal(err)
	}
	if f.Peak() > 2 {
		t.Errorf("peak concurrency %d, want at most the global bound of 2", f.Peak())
	}
}

func TestIndependentStepsRunConcurrently(t *testing.T) {
	e, f, _ := harness(t, map[string]runner.FakeResponse{
		"a": {Delay: 60 * time.Millisecond}, "b": {Delay: 60 * time.Millisecond},
	})
	wf := parse(t, "name: w\nsteps:\n  - id: a\n    run: echo a\n  - id: b\n    run: echo b\n")
	if _, err := e.Run(context.Background(), wf, "h"); err != nil {
		t.Fatal(err)
	}
	if f.Peak() < 2 {
		t.Errorf("peak concurrency %d, want independent steps to overlap", f.Peak())
	}
}

func TestWhenItemSkipsNonMatchingItems(t *testing.T) {
	e, f, st := harness(t, map[string]runner.FakeResponse{
		"screen": {Stdout: `[{"v":"keep"},{"v":"drop"},{"v":"keep"}]`},
	})
	wf := parse(t, `
name: fan
steps:
  - id: screen
    run: echo s
    outputs:
      items: { from: stdout, as: json }
  - id: analyse
    needs: [screen]
    for_each: '{{ .steps.screen.outputs.items }}'
    when_item: '{{ eq .item.v "keep" }}'
    agent:
      prompt: 'x {{ .item.v }}'
`)
	run, err := e.Run(context.Background(), wf, "h")
	if err != nil {
		t.Fatal(err)
	}
	if run.State != store.StateSucceeded {
		t.Fatalf("state = %q (%s)", run.State, run.Error)
	}
	if f.CallCount("analyse") != 2 {
		t.Errorf("analyse ran %d times, want 2", f.CallCount("analyse"))
	}
	tree, _ := st.RunTree(run.ID)
	var skipped int
	for _, s := range tree.Steps {
		for _, it := range s.Items {
			if it.State == store.StateSkipped {
				skipped++
			}
		}
	}
	if skipped != 1 {
		t.Errorf("recorded %d skipped items, want 1", skipped)
	}
}

func TestIfSkipsWholeStepAndStillUnlocksDownstream(t *testing.T) {
	e, f, st := harness(t, nil)
	wf := parse(t, `
name: cond
steps:
  - id: a
    run: echo a
  - id: b
    needs: [a]
    if: '{{ eq .steps.a.result "never" }}'
    run: echo b
  - id: c
    needs: [b]
    run: echo c
`)
	run, err := e.Run(context.Background(), wf, "h")
	if err != nil {
		t.Fatal(err)
	}
	if run.State != store.StateSucceeded {
		t.Fatalf("state = %q (%s), want succeeded", run.State, run.Error)
	}
	if f.CallCount("b") != 0 {
		t.Error("b ran despite a false if")
	}
	if f.CallCount("c") != 1 {
		t.Error("c did not run: a skipped step must still unlock its dependents")
	}
	tree, _ := st.RunTree(run.ID)
	for _, s := range tree.Steps {
		if s.StepID == "b" && s.State != store.StateSkipped {
			t.Errorf("b = %q, want skipped", s.State)
		}
	}
}

func TestOnItemFailureContinueRunsAllItems(t *testing.T) {
	e, f, _ := harness(t, map[string]runner.FakeResponse{
		"screen":  {Stdout: `[{"t":1},{"t":2},{"t":3}]`},
		"analyse": {ExitCode: 1},
	})
	wf := parse(t, `
name: fan
steps:
  - id: screen
    run: echo s
    outputs:
      items: { from: stdout, as: json }
  - id: analyse
    needs: [screen]
    for_each: '{{ .steps.screen.outputs.items }}'
    on_item_failure: continue
    agent:
      prompt: 'x {{ .item.t }}'
`)
	run, _ := e.Run(context.Background(), wf, "h")
	if f.CallCount("analyse") != 3 {
		t.Errorf("ran %d items, want all 3 attempted", f.CallCount("analyse"))
	}
	if run.State != store.StateFailed {
		t.Errorf("state = %q, want failed: items failed", run.State)
	}
}

func TestOnItemFailureFailFastStopsEarly(t *testing.T) {
	e, f, _ := harness(t, map[string]runner.FakeResponse{
		"screen":  {Stdout: `[{"t":1},{"t":2},{"t":3},{"t":4},{"t":5},{"t":6}]`},
		"analyse": {ExitCode: 1, Delay: 30 * time.Millisecond},
	})
	wf := parse(t, `
name: fan
steps:
  - id: screen
    run: echo s
    outputs:
      items: { from: stdout, as: json }
  - id: analyse
    needs: [screen]
    for_each: '{{ .steps.screen.outputs.items }}'
    max_parallel: 1
    on_item_failure: fail_fast
    agent:
      prompt: 'x {{ .item.t }}'
`)
	run, _ := e.Run(context.Background(), wf, "h")
	if run.State != store.StateFailed {
		t.Errorf("state = %q, want failed", run.State)
	}
	if f.CallCount("analyse") >= 6 {
		t.Errorf("ran %d items, want fail_fast to stop before all 6", f.CallCount("analyse"))
	}
}

func TestDownstreamSeesFanOutItems(t *testing.T) {
	e, f, _ := harness(t, map[string]runner.FakeResponse{
		"screen":  {Stdout: `[{"t":"A"},{"t":"B"}]`},
		"analyse": {Stdout: "verdict"},
	})
	wf := parse(t, `
name: fan
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
  - id: challenge
    needs: [analyse]
    for_each: '{{ .steps.analyse.items }}'
    agent:
      prompt: 'challenge {{ .item.result }}'
`)
	run, err := e.Run(context.Background(), wf, "h")
	if err != nil || run.State != store.StateSucceeded {
		t.Fatalf("run = %v %q %s", err, run.State, run.Error)
	}
	if f.CallCount("challenge") != 2 {
		t.Errorf("challenge ran %d times, want 2", f.CallCount("challenge"))
	}
	for _, c := range f.CallsFor("challenge") {
		if !strings.Contains(c.Prompt, "verdict") {
			t.Errorf("challenge prompt %q should carry the analyst result", c.Prompt)
		}
	}
}

// Cancellation can land while the engine is still creating items. Recording
// the step as succeeded there is the worst possible outcome: --resume then
// inherits it and the work is silently never done.
func TestFanOutCancelledBeforeItemsStartIsNotSuccess(t *testing.T) {
	e, f, st := harness(t, nil)
	wf := parse(t, fanoutWF)

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
	rs.setView("screen", template.StepView{
		Outputs: map[string]any{"items": []any{
			map[string]any{"ticker": "A"}, map[string]any{"ticker": "B"},
		}},
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	step, _ := wf.Step("analyse")
	state, err := e.runStep(ctx, wf, step, rs)
	if err != nil {
		t.Fatalf("runStep: %v", err)
	}
	if f.CallCount("analyse") != 0 {
		t.Fatalf("items ran despite a cancelled context")
	}
	if state == store.StateSucceeded {
		t.Errorf("state = succeeded with zero items executed; --resume would inherit this step and skip the work")
	}
	if state != store.StateCancelled {
		t.Errorf("state = %q, want cancelled", state)
	}
	tree, _ := st.RunTree(run.ID)
	for _, s := range tree.Steps {
		for _, it := range s.Items {
			if it.State == store.StateSkipped && strings.Contains(it.Error, "earlier item failed") {
				t.Errorf("item %d blames a failure that never happened: %q", it.Index, it.Error)
			}
		}
	}
}
