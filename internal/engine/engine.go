// Package engine drives a workflow run: it walks the dependency graph, builds
// an isolated jail for every step item, executes it through a runner, and
// records every state transition in the store.
package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hacKarim/cadence/internal/extract"
	"github.com/hacKarim/cadence/internal/graph"
	"github.com/hacKarim/cadence/internal/jail"
	"github.com/hacKarim/cadence/internal/runner"
	"github.com/hacKarim/cadence/internal/sandbox"
	"github.com/hacKarim/cadence/internal/spec"
	"github.com/hacKarim/cadence/internal/store"
	"github.com/hacKarim/cadence/internal/template"
)

// Options tunes one run.
type Options struct {
	MaxParallel int
	Force       bool
	ResumeFrom  int64
	RunRoot     string
	Trigger     string
}

// Engine executes workflows. Runners are injected by kind ("shell", "agent")
// so the engine never knows whether a step is a model call or a command.
type Engine struct {
	store   *store.Store
	runners map[string]runner.Runner
	sb      sandbox.Sandbox
	opt     Options
}

// New builds an engine.
func New(st *store.Store, runners map[string]runner.Runner, sb sandbox.Sandbox, opt Options) *Engine {
	if opt.MaxParallel <= 0 {
		opt.MaxParallel = 4
	}
	if opt.Trigger == "" {
		opt.Trigger = "manual"
	}
	return &Engine{store: st, runners: runners, sb: sb, opt: opt}
}

// Backend reports which sandbox backend this engine will confine steps with.
func (e *Engine) Backend() sandbox.Backend { return e.sb.Name() }

// runState carries the mutable bookkeeping of one run.
type runState struct {
	run      *store.Run
	views    map[string]template.StepView
	done     map[string]bool
	failed   map[string]bool
	stepRows map[string]*store.StepRun
}

// Run executes the workflow and returns the recorded run.
func (e *Engine) Run(ctx context.Context, wf *spec.Workflow, wfHash string) (*store.Run, error) {
	if wf.RequireSandbox {
		if err := sandbox.Require(e.sb); err != nil {
			return nil, err
		}
	}

	runDir := filepath.Join(e.opt.RunRoot, wf.Name, time.Now().Format("20060102-150405.000"))
	if err := os.MkdirAll(runDir, 0o750); err != nil {
		return nil, fmt.Errorf("create run directory: %w", err)
	}

	run := &store.Run{
		Workflow: wf.Name, WorkflowHash: wfHash, State: store.StateRunning,
		Trigger: e.opt.Trigger, RunDir: runDir, StartedAt: time.Now(),
	}
	if e.opt.ResumeFrom != 0 {
		from := e.opt.ResumeFrom
		run.ResumedFrom = &from
	}
	if err := e.store.CreateRun(run); err != nil {
		return nil, err
	}

	rs := &runState{
		run:      run,
		views:    map[string]template.StepView{},
		done:     map[string]bool{},
		failed:   map[string]bool{},
		stepRows: map[string]*store.StepRun{},
	}

	g := graph.New(wf.Steps)
	cancelled := false

	for {
		if ctx.Err() != nil {
			cancelled = true
			break
		}
		ready := g.Ready(rs.done, rs.failed)
		if len(ready) == 0 {
			break
		}
		for _, id := range ready {
			st, _ := wf.Step(id)
			state, err := e.runStep(ctx, wf, st, rs)
			if err != nil {
				return e.finish(run, store.StateFailed, err.Error()), nil
			}
			rs.done[id] = true
			if !state.OK() {
				rs.failed[id] = true
			}
			if state == store.StateCancelled {
				cancelled = true
			}
		}
		if cancelled {
			break
		}
	}

	e.markUnreached(wf, rs, cancelled)

	switch {
	case cancelled || ctx.Err() != nil:
		return e.finish(run, store.StateCancelled, "run cancelled"), nil
	case len(rs.failed) > 0:
		return e.finish(run, store.StateFailed, e.failureSummary(rs)), nil
	default:
		return e.finish(run, store.StateSucceeded, ""), nil
	}
}

// markUnreached records the steps that never became ready. A step blocked by a
// failure is skipped, which is a terminal state a reader can act on, rather
// than being left silently absent from the run.
func (e *Engine) markUnreached(wf *spec.Workflow, rs *runState, cancelled bool) {
	state := store.StateSkipped
	if cancelled {
		state = store.StateCancelled
	}
	for i := range wf.Steps {
		id := wf.Steps[i].ID
		if rs.done[id] {
			continue
		}
		row := &store.StepRun{RunID: rs.run.ID, StepID: id, State: state}
		if state == store.StateSkipped {
			row.Error = "skipped: an earlier step failed"
		}
		_ = e.store.CreateStepRun(row)
	}
}

func (e *Engine) failureSummary(rs *runState) string {
	for id := range rs.failed {
		if row, ok := rs.stepRows[id]; ok && row.Error != "" {
			return fmt.Sprintf("step %q failed: %s", id, row.Error)
		}
		return fmt.Sprintf("step %q failed", id)
	}
	return "run failed"
}

func (e *Engine) finish(run *store.Run, state store.State, msg string) *store.Run {
	end := time.Now()
	run.State, run.EndedAt, run.Error = state, &end, msg
	_ = e.store.UpdateRun(run)
	return run
}

// runStep executes one step. Task-level fan-out arrives in a later change; a
// plain step is simply a fan-out of exactly one item, so the item path below
// is the only execution path.
func (e *Engine) runStep(ctx context.Context, wf *spec.Workflow, st *spec.Step, rs *runState) (store.State, error) {
	start := time.Now()
	row := &store.StepRun{RunID: rs.run.ID, StepID: st.ID, State: store.StateRunning, StartedAt: &start}
	if err := e.store.CreateStepRun(row); err != nil {
		return store.StateFailed, err
	}
	rs.stepRows[st.ID] = row

	sc := template.Scope{Steps: rs.views}
	item, err := e.runItem(ctx, st, rs, sc, 0, nil)
	if err != nil {
		return e.finishStep(row, store.StateFailed, err.Error()), nil
	}

	end := time.Now()
	row.EndedAt = &end
	state := item.State
	if state == store.StateFailed {
		row.Error = item.Error
	}
	row.State = state
	_ = e.store.UpdateStepRun(row)

	rs.views[st.ID] = template.StepView{
		Result:  item.Result,
		Outputs: item.Outputs,
		Items:   []template.ItemView{{Index: 0, Result: item.Result, Outputs: item.Outputs}},
		Dir:     item.JailDir,
	}
	return state, nil
}

func (e *Engine) finishStep(row *store.StepRun, state store.State, msg string) store.State {
	end := time.Now()
	row.State, row.EndedAt, row.Error = state, &end, msg
	_ = e.store.UpdateStepRun(row)
	return state
}

// runItem executes a single step item inside its own jail.
func (e *Engine) runItem(ctx context.Context, st *spec.Step, rs *runState, sc template.Scope, index int, input any) (*store.StepItem, error) {
	j, err := jail.Create(rs.run.RunDir, st.ID, index)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	item := &store.StepItem{
		StepRunID: rs.stepRows[st.ID].ID, Index: index, State: store.StateRunning,
		JailDir: j.Out, StartedAt: &start,
	}
	if err := e.store.CreateItem(item); err != nil {
		return nil, err
	}

	fail := func(msg string) (*store.StepItem, error) {
		end := time.Now()
		item.State, item.Error, item.EndedAt = store.StateFailed, msg, &end
		_ = e.store.UpdateItem(item)
		return item, nil
	}

	if err := j.Stage(*st, sc); err != nil {
		return fail(err.Error())
	}
	env, err := j.Env(*st, os.Environ())
	if err != nil {
		return fail(err.Error())
	}

	req := runner.Request{Step: *st, Jail: j, Env: env, Timeout: st.Timeout}
	kind := "shell"
	if st.IsAgent() {
		kind = "agent"
		if req.Prompt, err = template.Render(st.Agent.Prompt, sc); err != nil {
			return fail(fmt.Sprintf("render prompt: %v", err))
		}
	} else {
		if req.Script, err = template.Render(st.Run, sc); err != nil {
			return fail(fmt.Sprintf("render command: %v", err))
		}
	}

	rn, ok := e.runners[kind]
	if !ok {
		return nil, fmt.Errorf("no runner registered for %q steps", kind)
	}
	res, err := rn.Run(ctx, req)
	if err != nil {
		return fail(err.Error())
	}

	end := time.Now()
	item.EndedAt = &end
	item.ExitCode = res.ExitCode
	item.InputTokens, item.OutputTokens, item.CostUSD = res.InputTokens, res.OutputTokens, res.CostUSD
	item.Excerpt = excerpt(res.Stdout)

	if ctx.Err() != nil {
		item.State, item.Error = store.StateCancelled, "cancelled"
		_ = e.store.UpdateItem(item)
		return item, nil
	}
	if res.TimedOut {
		item.State, item.Error = store.StateFailed, fmt.Sprintf("timed out after %s", st.Timeout)
		_ = e.store.UpdateItem(item)
		return item, nil
	}
	if res.ExitCode != 0 {
		item.State = store.StateFailed
		item.Error = fmt.Sprintf("exit status %d: %s", res.ExitCode, excerpt(res.Stderr))
		_ = e.store.UpdateItem(item)
		return item, nil
	}

	var ex *spec.Extract
	if st.Agent != nil {
		ex = st.Agent.Extract
	}
	primary, err := extract.Primary(res.Stdout, ex)
	if err != nil {
		return fail(err.Error())
	}
	item.Result = primary

	outs := map[string]any{}
	src := extract.Sources{Stdout: res.Stdout, Result: primary, Dir: j.Out}
	for name, o := range st.Outputs {
		v, err := extract.Output(o, src)
		if err != nil {
			return fail(fmt.Sprintf("output %q: %v", name, err))
		}
		outs[name] = v
	}
	item.Outputs = outs
	item.State = store.StateSucceeded
	_ = e.store.UpdateItem(item)
	return item, nil
}

const excerptBytes = 4096

func excerpt(s string) string {
	if len(s) <= excerptBytes {
		return s
	}
	return s[:excerptBytes] + "\n... truncated ..."
}
