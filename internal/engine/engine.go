// Package engine drives a workflow run: it walks the dependency graph, builds
// an isolated jail for every step item, executes it through a runner, and
// records every state transition in the store.
package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
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
	Clock       Clock
	Log         io.Writer
}

// Engine executes workflows. Runners are injected by kind ("shell", "agent")
// so the engine never knows whether a step is a model call or a command.
type Engine struct {
	store   *store.Store
	runners map[string]runner.Runner
	sb      sandbox.Sandbox
	opt     Options
	sem     chan struct{}
}

// New builds an engine.
func New(st *store.Store, runners map[string]runner.Runner, sb sandbox.Sandbox, opt Options) *Engine {
	if opt.MaxParallel <= 0 {
		opt.MaxParallel = 4
	}
	if opt.Trigger == "" {
		opt.Trigger = "manual"
	}
	if opt.Clock == nil {
		opt.Clock = realClock{}
	}
	return &Engine{store: st, runners: runners, sb: sb, opt: opt}
}

// Backend reports which sandbox backend this engine will confine steps with.
func (e *Engine) Backend() sandbox.Backend { return e.sb.Name() }

// logf reports something that went wrong but does not change the run's own
// outcome, such as a bookkeeping write for an already-terminal step.
func (e *Engine) logf(format string, args ...any) {
	if e.opt.Log != nil {
		fmt.Fprintf(e.opt.Log, "cadence: "+format+"\n", args...)
	}
}

// runState carries the mutable bookkeeping of one run. Ready steps execute
// concurrently, so every field here is guarded.
type runState struct {
	mu       sync.Mutex
	run      *store.Run
	views    map[string]template.StepView
	done     map[string]bool
	failed   map[string]bool
	stepRows map[string]*store.StepRun
	// inherited holds the items of steps that already succeeded in the run
	// being resumed. Those steps are not executed again.
	inherited map[string][]store.StepItem
}

// scope snapshots the completed steps a template may read.
func (rs *runState) scope() template.Scope {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	views := make(map[string]template.StepView, len(rs.views))
	for k, v := range rs.views {
		views[k] = v
	}
	return template.Scope{Steps: views}
}

func (rs *runState) setView(id string, v template.StepView) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.views[id] = v
}

func (rs *runState) stepRow(id string) *store.StepRun {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.stepRows[id]
}

func (rs *runState) setStepRow(id string, row *store.StepRun) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.stepRows[id] = row
}

// Run executes the workflow and returns the recorded run.
func (e *Engine) Run(ctx context.Context, wf *spec.Workflow, wfHash string) (*store.Run, error) {
	if wf.RequireSandbox {
		if err := sandbox.Require(e.sb); err != nil {
			return nil, err
		}
	}

	inherited, err := e.inheritable(wfHash)
	if err != nil {
		return nil, err
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
		run:       run,
		views:     map[string]template.StepView{},
		done:      map[string]bool{},
		failed:    map[string]bool{},
		stepRows:  map[string]*store.StepRun{},
		inherited: inherited,
	}

	// One global semaphore bounds how many step items execute at once, across
	// every step that happens to be ready together.
	e.sem = make(chan struct{}, e.opt.MaxParallel)

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
		type outcome struct {
			id    string
			state store.State
			err   error
		}
		results := make([]outcome, len(ready))
		var wg sync.WaitGroup
		for i, id := range ready {
			wg.Add(1)
			go func(i int, id string) {
				defer wg.Done()
				st, _ := wf.Step(id)
				state, err := e.runStep(ctx, wf, st, rs)
				results[i] = outcome{id: id, state: state, err: err}
			}(i, id)
		}
		wg.Wait()

		for _, o := range results {
			if o.err != nil {
				return e.finish(run, store.StateFailed, o.err.Error()), nil
			}
			rs.done[o.id] = true
			if !o.state.OK() {
				rs.failed[o.id] = true
			}
			if o.state == store.StateCancelled {
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
			row.Note = "skipped: an earlier step failed"
		}
		if err := e.store.CreateStepRun(row); err != nil {
			e.logf("could not record skipped step %q: %v", id, err)
		}
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

// inheritable loads the steps that already succeeded in the run being
// resumed. Resuming creates a NEW run that references those results, so the
// failed run stays on record exactly as it happened.
func (e *Engine) inheritable(wfHash string) (map[string][]store.StepItem, error) {
	if e.opt.ResumeFrom == 0 {
		return nil, nil
	}
	prior, err := e.store.GetRun(e.opt.ResumeFrom)
	if err != nil {
		return nil, fmt.Errorf("resume: run %d not found", e.opt.ResumeFrom)
	}
	if prior.WorkflowHash != wfHash {
		if !e.opt.Force {
			return nil, fmt.Errorf(
				"cannot resume run %d: the workflow file changed since it ran, so reusing its results would mix two definitions; re-run with --force to start over",
				prior.ID)
		}
		return nil, nil
	}
	if e.opt.Force {
		return nil, nil
	}
	tree, err := e.store.RunTree(prior.ID)
	if err != nil {
		return nil, err
	}
	out := map[string][]store.StepItem{}
	for _, s := range tree.Steps {
		if s.State == store.StateSucceeded || s.State == store.StateCached {
			out[s.StepID] = s.Items
		}
	}
	return out, nil
}

// inheritStep records a step whose results are carried over from the run being
// resumed, without executing anything.
func (e *Engine) inheritStep(rs *runState, st *spec.Step, items []store.StepItem) (store.State, error) {
	row, err := e.newStepRow(rs, st.ID, store.StateCached)
	if err != nil {
		return store.StateFailed, err
	}
	row.Note = fmt.Sprintf("inherited from run %d", e.opt.ResumeFrom)
	_ = e.store.UpdateStepRun(row)

	copies := make([]*store.StepItem, 0, len(items))
	for _, old := range items {
		now := time.Now()
		it := &store.StepItem{
			StepRunID: row.ID, Index: old.Index, State: store.StateCached,
			JailDir: old.JailDir, CacheKey: old.CacheKey, Result: old.Result,
			Excerpt: old.Excerpt, Outputs: old.Outputs,
			StartedAt: &now, EndedAt: &now,
		}
		_ = e.store.CreateItem(it)
		copies = append(copies, it)
	}
	rs.setView(st.ID, viewFor(copies))
	return store.StateCached, nil
}

// runStep executes one step: it gates on `if`, expands `for_each`, and runs
// every item in its own jail under the parallelism bounds.
func (e *Engine) runStep(ctx context.Context, wf *spec.Workflow, st *spec.Step, rs *runState) (store.State, error) {
	if items, ok := rs.inherited[st.ID]; ok {
		return e.inheritStep(rs, st, items)
	}

	sc := rs.scope()

	if st.If != "" {
		ok, evalErr := evalBool(st.If, sc)
		if evalErr != nil {
			row, err := e.newStepRow(rs, st.ID, store.StateRunning)
			if err != nil {
				return store.StateFailed, err
			}
			return e.finishStep(row, store.StateFailed, fmt.Sprintf("if: %v", evalErr)), nil
		}
		if !ok {
			row, err := e.newStepRow(rs, st.ID, store.StateSkipped)
			if err != nil {
				return store.StateFailed, err
			}
			row.Error = "skipped: the step's if condition was false"
			_ = e.store.UpdateStepRun(row)
			rs.setView(st.ID, template.StepView{})
			return store.StateSkipped, nil
		}
	}

	row, err := e.newStepRow(rs, st.ID, store.StateRunning)
	if err != nil {
		return store.StateFailed, err
	}

	items, err := e.expand(st, sc)
	if err != nil {
		return e.finishStep(row, store.StateFailed, err.Error()), nil
	}

	results := make([]*store.StepItem, len(items))
	var stop atomic.Bool
	var wg sync.WaitGroup
	perStep := make(chan struct{}, perStepBound(st, e.opt.MaxParallel))

	for i, input := range items {
		// Cancellation and fail-fast both stop the spawn loop, but they are
		// not the same outcome. Recording a cancelled step as succeeded would
		// let --resume inherit it and skip the work for good.
		if ctx.Err() != nil {
			results[i] = e.recordItem(rs, st, i, store.StateCancelled, "cancelled before it started")
			continue
		}
		if stop.Load() {
			results[i] = e.recordSkippedItem(rs, st, i, "not started: an earlier item failed")
			continue
		}
		wg.Add(1)
		go func(i int, input any) {
			defer wg.Done()

			itemScope := sc
			itemScope.Item = input
			if st.WhenItem != "" {
				keep, err := evalBool(st.WhenItem, itemScope)
				if err != nil {
					results[i] = e.recordFailedItem(rs, st, i, fmt.Sprintf("when_item: %v", err))
					return
				}
				if !keep {
					results[i] = e.recordSkippedItem(rs, st, i, "skipped: when_item was false")
					return
				}
			}

			perStep <- struct{}{}
			defer func() { <-perStep }()
			e.sem <- struct{}{}
			defer func() { <-e.sem }()

			if ctx.Err() != nil {
				results[i] = e.recordItem(rs, st, i, store.StateCancelled, "cancelled before it started")
				return
			}
			if stop.Load() {
				results[i] = e.recordSkippedItem(rs, st, i, "not started: an earlier item failed")
				return
			}

			it, err := e.runItem(ctx, st, rs, itemScope, i, input)
			if err != nil {
				results[i] = e.recordFailedItem(rs, st, i, err.Error())
				return
			}
			results[i] = it
			if it.State == store.StateFailed && st.OnItemFailure == spec.ItemFailureFailFast {
				stop.Store(true)
			}
		}(i, input)
	}
	wg.Wait()

	state, msg := aggregate(results)
	e.finishStep(row, state, msg)
	rs.setView(st.ID, viewFor(results))
	return state, nil
}

func perStepBound(st *spec.Step, global int) int {
	if st.MaxParallel > 0 {
		return st.MaxParallel
	}
	return global
}

// newStepRow records a step. A failure here is fatal to the step: the store
// is the run's only record, and a step whose row does not exist would report a
// state nobody can see.
func (e *Engine) newStepRow(rs *runState, stepID string, state store.State) (*store.StepRun, error) {
	start := time.Now()
	row := &store.StepRun{RunID: rs.run.ID, StepID: stepID, State: state, StartedAt: &start}
	if err := e.store.CreateStepRun(row); err != nil {
		return nil, fmt.Errorf("record step %q: %w", stepID, err)
	}
	rs.setStepRow(stepID, row)
	return row, nil
}

func (e *Engine) recordItem(rs *runState, st *spec.Step, index int, state store.State, msg string) *store.StepItem {
	now := time.Now()
	it := &store.StepItem{
		StepRunID: rs.stepRow(st.ID).ID, Index: index, State: state,
		Error: msg, StartedAt: &now, EndedAt: &now,
	}
	if err := e.store.CreateItem(it); err != nil {
		// The item is still returned so aggregation sees the outcome, but it
		// is downgraded to a failure: nothing about it was recorded.
		it.State, it.Error = store.StateFailed, fmt.Sprintf("could not record item %d: %v", index, err)
	}
	return it
}

func (e *Engine) recordSkippedItem(rs *runState, st *spec.Step, index int, msg string) *store.StepItem {
	return e.recordItem(rs, st, index, store.StateSkipped, msg)
}

func (e *Engine) recordFailedItem(rs *runState, st *spec.Step, index int, msg string) *store.StepItem {
	return e.recordItem(rs, st, index, store.StateFailed, msg)
}

// aggregate reduces item states to the step's state. A step is only
// successful when nothing inside it failed or was cancelled.
func aggregate(items []*store.StepItem) (store.State, string) {
	var firstErr string
	failed, cancelled := 0, 0
	for _, it := range items {
		if it == nil {
			continue
		}
		switch it.State {
		case store.StateFailed:
			failed++
			if firstErr == "" {
				firstErr = it.Error
			}
		case store.StateCancelled:
			cancelled++
		}
	}
	switch {
	case cancelled > 0:
		return store.StateCancelled, "cancelled"
	case failed > 0:
		return store.StateFailed, fmt.Sprintf("%d of %d items failed: %s", failed, len(items), firstErr)
	default:
		return store.StateSucceeded, ""
	}
}

// viewFor builds what downstream steps may read about a finished step. Only
// items that actually produced a result appear in Items, so a for_each over
// them does not have to filter out skipped ones.
func viewFor(items []*store.StepItem) template.StepView {
	v := template.StepView{}
	for _, it := range items {
		if it == nil || !it.State.OK() || it.State == store.StateSkipped {
			continue
		}
		v.Items = append(v.Items, template.ItemView{
			Index: it.Index, Result: it.Result, Outputs: it.Outputs,
		})
	}
	if len(v.Items) == 1 {
		v.Result = v.Items[0].Result
		v.Outputs = v.Items[0].Outputs
	}
	for _, it := range items {
		if it != nil && it.JailDir != "" {
			v.Dir = it.JailDir
			break
		}
	}
	return v
}

func (e *Engine) finishStep(row *store.StepRun, state store.State, msg string) store.State {
	end := time.Now()
	row.State, row.EndedAt, row.Error = state, &end, msg
	_ = e.store.UpdateStepRun(row)
	return state
}

// runItem executes a single step item inside its own jail: stage, consult the
// cache, run with retries, then extract.
func (e *Engine) runItem(ctx context.Context, st *spec.Step, rs *runState, sc template.Scope, index int, input any) (*store.StepItem, error) {
	j, err := jail.Create(rs.run.RunDir, st.ID, index)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	item := &store.StepItem{
		StepRunID: rs.stepRow(st.ID).ID, Index: index, State: store.StateRunning,
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
	env, err := j.Env(*st, sc, os.Environ())
	if err != nil {
		return fail(err.Error())
	}

	req := runner.Request{Step: *st, Jail: j, Env: env, Timeout: st.Timeout}
	kind := "shell"
	rendered := ""
	if st.IsAgent() {
		kind = "agent"
		if req.Prompt, err = template.Render(st.Agent.Prompt, sc); err != nil {
			return fail(fmt.Sprintf("render prompt: %v", err))
		}
		rendered = req.Prompt
	} else {
		if req.Script, err = template.Render(st.Run, sc); err != nil {
			return fail(fmt.Sprintf("render command: %v", err))
		}
		rendered = req.Script
	}

	rn, ok := e.runners[kind]
	if !ok {
		return nil, fmt.Errorf("no runner registered for %q steps", kind)
	}

	digest, err := stagedDigest(j.Work)
	if err != nil {
		return fail(fmt.Sprintf("hash staged inputs: %v", err))
	}
	// Environment values derived from the current item are part of what this
	// item was given, so they belong in the key. Static values do not: they
	// are where secrets live, and rotating one must not invalidate the cache.
	renderedEnv, err := j.RenderedEnv(*st, sc)
	if err != nil {
		return fail(fmt.Sprintf("render env: %v", err))
	}
	digest = mixEnv(digest, renderedEnv)
	item.CacheKey = store.CacheKey(*st, rendered, digest, rs.run.WorkflowHash, st.Cache, e.opt.Clock.Now())

	if item.CacheKey != "" && !e.opt.Force {
		if entry, hit, err := e.store.LookupCache(item.CacheKey); err == nil && hit {
			if err := store.RestoreCache(entry, j); err != nil {
				return fail(fmt.Sprintf("restore cached artifacts: %v", err))
			}
			end := time.Now()
			item.State, item.Result, item.Outputs, item.EndedAt = store.StateCached, entry.Result, entry.Outputs, &end
			item.Excerpt = excerpt(entry.Result)
			_ = e.store.UpdateItem(item)
			return item, nil
		}
	}

	res, runErr := e.attempt(ctx, rn, req, st, item)
	if runErr != nil {
		return fail(runErr.Error())
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
	// Extraction runs once, outside the retry loop: the same output would
	// fail to parse every time, so retrying only spends another model call.
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

	if item.CacheKey != "" {
		_ = e.store.SaveCache(item.CacheKey, st.ID, j, outs, primary)
	}
	return item, nil
}

// attempt runs the request, retrying execution failures with backoff. Retries
// are per item: a step fanning out over forty candidates gives each of them
// its own budget.
func (e *Engine) attempt(ctx context.Context, rn runner.Runner, req runner.Request, st *spec.Step, item *store.StepItem) (*runner.Result, error) {
	attempts := st.Retries + 1
	if attempts < 1 {
		attempts = 1
	}
	var base, max time.Duration
	var factor float64
	if st.Backoff != nil {
		base, factor, max = st.Backoff.Base, st.Backoff.Factor, st.Backoff.Max
	}

	var res *runner.Result
	var lastErr error
	for n := 0; n < attempts; n++ {
		item.Attempt = n
		res, lastErr = rn.Run(ctx, req)
		if lastErr == nil && res != nil && res.ExitCode == 0 && !res.TimedOut {
			return res, nil
		}
		if ctx.Err() != nil {
			break
		}
		if n < attempts-1 {
			e.opt.Clock.Sleep(backoffFor(base, factor, max, n))
		}
	}
	if res == nil {
		if lastErr == nil {
			lastErr = fmt.Errorf("runner returned no result")
		}
		return nil, lastErr
	}
	return res, nil
}

// stagedDigest hashes the staged input tree so the cache key changes whenever
// what the step was given changes.
func stagedDigest(dir string) (string, error) {
	h := sha256.New()
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00", rel)
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(h, f)
		return err
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// mixEnv folds templated environment values into the staged digest in a
// stable order.
func mixEnv(digest string, env map[string]string) string {
	if len(env) == 0 {
		return digest
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	fmt.Fprint(h, digest)
	for _, k := range keys {
		fmt.Fprintf(h, "\x00%s=%s", k, env[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

const excerptBytes = 4096

func excerpt(s string) string {
	if len(s) <= excerptBytes {
		return s
	}
	return s[:excerptBytes] + "\n... truncated ..."
}
