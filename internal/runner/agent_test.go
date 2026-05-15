package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hacKarim/cadence/internal/sandbox"
	"github.com/hacKarim/cadence/internal/spec"
)

func agentReq(t *testing.T, st spec.Step, prompt string) (Request, string) {
	t.Helper()
	capture := filepath.Join(t.TempDir(), "cap")
	return Request{
		Step:    st,
		Jail:    newJail(t),
		Prompt:  prompt,
		Env:     []string{"PATH=" + os.Getenv("PATH"), "CLAUDE_TEST_CAPTURE=" + capture},
		Timeout: 30 * time.Second,
	}, capture
}

func fakeBin(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("testdata/fake-claude.sh")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAgentSendsPromptOnStdin(t *testing.T) {
	r, cap := agentReq(t, spec.Step{ID: "a", Agent: &spec.AgentSpec{Prompt: "p"}}, "analyse AAPL")
	if _, err := NewAgent(sandbox.Confined{}, fakeBin(t)).Run(context.Background(), r); err != nil {
		t.Fatalf("Run: %v", err)
	}
	b, err := os.ReadFile(cap + ".stdin")
	if err != nil {
		t.Fatalf("stdin capture: %v", err)
	}
	if strings.TrimSpace(string(b)) != "analyse AAPL" {
		t.Errorf("stdin = %q, want the prompt", b)
	}
	args, _ := os.ReadFile(cap + ".args")
	if strings.Contains(string(args), "analyse AAPL") {
		t.Error("prompt was passed on argv as well as stdin")
	}
}

func TestAgentPassesModelAndAllowedTools(t *testing.T) {
	st := spec.Step{ID: "a", Agent: &spec.AgentSpec{Prompt: "p", Model: "claude-opus-5", Tools: []string{"Read", "WebSearch"}}}
	r, cap := agentReq(t, st, "x")
	if _, err := NewAgent(sandbox.Confined{}, fakeBin(t)).Run(context.Background(), r); err != nil {
		t.Fatalf("Run: %v", err)
	}
	args, _ := os.ReadFile(cap + ".args")
	s := string(args)
	for _, want := range []string{"--print", "--output-format json", "--model claude-opus-5", "--allowed-tools Read,WebSearch"} {
		if !strings.Contains(s, want) {
			t.Errorf("argv %q is missing %q", s, want)
		}
	}
}

func TestAgentOmitsSkipPermissionsByDefault(t *testing.T) {
	r, cap := agentReq(t, spec.Step{ID: "a", Agent: &spec.AgentSpec{Prompt: "p"}}, "x")
	NewAgent(sandbox.Confined{}, fakeBin(t)).Run(context.Background(), r)
	args, _ := os.ReadFile(cap + ".args")
	if strings.Contains(string(args), "dangerously-skip-permissions") {
		t.Error("scheduled agent was granted full permissions without asking")
	}
}

func TestAgentPassesSkipPermissionsWhenElevated(t *testing.T) {
	st := spec.Step{ID: "a", Agent: &spec.AgentSpec{Prompt: "p", ElevatedPermissions: true}}
	r, cap := agentReq(t, st, "x")
	NewAgent(sandbox.Confined{}, fakeBin(t)).Run(context.Background(), r)
	args, _ := os.ReadFile(cap + ".args")
	if !strings.Contains(string(args), "dangerously-skip-permissions") {
		t.Error("elevated_permissions was not honoured")
	}
}

func TestAgentParsesJSONEnvelope(t *testing.T) {
	r, _ := agentReq(t, spec.Step{ID: "a", Agent: &spec.AgentSpec{Prompt: "p"}}, "x")
	res, err := NewAgent(sandbox.Confined{}, fakeBin(t)).Run(context.Background(), r)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Stdout, "the report body") {
		t.Errorf("Stdout = %q, want the envelope's result text", res.Stdout)
	}
	if res.InputTokens != 111 || res.OutputTokens != 222 {
		t.Errorf("tokens = %d/%d, want 111/222", res.InputTokens, res.OutputTokens)
	}
	if res.CostUSD != 0.0123 {
		t.Errorf("CostUSD = %v, want 0.0123", res.CostUSD)
	}
}

func TestAgentMalformedEnvelopeIsError(t *testing.T) {
	r, _ := agentReq(t, spec.Step{ID: "a", Agent: &spec.AgentSpec{Prompt: "p"}}, "x")
	r.Env = append(r.Env, "CLAUDE_TEST_BADJSON=1")
	if _, err := NewAgent(sandbox.Confined{}, fakeBin(t)).Run(context.Background(), r); err == nil {
		t.Fatal("Run succeeded on a malformed envelope")
	}
}

func TestAgentRawEnvelopeIsLogged(t *testing.T) {
	r, _ := agentReq(t, spec.Step{ID: "a", Agent: &spec.AgentSpec{Prompt: "p"}}, "x")
	NewAgent(sandbox.Confined{}, fakeBin(t)).Run(context.Background(), r)
	b, err := os.ReadFile(filepath.Join(r.Jail.Meta, "stdout.log"))
	if err != nil || !strings.Contains(string(b), "total_cost_usd") {
		t.Errorf("raw envelope not logged: %v %q", err, b)
	}
}
