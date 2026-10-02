// Package spec parses and validates cadence workflow definitions.
package spec

import (
	"fmt"
	"time"
)

// ConcurrencyMode decides what happens when a workflow is triggered while a
// previous run of it is still in flight.
type ConcurrencyMode string

// Supported concurrency modes.
const (
	ConcurrencySkip  ConcurrencyMode = "skip"
	ConcurrencyQueue ConcurrencyMode = "queue"
	ConcurrencyAllow ConcurrencyMode = "allow"
)

// CacheMode decides whether a step item may be served from the step cache.
type CacheMode string

// Supported cache modes.
const (
	CacheNever  CacheMode = "never"
	CacheDaily  CacheMode = "daily"
	CacheAlways CacheMode = "always"
)

// ItemFailureMode decides what a fan-out step does when one item fails.
type ItemFailureMode string

// Supported item failure modes.
const (
	ItemFailureContinue ItemFailureMode = "continue"
	ItemFailureFailFast ItemFailureMode = "fail_fast"
)

// OutputSource names where a declared output is read from.
type OutputSource string

// Supported output sources.
const (
	OutputFromStdout OutputSource = "stdout"
	OutputFromResult OutputSource = "result"
	OutputFromFile   OutputSource = "file"
)

// ParseMode names how raw output text is interpreted.
type ParseMode string

// Supported parse modes.
const (
	ParseRaw   ParseMode = "raw"
	ParseJSON  ParseMode = "json"
	ParseLines ParseMode = "lines"
)

// NotifyEvent names a run outcome that triggers a notification.
type NotifyEvent string

// Supported notification events.
const (
	NotifySuccess NotifyEvent = "success"
	NotifyFailure NotifyEvent = "failure"
)

// Backoff describes the delay growth between retries of a step item.
type Backoff struct {
	Base   time.Duration
	Factor float64
	Max    time.Duration
}

// Defaults holds workflow-wide values inherited by steps that omit them.
type Defaults struct {
	Timeout time.Duration
	Retries int
	Backoff *Backoff
	Workdir string
	Model   string
}

// NotifySpec describes where run outcomes are posted.
type NotifySpec struct {
	On      []NotifyEvent
	Slack   string
	Discord string
}

// Extract describes how an agent's primary result is lifted out of its stdout.
type Extract struct {
	After string
	Regex string
}

// AgentSpec describes an agent step's invocation of the claude CLI.
type AgentSpec struct {
	Prompt              string
	Model               string
	Tools               []string
	Extract             *Extract
	ElevatedPermissions bool
}

// OutputSpec declares a named value lifted out of a step's output.
type OutputSpec struct {
	From   OutputSource
	Path   string
	As     ParseMode
	Select string
	Regex  string
}

// InputSpec declares one file staged into a step's jail. Exactly one of
// Content (rendered template text) and Copy (a path template) is set.
type InputSpec struct {
	Content string
	Copy    string
}

// Step is a single node of a workflow. Exactly one of Run and Agent is set.
type Step struct {
	ID          string
	Needs       []string
	Run         string
	Agent       *AgentSpec
	ForEach     string
	WhenItem    string
	If          string
	MaxParallel int
	Cache       CacheMode
	Isolate     bool
	Workdir     string
	Env         map[string]string
	// Allow lists paths outside the jail this step may read and write. It is
	// how a workflow grants a step access to something the machine provides,
	// such as a tool's configuration directory, without widening every step.
	Allow         []string
	Inputs        map[string]InputSpec
	Outputs       map[string]OutputSpec
	Timeout       time.Duration
	Retries       int
	Backoff       *Backoff
	OnItemFailure ItemFailureMode
}

// IsAgent reports whether this step invokes an agent rather than a shell command.
func (s *Step) IsAgent() bool { return s.Agent != nil }

// Workflow is one parsed cadence workflow file.
type Workflow struct {
	Name           string
	Description    string
	Schedule       string
	Timezone       string
	Concurrency    ConcurrencyMode
	Catchup        bool
	RequireSandbox bool
	Defaults       Defaults
	Notify         *NotifySpec
	Steps          []Step
}

// Step returns the step with the given id.
func (w *Workflow) Step(id string) (*Step, bool) {
	for i := range w.Steps {
		if w.Steps[i].ID == id {
			return &w.Steps[i], true
		}
	}
	return nil, false
}

func enumError(field, got string, allowed ...string) error {
	return fmt.Errorf("%s: unknown value %q, allowed: %v", field, got, allowed)
}
