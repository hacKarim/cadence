package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/hacKarim/cadence/internal/sandbox"
	"github.com/hacKarim/cadence/internal/store"
)

func tree() *store.RunTree {
	now := time.Now()
	return &store.RunTree{
		Run: store.Run{ID: 7, Workflow: "research-funnel", State: store.StateRunning, StartedAt: now},
		Steps: []store.StepRun{
			{StepID: "screen", State: store.StateSucceeded, Items: []store.StepItem{
				{Index: 0, State: store.StateSucceeded},
			}},
			{StepID: "analyse", State: store.StateRunning, Items: fanItems(38, 4)},
			{StepID: "challenge", State: store.StatePending},
			{StepID: "publish", State: store.StateSkipped},
		},
	}
}

func fanItems(total, done int) []store.StepItem {
	out := make([]store.StepItem, total)
	for i := range out {
		out[i] = store.StepItem{Index: i, State: store.StatePending, CostUSD: 0.01}
		if i < done {
			out[i].State = store.StateSucceeded
		}
	}
	return out
}

func TestRenderGraphShowsStepStates(t *testing.T) {
	out := RenderGraph(tree(), 100, sandbox.BackendSeatbelt, true)
	for _, want := range []string{"screen", "analyse", "challenge", "publish", "succeeded", "running", "skipped"} {
		if !strings.Contains(out, want) {
			t.Errorf("graph does not mention %q:\n%s", want, out)
		}
	}
}

func TestRenderGraphShowsFanOutCounts(t *testing.T) {
	out := RenderGraph(tree(), 100, sandbox.BackendSeatbelt, true)
	if !strings.Contains(out, "4/38") {
		t.Errorf("graph does not show fan-out progress as done/total:\n%s", out)
	}
}

func TestRenderGraphOmitsCountForSingleItemSteps(t *testing.T) {
	out := RenderGraph(tree(), 100, sandbox.BackendSeatbelt, true)
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "screen") && strings.Contains(line, "1/1") {
			t.Errorf("single-item step shows a pointless item count: %q", line)
		}
	}
}

// If the sandbox cannot enforce isolation the user must see it here, not only
// in doctor output they may never run.
func TestRenderGraphBadgesUnenforcedSandbox(t *testing.T) {
	out := RenderGraph(tree(), 100, sandbox.BackendConfined, false)
	low := strings.ToLower(out)
	if !strings.Contains(low, "confined") {
		t.Errorf("graph does not name the backend:\n%s", out)
	}
	if !strings.Contains(low, "not enforced") {
		t.Errorf("graph does not warn that isolation is not enforced:\n%s", out)
	}
}

func TestRenderGraphShowsEnforcedBackendWithoutWarning(t *testing.T) {
	out := RenderGraph(tree(), 100, sandbox.BackendSeatbelt, true)
	if strings.Contains(strings.ToLower(out), "not enforced") {
		t.Errorf("enforced backend should not carry a warning:\n%s", out)
	}
}

func TestRenderGraphFitsNarrowTerminal(t *testing.T) {
	const width = 60
	out := RenderGraph(tree(), width, sandbox.BackendConfined, false)
	for i, line := range strings.Split(out, "\n") {
		if len([]rune(line)) > width {
			t.Errorf("line %d is %d runes wide, limit is %d: %q", i, len([]rune(line)), width, line)
		}
	}
}

func TestRenderRunListShowsRuns(t *testing.T) {
	runs := []store.Run{
		{ID: 2, Workflow: "research-funnel", State: store.StateFailed, StartedAt: time.Now()},
		{ID: 1, Workflow: "code-review", State: store.StateSucceeded, StartedAt: time.Now()},
	}
	out := RenderRunList(runs, 80, 0)
	for _, want := range []string{"research-funnel", "code-review", "failed", "succeeded"} {
		if !strings.Contains(out, want) {
			t.Errorf("run list does not mention %q:\n%s", want, out)
		}
	}
}

func TestRenderRunListMarksSelection(t *testing.T) {
	runs := []store.Run{
		{ID: 2, Workflow: "a", State: store.StateFailed, StartedAt: time.Now()},
		{ID: 1, Workflow: "b", State: store.StateSucceeded, StartedAt: time.Now()},
	}
	first := RenderRunList(runs, 80, 0)
	second := RenderRunList(runs, 80, 1)
	if first == second {
		t.Error("the selected row is not marked differently")
	}
}

func TestRenderRunListHandlesEmpty(t *testing.T) {
	if out := RenderRunList(nil, 80, 0); !strings.Contains(out, "no runs") {
		t.Errorf("empty run list should say so, got %q", out)
	}
}

func TestRenderGraphShowsAccumulatedCost(t *testing.T) {
	out := RenderGraph(tree(), 100, sandbox.BackendSeatbelt, true)
	if !strings.Contains(out, "$") {
		t.Errorf("graph does not show accumulated cost:\n%s", out)
	}
}
