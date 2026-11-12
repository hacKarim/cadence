package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hacKarim/cadence/internal/jail"
	"github.com/hacKarim/cadence/internal/spec"
)

var day1 = time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)
var day2 = time.Date(2026, 3, 5, 9, 0, 0, 0, time.UTC)

func baseStep() spec.Step {
	return spec.Step{ID: "analyse", Agent: &spec.AgentSpec{Prompt: "p"}, Cache: spec.CacheAlways}
}

func TestKeyIsStableForSameInputs(t *testing.T) {
	a := CacheKey(baseStep(), "prompt", "digest", "wfhash", spec.CacheAlways, day1)
	b := CacheKey(baseStep(), "prompt", "digest", "wfhash", spec.CacheAlways, day1)
	if a == "" || a != b {
		t.Errorf("keys differ for identical inputs: %q vs %q", a, b)
	}
}

func TestKeyIsEmptyWhenCachingIsOff(t *testing.T) {
	if k := CacheKey(baseStep(), "p", "d", "w", spec.CacheNever, day1); k != "" {
		t.Errorf("CacheKey = %q, want empty when cache is never", k)
	}
}

func TestKeyChangesWithPrompt(t *testing.T) {
	a := CacheKey(baseStep(), "prompt one", "d", "w", spec.CacheAlways, day1)
	b := CacheKey(baseStep(), "prompt two", "d", "w", spec.CacheAlways, day1)
	if a == b {
		t.Error("key did not change with the resolved prompt")
	}
}

func TestKeyChangesWithStagedInputs(t *testing.T) {
	a := CacheKey(baseStep(), "p", "digest-a", "w", spec.CacheAlways, day1)
	b := CacheKey(baseStep(), "p", "digest-b", "w", spec.CacheAlways, day1)
	if a == b {
		t.Error("key did not change with the staged input digest")
	}
}

func TestKeyChangesWithWorkflowHash(t *testing.T) {
	a := CacheKey(baseStep(), "p", "d", "hash-a", spec.CacheAlways, day1)
	b := CacheKey(baseStep(), "p", "d", "hash-b", spec.CacheAlways, day1)
	if a == b {
		t.Error("key did not change with the workflow file hash")
	}
}

// Secrets live in the environment. If env values entered the key, rotating a
// credential would invalidate every cached step for no reason.
func TestKeyExcludesEnvValues(t *testing.T) {
	s1, s2 := baseStep(), baseStep()
	s1.Env = map[string]string{"TOKEN": "old-secret"}
	s2.Env = map[string]string{"TOKEN": "new-secret"}
	if CacheKey(s1, "p", "d", "w", spec.CacheAlways, day1) != CacheKey(s2, "p", "d", "w", spec.CacheAlways, day1) {
		t.Error("key changed when only an environment value changed")
	}
}

func TestKeyDailyFoldsDate(t *testing.T) {
	a := CacheKey(baseStep(), "p", "d", "w", spec.CacheDaily, day1)
	b := CacheKey(baseStep(), "p", "d", "w", spec.CacheDaily, day2)
	if a == b {
		t.Error("daily key did not change across days")
	}
	c := CacheKey(baseStep(), "p", "d", "w", spec.CacheAlways, day1)
	d := CacheKey(baseStep(), "p", "d", "w", spec.CacheAlways, day2)
	if c != d {
		t.Error("always key changed across days")
	}
}

func TestSaveThenLookupHits(t *testing.T) {
	s := openTemp(t)
	j, _ := jail.Create(t.TempDir(), "analyse", 0)
	os.WriteFile(filepath.Join(j.Out, "report.md"), []byte("the report"), 0o600)

	err := s.SaveCache("k1", "analyse", j, map[string]any{"verdict": "undervalued"}, "result text")
	if err != nil {
		t.Fatalf("SaveCache: %v", err)
	}
	e, ok, err := s.LookupCache("k1")
	if err != nil || !ok {
		t.Fatalf("LookupCache = %v, %v", ok, err)
	}
	if e.Result != "result text" || e.Outputs["verdict"] != "undervalued" {
		t.Errorf("entry = %+v", e)
	}
}

func TestLookupMissReturnsFalse(t *testing.T) {
	s := openTemp(t)
	if _, ok, err := s.LookupCache("nope"); ok || err != nil {
		t.Errorf("LookupCache = %v, %v, want miss", ok, err)
	}
}

// A cached step must leave downstream steps something to stage, so the saved
// out/ tree is restored into the new jail.
func TestRestoreCopiesOutTree(t *testing.T) {
	s := openTemp(t)
	src, _ := jail.Create(t.TempDir(), "analyse", 0)
	os.MkdirAll(filepath.Join(src.Out, "sub"), 0o750)
	os.WriteFile(filepath.Join(src.Out, "report.md"), []byte("the report"), 0o600)
	os.WriteFile(filepath.Join(src.Out, "sub", "data.json"), []byte("[1]"), 0o600)
	if err := s.SaveCache("k2", "analyse", src, nil, "r"); err != nil {
		t.Fatal(err)
	}
	e, _, _ := s.LookupCache("k2")

	dst, _ := jail.Create(t.TempDir(), "analyse", 0)
	if err := RestoreCache(e, dst); err != nil {
		t.Fatalf("RestoreCache: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dst.Out, "report.md"))
	if err != nil || string(b) != "the report" {
		t.Errorf("report not restored: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dst.Out, "sub", "data.json")); err != nil {
		t.Errorf("nested artifact not restored: %v", err)
	}
}

func TestPruneRemovesOldEntries(t *testing.T) {
	s := openTemp(t)
	j, _ := jail.Create(t.TempDir(), "a", 0)
	s.SaveCache("old", "a", j, nil, "r")
	s.db.Exec(`UPDATE cache_entries SET created_at=? WHERE key='old'`, time.Now().Add(-72*time.Hour))
	s.SaveCache("new", "a", j, nil, "r")

	n, err := s.PruneCache(time.Now().Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("PruneCache: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned %d, want 1", n)
	}
	if _, ok, _ := s.LookupCache("old"); ok {
		t.Error("old entry survived prune")
	}
	if _, ok, _ := s.LookupCache("new"); !ok {
		t.Error("recent entry was pruned")
	}
}

// A cache hit hands the new run the saved artifacts. If those are hardlinks,
// the new step rewriting its own output silently rewrites the cache entry and
// the artifact the earlier run is recorded as having produced.
func TestRestoredArtifactsAreIndependentCopies(t *testing.T) {
	s := openTemp(t)
	first, _ := jail.Create(t.TempDir(), "analyse", 0)
	report := filepath.Join(first.Out, "report.md")
	if err := os.WriteFile(report, []byte("ORIGINAL"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveCache("k", "analyse", first, nil, "r"); err != nil {
		t.Fatal(err)
	}

	second, _ := jail.Create(t.TempDir(), "analyse", 0)
	e, _, _ := s.LookupCache("k")
	if err := RestoreCache(e, second); err != nil {
		t.Fatal(err)
	}
	// The step rewrites its own output, exactly as `>` in a shell does.
	if err := os.WriteFile(filepath.Join(second.Out, "report.md"), []byte("TAMPERED"), 0o600); err != nil {
		t.Fatal(err)
	}

	if b, _ := os.ReadFile(report); string(b) != "ORIGINAL" {
		t.Errorf("the earlier run's artifact now reads %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(e.ArtifactDir, "report.md")); string(b) != "ORIGINAL" {
		t.Errorf("the cache entry now reads %q", b)
	}
}
