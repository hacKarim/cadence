package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hacKarim/cadence/internal/sandbox"
)

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

	cmd, err := a.sb.Wrap(ctx, r.Jail, a.argv(r))
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
	if env.IsError && res.ExitCode == 0 {
		res.ExitCode = 1
	}
	return res, nil
}
