package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/hacKarim/cadence/internal/jail"
	"github.com/hacKarim/cadence/internal/spec"
)

// CacheEntry is a previously computed step item, keyed by content.
type CacheEntry struct {
	Key         string
	StepID      string
	Result      string
	Outputs     map[string]any
	ArtifactDir string
	CreatedAt   time.Time
}

// keyShape is the canonical, order-stable encoding a cache key hashes. Env is
// deliberately absent: secrets live there, and a rotated credential should not
// invalidate every cached step.
type keyShape struct {
	StepID        string                     `json:"step_id"`
	Run           string                     `json:"run"`
	AgentModel    string                     `json:"agent_model"`
	AgentTools    []string                   `json:"agent_tools"`
	Elevated      bool                       `json:"elevated"`
	Extract       *spec.Extract              `json:"extract"`
	Outputs       map[string]spec.OutputSpec `json:"outputs"`
	Isolate       bool                       `json:"isolate"`
	Workdir       string                     `json:"workdir"`
	Prompt        string                     `json:"prompt"`
	StagedDigest  string                     `json:"staged"`
	WorkflowHash  string                     `json:"wf"`
	DateComponent string                     `json:"date,omitempty"`
}

// CacheKey computes the content address of one step item. An empty result
// means this step is not cacheable, which is the default.
func CacheKey(st spec.Step, resolvedPrompt, stagedDigest, workflowHash string, mode spec.CacheMode, now time.Time) string {
	if mode == spec.CacheNever || mode == "" {
		return ""
	}
	k := keyShape{
		StepID:       st.ID,
		Run:          st.Run,
		Outputs:      st.Outputs,
		Isolate:      st.Isolate,
		Workdir:      st.Workdir,
		Prompt:       resolvedPrompt,
		StagedDigest: stagedDigest,
		WorkflowHash: workflowHash,
	}
	if st.Agent != nil {
		k.AgentModel, k.AgentTools, k.Elevated, k.Extract = st.Agent.Model, st.Agent.Tools, st.Agent.ElevatedPermissions, st.Agent.Extract
	}
	if mode == spec.CacheDaily {
		k.DateComponent = now.Format("2006-01-02")
	}
	b, err := json.Marshal(k)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// cacheDir is where saved artifacts live, beside the database.
func (s *Store) cacheDir() string { return s.cacheRoot }

// LookupCache returns a saved entry for key, if one exists.
func (s *Store) LookupCache(key string) (*CacheEntry, bool, error) {
	if key == "" {
		return nil, false, nil
	}
	var e CacheEntry
	var outs string
	err := s.db.QueryRow(
		`SELECT key, step_id, result, outputs, artifact_dir, created_at FROM cache_entries WHERE key=?`, key).
		Scan(&e.Key, &e.StepID, &e.Result, &outs, &e.ArtifactDir, &e.CreatedAt)
	if err != nil {
		return nil, false, nil
	}
	if err := json.Unmarshal([]byte(outs), &e.Outputs); err != nil {
		return nil, false, err
	}
	return &e, true, nil
}

// SaveCache records a finished item and copies its out/ tree into the cache,
// so a later hit can hand downstream steps the same artifacts.
func (s *Store) SaveCache(key, stepID string, j *jail.Jail, outputs map[string]any, result string) error {
	if key == "" {
		return nil
	}
	dir := filepath.Join(s.cacheDir(), key)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := copyTree(j.Out, dir); err != nil {
		return fmt.Errorf("cache artifacts: %w", err)
	}
	outs, err := json.Marshal(orEmptyMap(outputs))
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT OR REPLACE INTO cache_entries (key, step_id, result, outputs, artifact_dir, created_at)
		 VALUES (?,?,?,?,?,?)`,
		key, stepID, result, string(outs), dir, time.Now())
	return err
}

// RestoreCache copies a cached entry's artifacts into a fresh jail.
func RestoreCache(e *CacheEntry, j *jail.Jail) error {
	if e == nil || e.ArtifactDir == "" {
		return nil
	}
	return copyTree(e.ArtifactDir, j.Out)
}

// PruneCache deletes entries created before the cutoff and their artifacts.
func (s *Store) PruneCache(olderThan time.Time) (int, error) {
	rows, err := s.db.Query(`SELECT key, artifact_dir FROM cache_entries WHERE created_at < ?`, olderThan)
	if err != nil {
		return 0, err
	}
	var keys, dirs []string
	for rows.Next() {
		var k, d string
		if err := rows.Scan(&k, &d); err != nil {
			rows.Close()
			return 0, err
		}
		keys, dirs = append(keys, k), append(dirs, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for i, k := range keys {
		if dirs[i] != "" {
			os.RemoveAll(dirs[i])
		}
		if _, err := s.db.Exec(`DELETE FROM cache_entries WHERE key=?`, k); err != nil {
			return 0, err
		}
	}
	return len(keys), nil
}

// copyTree copies a directory recursively, hardlinking where the filesystem
// allows it and falling back to a byte copy.
func copyTree(src, dst string) error {
	if _, err := os.Stat(src); err != nil {
		return nil
	}
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		if err := os.Link(p, target); err == nil {
			return nil
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}
