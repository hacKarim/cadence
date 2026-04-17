package jail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hacKarim/cadence/internal/spec"
	"github.com/hacKarim/cadence/internal/template"
)

func TestCreateMakesWorkOutMeta(t *testing.T) {
	j, err := Create(t.TempDir(), "screen", 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, d := range []string{j.Work, j.Out, j.Meta} {
		st, err := os.Stat(d)
		if err != nil || !st.IsDir() {
			t.Errorf("%s is not a directory: %v", d, err)
		}
	}
}

func TestStageWritesContentInputs(t *testing.T) {
	j, _ := Create(t.TempDir(), "challenge", 0)
	st := spec.Step{ID: "challenge", Inputs: map[string]spec.InputSpec{
		"report.md": {Content: "{{ .item.result }}"},
	}}
	sc := template.Scope{Item: map[string]any{"result": "the report body"}}
	if err := j.Stage(st, sc); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(j.Work, "report.md"))
	if err != nil {
		t.Fatalf("read staged file: %v", err)
	}
	if string(b) != "the report body" {
		t.Errorf("staged content = %q", b)
	}
}

func TestStageCopiesFileInputs(t *testing.T) {
	src := t.TempDir()
	srcFile := filepath.Join(src, "candidates.json")
	os.WriteFile(srcFile, []byte(`[1,2]`), 0o600)

	j, _ := Create(t.TempDir(), "analyse", 0)
	st := spec.Step{ID: "analyse", Inputs: map[string]spec.InputSpec{
		"data.json": {Copy: `{{ file .steps.screen "candidates.json" }}`},
	}}
	sc := template.Scope{Steps: map[string]template.StepView{"screen": {Dir: src}}}
	if err := j.Stage(st, sc); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(j.Work, "data.json"))
	if err != nil || string(b) != `[1,2]` {
		t.Errorf("copied file = %q, err %v", b, err)
	}
}

func TestStageRejectsEscapingInputName(t *testing.T) {
	j, _ := Create(t.TempDir(), "a", 0)
	for _, bad := range []string{"../evil", "/etc/passwd", "a/../../b"} {
		st := spec.Step{ID: "a", Inputs: map[string]spec.InputSpec{bad: {Content: "x"}}}
		if err := j.Stage(st, template.Scope{}); err == nil {
			t.Errorf("Stage accepted input name %q", bad)
		}
	}
}

func TestStageCopiesWorkdirReadOnly(t *testing.T) {
	proj := t.TempDir()
	os.WriteFile(filepath.Join(proj, "screen.py"), []byte("print(1)"), 0o755)
	os.MkdirAll(filepath.Join(proj, "lib"), 0o755)
	os.WriteFile(filepath.Join(proj, "lib", "helper.py"), []byte("x=1"), 0o644)

	j, _ := Create(t.TempDir(), "screen", 0)
	st := spec.Step{ID: "screen", Workdir: proj}
	if err := j.Stage(st, template.Scope{}); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	staged := filepath.Join(j.Work, "screen.py")
	info, err := os.Stat(staged)
	if err != nil {
		t.Fatalf("workdir file not staged: %v", err)
	}
	if info.Mode().Perm()&0o222 != 0 {
		t.Errorf("staged workdir file mode = %v, want read-only", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(j.Work, "lib", "helper.py")); err != nil {
		t.Errorf("nested workdir file not staged: %v", err)
	}
}

func TestEnvIsConstructedNotInherited(t *testing.T) {
	j, _ := Create(t.TempDir(), "a", 0)
	parent := []string{"PATH=/bin", "HOME=/home/x", "AWS_SECRET_ACCESS_KEY=hunter2", "RANDOM_THING=1"}
	env, err := j.Env(spec.Step{ID: "a"}, parent)
	if err != nil {
		t.Fatalf("Env: %v", err)
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "PATH=/bin") {
		t.Error("PATH was not carried through")
	}
	for _, leaked := range []string{"AWS_SECRET_ACCESS_KEY", "RANDOM_THING"} {
		if strings.Contains(joined, leaked) {
			t.Errorf("environment leaked %s", leaked)
		}
	}
}

func TestEnvCarriesAgentCredentials(t *testing.T) {
	j, _ := Create(t.TempDir(), "a", 0)
	env, _ := j.Env(spec.Step{ID: "a"}, []string{"ANTHROPIC_API_KEY=k", "CLAUDE_CONFIG_DIR=/c"})
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "ANTHROPIC_API_KEY=k") || !strings.Contains(joined, "CLAUDE_CONFIG_DIR=/c") {
		t.Errorf("agent credentials not carried: %v", env)
	}
}

func TestEnvIncludesDeclaredStepEnv(t *testing.T) {
	j, _ := Create(t.TempDir(), "a", 0)
	env, _ := j.Env(spec.Step{ID: "a", Env: map[string]string{"TICKER": "AAPL"}}, []string{"PATH=/bin"})
	if !strings.Contains(strings.Join(env, "\n"), "TICKER=AAPL") {
		t.Errorf("declared env missing: %v", env)
	}
}

func TestEnvContainsNoRunRootPath(t *testing.T) {
	runDir := t.TempDir()
	j, _ := Create(runDir, "a", 0)
	env, _ := j.Env(spec.Step{ID: "a"}, []string{"PATH=/bin", "HOME=/home/x"})
	for _, kv := range env {
		if strings.Contains(kv, runDir) {
			t.Errorf("environment exposes the run root: %q", kv)
		}
	}
}

// The invariant the whole project exists for: a sceptic step must not be able
// to reach the analyst step's output.
func TestIsolationNeighbourNotVisible(t *testing.T) {
	runDir := t.TempDir()
	analyst, _ := Create(runDir, "analyse", 0)
	os.WriteFile(filepath.Join(analyst.Out, "report.md"), []byte("I think it is undervalued"), 0o600)
	os.WriteFile(filepath.Join(analyst.Meta, "stdout.log"), []byte("full reasoning trace"), 0o600)

	sceptic, _ := Create(runDir, "challenge", 0)
	st := spec.Step{ID: "challenge", Inputs: map[string]spec.InputSpec{
		"report.md": {Content: "{{ .item.result }}"},
	}}
	sc := template.Scope{Item: map[string]any{"result": "I think it is undervalued"}}
	if err := sceptic.Stage(st, sc); err != nil {
		t.Fatalf("Stage: %v", err)
	}

	var got []string
	filepath.Walk(sceptic.Work, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			rel, _ := filepath.Rel(sceptic.Work, p)
			got = append(got, rel)
		}
		return nil
	})
	if len(got) != 1 || got[0] != "report.md" {
		t.Errorf("sceptic jail contains %v, want exactly [report.md]", got)
	}
	if strings.HasPrefix(sceptic.Work, analyst.Root) || strings.HasPrefix(analyst.Root, sceptic.Work) {
		t.Error("jails are nested inside one another")
	}
	if _, err := os.Stat(filepath.Join(sceptic.Work, "stdout.log")); err == nil {
		t.Error("analyst reasoning trace is reachable from the sceptic jail")
	}
}
