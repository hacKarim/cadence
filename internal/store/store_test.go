package store

import (
	"path/filepath"
	"testing"
	"time"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "cadence.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenCreatesSchema(t *testing.T) {
	s := openTemp(t)
	for _, table := range []string{"runs", "step_runs", "step_items", "workflow_locks", "cache_entries"} {
		var n int
		if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil {
			t.Fatalf("query: %v", err)
		}
		if n != 1 {
			t.Errorf("table %q missing", table)
		}
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cadence.db")
	s1, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	s1.Close()
	s2, err := Open(p)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer s2.Close()
	var n int
	if err := s2.db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != migrationCount() {
		t.Errorf("applied %d migrations, want %d", n, migrationCount())
	}
}

func TestRunLifecycle(t *testing.T) {
	s := openTemp(t)
	r := &Run{Workflow: "wf", WorkflowHash: "h1", State: StateRunning, RunDir: "/tmp/r", Trigger: "manual", StartedAt: time.Now()}
	if err := s.CreateRun(r); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if r.ID == 0 {
		t.Fatal("CreateRun did not set an id")
	}
	end := time.Now()
	r.State, r.EndedAt = StateSucceeded, &end
	if err := s.UpdateRun(r); err != nil {
		t.Fatalf("UpdateRun: %v", err)
	}
	got, err := s.GetRun(r.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.State != StateSucceeded || got.EndedAt == nil {
		t.Errorf("round trip lost state: %+v", got)
	}
	runs, err := s.ListRuns("wf", 10)
	if err != nil || len(runs) != 1 {
		t.Errorf("ListRuns = %v, %v", runs, err)
	}
}

func TestStateRoundTrip(t *testing.T) {
	s := openTemp(t)
	for _, st := range AllStates() {
		r := &Run{Workflow: "wf", WorkflowHash: "h", State: st, RunDir: "/d", Trigger: "manual", StartedAt: time.Now()}
		if err := s.CreateRun(r); err != nil {
			t.Fatalf("CreateRun(%s): %v", st, err)
		}
		got, err := s.GetRun(r.ID)
		if err != nil {
			t.Fatalf("GetRun(%s): %v", st, err)
		}
		if got.State != st {
			t.Errorf("state %q came back as %q", st, got.State)
		}
	}
}

func TestRunTreeReturnsStepsAndItems(t *testing.T) {
	s := openTemp(t)
	r := &Run{Workflow: "wf", WorkflowHash: "h", State: StateRunning, RunDir: "/d", Trigger: "manual", StartedAt: time.Now()}
	s.CreateRun(r)

	sr := &StepRun{RunID: r.ID, StepID: "analyse", State: StateRunning}
	if err := s.CreateStepRun(sr); err != nil {
		t.Fatalf("CreateStepRun: %v", err)
	}
	for i := 0; i < 3; i++ {
		it := &StepItem{StepRunID: sr.ID, Index: i, State: StateSucceeded, Result: "r"}
		if err := s.CreateItem(it); err != nil {
			t.Fatalf("CreateItem: %v", err)
		}
	}
	tree, err := s.RunTree(r.ID)
	if err != nil {
		t.Fatalf("RunTree: %v", err)
	}
	if len(tree.Steps) != 1 {
		t.Fatalf("got %d steps, want 1", len(tree.Steps))
	}
	if len(tree.Steps[0].Items) != 3 {
		t.Errorf("got %d items, want 3", len(tree.Steps[0].Items))
	}
	if tree.Run.Workflow != "wf" {
		t.Errorf("tree.Run not populated")
	}
}

func TestItemStoresTokensAndCostAndOutputs(t *testing.T) {
	s := openTemp(t)
	r := &Run{Workflow: "wf", WorkflowHash: "h", State: StateRunning, RunDir: "/d", Trigger: "manual", StartedAt: time.Now()}
	s.CreateRun(r)
	sr := &StepRun{RunID: r.ID, StepID: "a", State: StateRunning}
	s.CreateStepRun(sr)

	it := &StepItem{
		StepRunID: sr.ID, Index: 0, State: StateSucceeded,
		InputTokens: 111, OutputTokens: 222, CostUSD: 0.0123,
		Outputs: map[string]any{"verdict": "undervalued"},
	}
	if err := s.CreateItem(it); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	tree, _ := s.RunTree(r.ID)
	got := tree.Steps[0].Items[0]
	if got.InputTokens != 111 || got.OutputTokens != 222 || got.CostUSD != 0.0123 {
		t.Errorf("tokens/cost lost: %+v", got)
	}
	if got.Outputs["verdict"] != "undervalued" {
		t.Errorf("outputs lost: %+v", got.Outputs)
	}
}

func TestAcquireLockIsExclusive(t *testing.T) {
	s := openTemp(t)
	ok, err := s.AcquireWorkflowLock("wf", 1)
	if err != nil || !ok {
		t.Fatalf("first acquire = %v, %v", ok, err)
	}
	ok, err = s.AcquireWorkflowLock("wf", 2)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("second acquire succeeded while the first still holds the lock")
	}
}

func TestReleaseLockAllowsReacquire(t *testing.T) {
	s := openTemp(t)
	s.AcquireWorkflowLock("wf", 1)
	if err := s.ReleaseWorkflowLock("wf"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	ok, err := s.AcquireWorkflowLock("wf", 2)
	if err != nil || !ok {
		t.Errorf("reacquire = %v, %v", ok, err)
	}
}

func TestLocksAreIndependentPerWorkflow(t *testing.T) {
	s := openTemp(t)
	s.AcquireWorkflowLock("a", 1)
	ok, err := s.AcquireWorkflowLock("b", 2)
	if err != nil || !ok {
		t.Errorf("unrelated workflow blocked: %v, %v", ok, err)
	}
}
