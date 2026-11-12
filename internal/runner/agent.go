package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/hacKarim/cadence/internal/spec"

	"github.com/hacKarim/cadence/internal/sandbox"
)

// AgentSupportPaths are the directories the claude CLI needs for itself: its
// configuration and session state, and the per-user temporary directory it
// opens before doing anything. A step confined without them exits immediately.
//
// Granting them is a deliberate, documented widening: an agent step with file
// tools can reach its own CLI state. It still cannot reach another step's
// jail, which is the isolation this project is built around.
func AgentSupportPaths() []string {
	var out []string
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".claude"), filepath.Join(home, ".claude.json"))
		if runtime.GOOS == "darwin" {
			// The CLI stores its credentials in the macOS keychain, so a step
			// without this is told to log in again on every run.
			out = append(out,
				filepath.Join(home, "Library", "Keychains"),
				filepath.Join(home, "Library", "Preferences"),
				filepath.Join(home, "Library", "Caches"),
			)
		}
	}
	// The shared temp directory is deliberately not granted: each item has
	// its own inside the jail, and granting the host's would reopen a channel
	// between the very steps the design separates. The CLI's per-user
	// directory is granted because it opens that exact path itself.
	out = append(out, filepath.Join("/tmp", "claude-"+strconv.Itoa(os.Getuid())))
	return out
}

// policyFor builds the sandbox policy for a step: the paths its own spec
// declares, plus the agent CLI's support paths when it is an agent step.
func policyFor(st spec.Step, isAgent bool) sandbox.Policy {
	return policyForBinary(st, "")
}

// policyForBinary is policyFor plus the directory the agent binary lives in.
// An npm, nvm or ~/.local install sits outside every system path the sandbox
// binds, so without this the agent cannot be executed at all.
func policyForBinary(st spec.Step, bin string) sandbox.Policy {
	isAgent := st.IsAgent()
	var paths []string
	if isAgent {
		paths = append(paths, AgentSupportPaths()...)
		if bin != "" {
			if resolved, err := exec.LookPath(bin); err == nil {
				bin = resolved
			}
			if abs, err := filepath.Abs(bin); err == nil {
				paths = append(paths, filepath.Dir(abs))
			}
		}
	}
	for _, a := range st.Allow {
		paths = append(paths, expandHome(a))
	}
	return sandbox.Policy{ReadWrite: paths}
}

func expandHome(p string) string {
	if !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[2:])
}

type agentRunner struct {
	sb  sandbox.Sandbox
	bin string
}

// NewAgent returns a Runner that invokes the claude CLI. bin is the path to
// the binary, which tests replace with a stand-in.
func NewAgent(sb sandbox.Sandbox, bin string) Runner {
	if bin == "" {
		bin = "claude"
	}
	return agentRunner{sb: sb, bin: bin}
}

// envelope is the shape of claude --output-format json.
type envelope struct {
	Result   string  `json:"result"`
	IsError  bool    `json:"is_error"`
	CostUSD  float64 `json:"total_cost_usd"`
	Subtype  string  `json:"subtype"`
	Duration int     `json:"duration_ms"`
	Usage    struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (a agentRunner) argv(r Request) []string {
	args := []string{a.bin, "--print", "--output-format", "json"}
	ag := r.Step.Agent
	if ag != nil {
		if ag.Model != "" {
			args = append(args, "--model", ag.Model)
		}
		if len(ag.Tools) > 0 {
			args = append(args, "--allowed-tools", strings.Join(ag.Tools, ","))
		}
		// Permission is opt-in per step. A scheduler that silently grants
		// unattended agents full write access is a footgun, so the default
		// stays restrictive even though it means some workflows must say so.
		if ag.ElevatedPermissions {
			args = append(args, "--dangerously-skip-permissions")
		}
	}
	return args
}

func (a agentRunner) Run(ctx context.Context, r Request) (*Result, error) {
	ctx, cancel := withTimeout(ctx, r.Timeout)
	defer cancel()

	cmd, err := a.sb.Wrap(ctx, r.Jail, a.argv(r), policyForBinary(r.Step, a.bin))
	if err != nil {
		return nil, err
	}
	cmd.Env = r.Env

	res, err := capture(ctx, cmd, r.Jail, r.Timeout, r.Prompt)
	if err != nil {
		return nil, err
	}
	if res.TimedOut {
		return res, nil
	}

	var env envelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &env); err != nil {
		return nil, fmt.Errorf("claude did not return a JSON envelope (exit %d): %.200q", res.ExitCode, res.Stdout)
	}
	res.Stdout = env.Result
	res.InputTokens = env.Usage.InputTokens
	res.OutputTokens = env.Usage.OutputTokens
	res.CostUSD = env.CostUSD
	if env.IsError {
		if res.ExitCode == 0 {
			res.ExitCode = 1
		}
		// The CLI reports its failures in the envelope and leaves stderr
		// empty, so without this the user sees only an exit code.
		if strings.TrimSpace(res.Stderr) == "" {
			res.Stderr = env.Result
		}
	}
	return res, nil
}
