package runner

import (
	"context"
	"sync"
	"time"
)

// FakeResponse scripts what the fake runner does for one step.
type FakeResponse struct {
	Stdout    string
	Stderr    string
	ExitCode  int
	Err       error
	Delay     time.Duration
	FailTimes int // fail this many calls before succeeding
}

// Fake is a Runner for tests: it records every call, scripts outcomes per
// step, and tracks how many calls overlapped so parallelism bounds can be
// asserted on real behaviour rather than on a mock's call log.
type Fake struct {
	mu       sync.Mutex
	script   map[string]FakeResponse
	calls    []Request
	counts   map[string]int
	inflight int
	peak     int
}

// NewFake builds a fake runner from a per-step script.
func NewFake(script map[string]FakeResponse) *Fake {
	if script == nil {
		script = map[string]FakeResponse{}
	}
	return &Fake{script: script, counts: map[string]int{}}
}

// Run records the call and returns the scripted result.
func (f *Fake) Run(ctx context.Context, r Request) (*Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, r)
	n := f.counts[r.Step.ID]
	f.counts[r.Step.ID] = n + 1
	resp := f.script[r.Step.ID]
	f.inflight++
	if f.inflight > f.peak {
		f.peak = f.inflight
	}
	f.mu.Unlock()

	defer func() {
		f.mu.Lock()
		f.inflight--
		f.mu.Unlock()
	}()

	if resp.Delay > 0 {
		select {
		case <-time.After(resp.Delay):
		case <-ctx.Done():
			return &Result{TimedOut: true, ExitCode: -1}, nil
		}
	}
	if resp.Err != nil {
		return nil, resp.Err
	}
	// FailTimes means "fail the first N calls, then succeed", so once the
	// budget is spent the scripted failure code no longer applies.
	if resp.FailTimes > 0 {
		if n < resp.FailTimes {
			code := resp.ExitCode
			if code == 0 {
				code = 1
			}
			return &Result{Stdout: resp.Stdout, Stderr: resp.Stderr, ExitCode: code}, nil
		}
		return &Result{Stdout: resp.Stdout, Stderr: resp.Stderr, ExitCode: 0}, nil
	}
	return &Result{Stdout: resp.Stdout, Stderr: resp.Stderr, ExitCode: resp.ExitCode}, nil
}

// Order returns the step ids in the order they were run.
func (f *Fake) Order() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = c.Step.ID
	}
	return out
}

// Calls returns a copy of every recorded request.
func (f *Fake) Calls() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request{}, f.calls...)
}

// CallsFor returns the recorded requests for one step.
func (f *Fake) CallsFor(stepID string) []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Request
	for _, c := range f.calls {
		if c.Step.ID == stepID {
			out = append(out, c)
		}
	}
	return out
}

// CallCount returns how many times a step was run.
func (f *Fake) CallCount(stepID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counts[stepID]
}

// Peak returns the highest number of calls that were in flight at once.
func (f *Fake) Peak() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.peak
}

// Script replaces the response for one step mid-test.
func (f *Fake) Script(stepID string, resp FakeResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.script[stepID] = resp
}

var _ Runner = (*Fake)(nil)
