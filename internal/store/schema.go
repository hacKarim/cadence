package store

// migrations are applied in order, once each. Adding a migration is the only
// supported way to change the schema.
var migrations = []string{
	`CREATE TABLE runs (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		workflow      TEXT      NOT NULL,
		workflow_hash TEXT      NOT NULL,
		state         TEXT      NOT NULL,
		trigger       TEXT      NOT NULL DEFAULT 'manual',
		run_dir       TEXT      NOT NULL,
		resumed_from  INTEGER,
		started_at    TIMESTAMP NOT NULL,
		ended_at      TIMESTAMP,
		error         TEXT      NOT NULL DEFAULT ''
	);
	CREATE INDEX idx_runs_workflow ON runs(workflow, id DESC);

	CREATE TABLE step_runs (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		run_id     INTEGER   NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
		step_id    TEXT      NOT NULL,
		state      TEXT      NOT NULL,
		started_at TIMESTAMP,
		ended_at   TIMESTAMP,
		error      TEXT      NOT NULL DEFAULT '',
		UNIQUE(run_id, step_id)
	);

	CREATE TABLE step_items (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		step_run_id   INTEGER   NOT NULL REFERENCES step_runs(id) ON DELETE CASCADE,
		idx           INTEGER   NOT NULL,
		state         TEXT      NOT NULL,
		attempt       INTEGER   NOT NULL DEFAULT 0,
		exit_code     INTEGER   NOT NULL DEFAULT 0,
		jail_dir      TEXT      NOT NULL DEFAULT '',
		cache_key     TEXT      NOT NULL DEFAULT '',
		result        TEXT      NOT NULL DEFAULT '',
		excerpt       TEXT      NOT NULL DEFAULT '',
		outputs       TEXT      NOT NULL DEFAULT '{}',
		input_tokens  INTEGER   NOT NULL DEFAULT 0,
		output_tokens INTEGER   NOT NULL DEFAULT 0,
		cost_usd      REAL      NOT NULL DEFAULT 0,
		started_at    TIMESTAMP,
		ended_at      TIMESTAMP,
		error         TEXT      NOT NULL DEFAULT '',
		UNIQUE(step_run_id, idx)
	);

	CREATE TABLE workflow_locks (
		workflow    TEXT      PRIMARY KEY,
		run_id      INTEGER   NOT NULL,
		acquired_at TIMESTAMP NOT NULL
	);

	CREATE TABLE cache_entries (
		key          TEXT      PRIMARY KEY,
		step_id      TEXT      NOT NULL,
		result       TEXT      NOT NULL DEFAULT '',
		outputs      TEXT      NOT NULL DEFAULT '{}',
		artifact_dir TEXT      NOT NULL DEFAULT '',
		created_at   TIMESTAMP NOT NULL
	);`,
}

func migrationCount() int { return len(migrations) }
