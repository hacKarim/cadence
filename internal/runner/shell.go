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

	cmd, err := s.sb.Wrap(ctx, r.Jail, []string{"/bin/sh", "-c", r.command()})
	if err != nil {
		return nil, err
	}
	cmd.Env = r.Env
	return capture(ctx, cmd, r.Jail, r.Timeout, "")
}

// command is the shell command to run. The engine renders any template in it
// before the request is built; the raw step text is only a fallback for tests.
func (r Request) command() string {
	if r.Script != "" {
		return r.Script
	}
	return r.Step.Run
}
