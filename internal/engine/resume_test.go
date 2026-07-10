package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/hacKarim/cadence/internal/runner"
	"github.com/hacKarim/cadence/internal/store"
)

const resumeWF = `
name: resumable
steps:
  - id: a
    run: echo a
  - id: b
    needs: [a]
    run: echo b
  - id: c
    needs: [b]
    run: echo c
`

func TestResumeCreatesNewRunInheritingResults(t *testing.T) {
	e, f, st, _ := harnessClock(t, map[string]runner.FakeResponse{"b": {FailTimes: 1, ExitCode: 1}})
	wf := parse(t, resumeWF)

	first, _ := e.Run(context.Background(), wf, "h")
	if first.State != store.StateFailed {
		t.Fatalf("first run state = %q, want failed", first.State)
	}
	if f.CallCount("a") != 1 {
		t.Fatalf("a ran %d times in the first run", f.CallCount("a"))
	}

	e.opt.ResumeFrom = first.ID
	second, err := e.Run(context.Background(), wf, "h")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if second.ID == first.ID {
		t.Error("resume mutated the original run instead of creating a new one")
	}
	if second.ResumedFrom == nil || *second.ResumedFrom != first.ID {
		t.Error("resumed run does not record what it resumed from")
	}
	if second.State != store.StateSucceeded {
		t.Errorf("resumed run state = %q (%s)", second.State, second.Error)
	}
	if f.CallCount("a") != 1 {
		t.Errorf("a ran %d times in total, want the succeeded step to be inherited", f.CallCount("a"))
	}
	if f.CallCount("b") != 2 || f.CallCount("c") != 1 {
		t.Errorf("b ran %d, c ran %d; want b retried and c run once", f.CallCount("b"), f.CallCount("c"))
	}
	tree, _ := st.RunTree(second.ID)
	for _, s := range tree.Steps {
		if s.StepID == "a" && s.State != store.StateCached {
			t.Errorf("inherited step a = %q, want cached", s.State)
		}
	}
	if first2, _ := st.GetRun(first.ID); first2.State != store.StateFailed {
		t.Error("the original run was modified by the resume")
	}
}

// Reusing results computed from a different workflow file would be exactly the
// silent partial success the design forbids.
func TestResumeRefusesWhenWorkflowHashChanged(t *testing.T) {
	e, _, _, _ := harnessClock(t, map[string]runner.FakeResponse{"b": {FailTimes: 9, ExitCode: 1}})
	wf := parse(t, resumeWF)
	first, _ := e.Run(context.Background(), wf, "hash-one")

	e.opt.ResumeFrom = first.ID
	_, err := e.Run(context.Background(), wf, "hash-two")
	if err == nil {
		t.Fatal("resume succeeded against a changed workflow file")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error %q should tell the user how to proceed", err)
	}
}

func TestResumeForceRerunsEverything(t *testing.T) {
	e, f, _, _ := harnessClock(t, map[string]runner.FakeResponse{"b": {FailTimes: 1, ExitCode: 1}})
	wf := parse(t, resumeWF)
	first, _ := e.Run(context.Background(), wf, "hash-one")

	e.opt.ResumeFrom = first.ID
	e.opt.Force = true
	if _, err := e.Run(context.Background(), wf, "hash-two"); err != nil {
		t.Fatalf("forced resume: %v", err)
	}
	if f.CallCount("a") != 2 {
		t.Errorf("a ran %d times, want 2 with --force", f.CallCount("a"))
	}
}
