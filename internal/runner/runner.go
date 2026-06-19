// Package runner executes one step item: either a shell command or an agent
// invocation. The engine depends only on the Runner interface, so it never
// knows which kind it is driving, and tests substitute a fake.
package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/hacKarim/cadence/internal/jail"
	"github.com/hacKarim/cadence/internal/spec"
)

// MaxCaptureBytes caps how much of a step's output is held in memory and
// recorded in the database. The full stream always reaches the log file.
const MaxCaptureBytes = 1 << 20

// Request is everything needed to run one step item.
type Request struct {
	Step spec.Step
	Jail *jail.Jail
	// Script is the rendered shell command for a run step.
	Script string
	// Prompt is the rendered prompt for an agent step.
	Prompt  string
	Env     []string
	Timeout time.Duration
}

// Result is what one step item produced. For an agent step Stdout holds the
// model's result text, not the raw JSON envelope; the envelope is in the log.
type Result struct {
	Stdout       string
	Stderr       string
	ExitCode     int
	Duration     time.Duration
	InputTokens  int
	OutputTokens int
	CostUSD      float64
	TimedOut     bool
}

// Runner executes a request. A non-nil error means the step could not be run
// at all; a step that ran and failed reports a non-zero ExitCode instead.
type Runner interface {
	Run(ctx context.Context, r Request) (*Result, error)
}

// cappedWriter keeps the first MaxCaptureBytes of a stream in memory and
// counts the rest, so a runaway step cannot exhaust memory.
type cappedWriter struct {
	buf     bytes.Buffer
	total   int
	dropped int
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	c.total += len(p)
	if room := MaxCaptureBytes - c.buf.Len(); room > 0 {
		if len(p) <= room {
			c.buf.Write(p)
		} else {
			c.buf.Write(p[:room])
			c.dropped += len(p) - room
		}
	} else {
		c.dropped += len(p)
	}
	return len(p), nil
}

func (c *cappedWriter) String() string {
	if c.dropped == 0 {
		return c.buf.String()
	}
	return fmt.Sprintf("%s\n... truncated, %d more bytes in the step log ...\n", c.buf.String(), c.dropped)
}

// capture runs cmd, streaming both streams to the jail's log files while
// keeping a capped copy in memory.
func capture(ctx context.Context, cmd *exec.Cmd, j *jail.Jail, timeout time.Duration, stdin string) (*Result, error) {
	outFile, err := os.Create(filepath.Join(j.Meta, "stdout.log"))
	if err != nil {
		return nil, err
	}
	defer outFile.Close()
	errFile, err := os.Create(filepath.Join(j.Meta, "stderr.log"))
	if err != nil {
		return nil, err
	}
	defer errFile.Close()

	var outCap, errCap cappedWriter
	cmd.Stdout = io.MultiWriter(outFile, &outCap)
	cmd.Stderr = io.MultiWriter(errFile, &errCap)
	if stdin != "" {
		cmd.Stdin = bytes.NewBufferString(stdin)
	}

	start := time.Now()
	runErr := cmd.Run()
	res := &Result{
		Stdout:   outCap.String(),
		Stderr:   errCap.String(),
		Duration: time.Since(start),
	}
	if ctx.Err() == context.DeadlineExceeded {
		res.TimedOut = true
	}
	if runErr != nil {
		var ee *exec.ExitError
		if errorsAs(runErr, &ee) {
			res.ExitCode = ee.ExitCode()
		} else if res.TimedOut {
			res.ExitCode = -1
		} else {
			return nil, runErr
		}
	}
	return res, nil
}

// withTimeout derives a context that carries the request's timeout.
func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, d)
}
