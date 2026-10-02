package runner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
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

// The agent process needs its own config and temp directories or it exits
// before producing anything. Those paths are declared, not inherited.
func TestAgentDeclaresItsSupportPaths(t *testing.T) {
	p := AgentSupportPaths()
	if len(p) == 0 {
		t.Fatal("agent runner declares no support paths")
	}
	var hasConfig, hasTemp bool
	for _, path := range p {
		if strings.Contains(path, ".claude") {
			hasConfig = true
		}
		if strings.Contains(path, "tmp") || strings.Contains(path, "Temp") || strings.Contains(path, "folders") {
			hasTemp = true
		}
	}
	if !hasConfig {
		t.Errorf("support paths do not include the claude configuration directory: %v", p)
	}
	if !hasTemp {
		t.Errorf("support paths do not include a temporary directory: %v", p)
	}
}

func TestStepAllowListReachesThePolicy(t *testing.T) {
	st := spec.Step{ID: "a", Agent: &spec.AgentSpec{Prompt: "p"}, Allow: []string{"/opt/data"}}
	got := policyFor(st, true)
	var found bool
	for _, p := range got.ReadWrite {
		if p == "/opt/data" {
			found = true
		}
	}
	if !found {
		t.Errorf("declared allow path missing from the sandbox policy: %v", got.ReadWrite)
	}
}

func TestShellPolicyOmitsAgentSupportPaths(t *testing.T) {
	st := spec.Step{ID: "a", Run: "echo"}
	got := policyFor(st, false)
	for _, p := range got.ReadWrite {
		if strings.Contains(p, ".claude") {
			t.Errorf("a shell step was granted the agent's configuration: %v", got.ReadWrite)
		}
	}
}

func TestPolicyExpandsHomePrefix(t *testing.T) {
	st := spec.Step{ID: "a", Run: "echo", Allow: []string{"~/.config/tool"}}
	for _, p := range policyFor(st, false).ReadWrite {
		if strings.HasPrefix(p, "~") {
			t.Errorf("home prefix was not expanded: %q", p)
		}
	}
}

// On macOS the CLI's credentials live in the keychain, not in a dotfile. A
// step denied that store is told to log in again on every run.
func TestAgentSupportPathsIncludeCredentialStore(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("credential store path is macOS specific")
	}
	var found bool
	for _, p := range AgentSupportPaths() {
		if strings.Contains(p, "Keychains") {
			found = true
		}
	}
	if !found {
		t.Errorf("support paths omit the keychain: %v", AgentSupportPaths())
	}
}

// When the CLI reports an error in its envelope, that message is the only
// explanation there is: stderr is empty. Losing it leaves a user staring at
// "exit status 1" with nothing to act on.
func TestAgentSurfacesEnvelopeErrorMessage(t *testing.T) {
	bin, _ := filepath.Abs("testdata/fake-claude-error.sh")
	r, _ := agentReq(t, spec.Step{ID: "a", Agent: &spec.AgentSpec{Prompt: "p"}}, "x")
	res, err := NewAgent(sandbox.Confined{}, bin).Run(context.Background(), r)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode == 0 {
		t.Error("ExitCode = 0, want failure")
	}
	if !strings.Contains(res.Stderr, "Not logged in") {
		t.Errorf("Stderr = %q, want the envelope's error message", res.Stderr)
	}
}
