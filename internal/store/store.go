// Package store persists runs, steps, items and the step cache in SQLite. It
// is the single source of truth: the engine, the CLI and the TUI all read it
// directly rather than talking to each other.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Store is an open cadence database.
type Store struct {
	db        *sql.DB
	cacheRoot string
}

// Run is one execution of a workflow.
type Run struct {
	ID           int64
	Workflow     string
	WorkflowHash string
	State        State
	Trigger      string
	RunDir       string
	ResumedFrom  *int64
	StartedAt    time.Time
	EndedAt      *time.Time
	Error        string
}

// StepRun is one step within a run.
type StepRun struct {
	ID        int64
	RunID     int64
	StepID    string
	State     State
	StartedAt *time.Time
	EndedAt   *time.Time
	Error     string
	Items     []StepItem
}

// StepItem is one fan-out item of a step. A step without for_each has exactly
// one item at index 0, so the engine has a single code path.
type StepItem struct {
	ID           int64
	StepRunID    int64
	Index        int
	State        State
	Attempt      int
	ExitCode     int
	JailDir      string
	CacheKey     string
	Result       string
	Excerpt      string
	Outputs      map[string]any
	InputTokens  int
	OutputTokens int
	CostUSD      float64
	StartedAt    *time.Time
	EndedAt      *time.Time
	Error        string
}

// RunTree is a run with its steps and their items, for display.
type RunTree struct {
	Run   Run
	Steps []StepRun
}

// Open opens or creates the database and applies any pending migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, cacheRoot: filepath.Join(filepath.Dir(path), "cache")}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, applied_at TIMESTAMP NOT NULL)`); err != nil {
		return err
	}
	var applied int
	if err := s.db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&applied); err != nil {
		return err
	}
	for i := applied; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, i+1, time.Now()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// CreateRun inserts a run and fills in its id.
func (s *Store) CreateRun(r *Run) error {
	res, err := s.db.Exec(
		`INSERT INTO runs (workflow, workflow_hash, state, trigger, run_dir, resumed_from, started_at, ended_at, error)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		r.Workflow, r.WorkflowHash, string(r.State), r.Trigger, r.RunDir, r.ResumedFrom, r.StartedAt, r.EndedAt, r.Error)
	if err != nil {
		return err
	}
	r.ID, err = res.LastInsertId()
	return err
}

// UpdateRun writes back a run's mutable fields.
func (s *Store) UpdateRun(r *Run) error {
	_, err := s.db.Exec(`UPDATE runs SET state=?, ended_at=?, error=? WHERE id=?`,
		string(r.State), r.EndedAt, r.Error, r.ID)
	return err
}

const runCols = `id, workflow, workflow_hash, state, trigger, run_dir, resumed_from, started_at, ended_at, error`

func scanRun(sc interface{ Scan(...any) error }) (Run, error) {
	var r Run
	var state string
	err := sc.Scan(&r.ID, &r.Workflow, &r.WorkflowHash, &state, &r.Trigger, &r.RunDir, &r.ResumedFrom, &r.StartedAt, &r.EndedAt, &r.Error)
	r.State = State(state)
	return r, err
}

// GetRun returns one run by id.
func (s *Store) GetRun(id int64) (*Run, error) {
	r, err := scanRun(s.db.QueryRow(`SELECT `+runCols+` FROM runs WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// ListRuns returns the most recent runs, optionally filtered by workflow.
func (s *Store) ListRuns(workflow string, limit int) ([]Run, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT ` + runCols + ` FROM runs`
	args := []any{}
	if workflow != "" {
		q += ` WHERE workflow=?`
		args = append(args, workflow)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateStepRun inserts a step row and fills in its id.
func (s *Store) CreateStepRun(sr *StepRun) error {
	res, err := s.db.Exec(
		`INSERT INTO step_runs (run_id, step_id, state, started_at, ended_at, error) VALUES (?,?,?,?,?,?)`,
		sr.RunID, sr.StepID, string(sr.State), sr.StartedAt, sr.EndedAt, sr.Error)
	if err != nil {
		return err
	}
	sr.ID, err = res.LastInsertId()
	return err
}

// UpdateStepRun writes back a step's mutable fields.
func (s *Store) UpdateStepRun(sr *StepRun) error {
	_, err := s.db.Exec(`UPDATE step_runs SET state=?, started_at=?, ended_at=?, error=? WHERE id=?`,
		string(sr.State), sr.StartedAt, sr.EndedAt, sr.Error, sr.ID)
	return err
}

// CreateItem inserts a step item and fills in its id.
func (s *Store) CreateItem(it *StepItem) error {
	outs, err := json.Marshal(orEmptyMap(it.Outputs))
	if err != nil {
		return err
	}
	res, err := s.db.Exec(
		`INSERT INTO step_items (step_run_id, idx, state, attempt, exit_code, jail_dir, cache_key,
		  result, excerpt, outputs, input_tokens, output_tokens, cost_usd, started_at, ended_at, error)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		it.StepRunID, it.Index, string(it.State), it.Attempt, it.ExitCode, it.JailDir, it.CacheKey,
		it.Result, it.Excerpt, string(outs), it.InputTokens, it.OutputTokens, it.CostUSD,
		it.StartedAt, it.EndedAt, it.Error)
	if err != nil {
		return err
	}
	it.ID, err = res.LastInsertId()
	return err
}

// UpdateItem writes back an item's mutable fields.
func (s *Store) UpdateItem(it *StepItem) error {
	outs, err := json.Marshal(orEmptyMap(it.Outputs))
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`UPDATE step_items SET state=?, attempt=?, exit_code=?, jail_dir=?, cache_key=?, result=?,
		   excerpt=?, outputs=?, input_tokens=?, output_tokens=?, cost_usd=?, started_at=?, ended_at=?, error=?
		 WHERE id=?`,
		string(it.State), it.Attempt, it.ExitCode, it.JailDir, it.CacheKey, it.Result,
		it.Excerpt, string(outs), it.InputTokens, it.OutputTokens, it.CostUSD,
		it.StartedAt, it.EndedAt, it.Error, it.ID)
	return err
}

func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// RunTree returns a run with its steps and items, ordered for display.
func (s *Store) RunTree(id int64) (*RunTree, error) {
	run, err := s.GetRun(id)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(
		`SELECT id, run_id, step_id, state, started_at, ended_at, error FROM step_runs WHERE run_id=? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tree := &RunTree{Run: *run}
	for rows.Next() {
		var sr StepRun
		var state string
		if err := rows.Scan(&sr.ID, &sr.RunID, &sr.StepID, &state, &sr.StartedAt, &sr.EndedAt, &sr.Error); err != nil {
			return nil, err
		}
		sr.State = State(state)
		tree.Steps = append(tree.Steps, sr)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range tree.Steps {
		items, err := s.itemsFor(tree.Steps[i].ID)
		if err != nil {
			return nil, err
		}
		tree.Steps[i].Items = items
	}
	return tree, nil
}

func (s *Store) itemsFor(stepRunID int64) ([]StepItem, error) {
	rows, err := s.db.Query(
		`SELECT id, step_run_id, idx, state, attempt, exit_code, jail_dir, cache_key, result, excerpt,
		   outputs, input_tokens, output_tokens, cost_usd, started_at, ended_at, error
		 FROM step_items WHERE step_run_id=? ORDER BY idx`, stepRunID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StepItem
	for rows.Next() {
		var it StepItem
		var state, outs string
		if err := rows.Scan(&it.ID, &it.StepRunID, &it.Index, &state, &it.Attempt, &it.ExitCode,
			&it.JailDir, &it.CacheKey, &it.Result, &it.Excerpt, &outs,
			&it.InputTokens, &it.OutputTokens, &it.CostUSD, &it.StartedAt, &it.EndedAt, &it.Error); err != nil {
			return nil, err
		}
		it.State = State(state)
		if err := json.Unmarshal([]byte(outs), &it.Outputs); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// AcquireWorkflowLock takes the exclusive run lock for a workflow. It returns
// false when another run already holds it, which is how the concurrency
// policy is enforced across processes.
func (s *Store) AcquireWorkflowLock(workflow string, runID int64) (bool, error) {
	res, err := s.db.Exec(
		`INSERT OR IGNORE INTO workflow_locks (workflow, run_id, acquired_at) VALUES (?,?,?)`,
		workflow, runID, time.Now())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// ReleaseWorkflowLock frees a workflow's run lock.
func (s *Store) ReleaseWorkflowLock(workflow string) error {
	_, err := s.db.Exec(`DELETE FROM workflow_locks WHERE workflow=?`, workflow)
	return err
}
