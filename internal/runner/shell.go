package runner

import (
	"context"

	"github.com/hacKarim/cadence/internal/sandbox"
)

type shellRunner struct{ sb sandbox.Sandbox }

// NewShell returns a Runner that executes a step's run command through the
// given sandbox backend.
func NewShell(sb sandbox.Sandbox) Runner { return shellRunner{sb: sb} }

func (s shellRunner) Run(ctx context.Context, r Request) (*Result, error) {
	ctx, cancel := withTimeout(ctx, r.Timeout)
	defer cancel()

	cmd, err := s.sb.Wrap(ctx, r.Jail, []string{"/bin/sh", "-c", r.Command()})
	if err != nil {
		return nil, err
	}
	cmd.Env = r.Env
	return capture(ctx, cmd, r.Jail, r.Timeout, "")
}

// Command is the shell command to run. The engine has already rendered any
// template in it.
func (r Request) Command() string {
	if r.Prompt != "" {
		return r.Prompt
	}
	return r.Step.Run
}
