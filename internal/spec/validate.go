package spec

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// identifierRe is the permitted shape of a step id: it must be usable as a
// directory name and as a template key.
var identifierRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)

// cronParser accepts the 6-field form documented for schedules, where the
// leading field is seconds.
var cronParser = cron.NewParser(
	cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

// ValidationError is a single problem found in a workflow document.
type ValidationError struct {
	Step  string
	Field string
	Msg   string
}

func (e ValidationError) Error() string {
	if e.Step == "" {
		return fmt.Sprintf("%s: %s", e.Field, e.Msg)
	}
	return fmt.Sprintf("step %q: %s: %s", e.Step, e.Field, e.Msg)
}

// ValidationErrors is every problem found in one pass, so a user fixing a
// workflow sees the whole list rather than one error per run.
type ValidationErrors []ValidationError

func (e ValidationErrors) Error() string {
	parts := make([]string, len(e))
	for i, v := range e {
		parts[i] = v.Error()
	}
	return strings.Join(parts, "\n")
}

// Validate checks the whole workflow and returns every problem found.
func (w *Workflow) Validate() error {
	var errs ValidationErrors
	add := func(step, field, msg string) {
		errs = append(errs, ValidationError{Step: step, Field: field, Msg: msg})
	}

	if strings.TrimSpace(w.Name) == "" {
		add("", "name", "must not be empty")
	}
	if w.Schedule != "" {
		if _, err := cronParser.Parse(w.Schedule); err != nil {
			add("", "schedule", fmt.Sprintf("%q is not a 6-field cron expression (second minute hour day month weekday): %v", w.Schedule, err))
		}
	}
	if w.Timezone != "" {
		if _, err := time.LoadLocation(w.Timezone); err != nil {
			add("", "timezone", fmt.Sprintf("unknown timezone %q", w.Timezone))
		}
	}
	if len(w.Steps) == 0 {
		add("", "steps", "a workflow needs at least one step")
	}

	seen := map[string]bool{}
	for i := range w.Steps {
		s := &w.Steps[i]
		if s.ID == "" {
			add("", "id", fmt.Sprintf("step %d has an empty id", i))
			continue
		}
		if !identifierRe.MatchString(s.ID) {
			add(s.ID, "id", fmt.Sprintf("%q must start with a letter and contain only letters, digits, underscore and hyphen", s.ID))
		}
		if seen[s.ID] {
			add(s.ID, "id", fmt.Sprintf("duplicate step id %q", s.ID))
		}
		seen[s.ID] = true
	}

	for i := range w.Steps {
		s := &w.Steps[i]
		w.validateStep(s, seen, add)
	}

	if cyc := findCycle(w.Steps); len(cyc) > 0 {
		add("", "needs", "dependency cycle: "+strings.Join(cyc, " -> "))
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}

func (w *Workflow) validateStep(s *Step, known map[string]bool, add func(step, field, msg string)) {
	switch {
	case s.Run != "" && s.Agent != nil:
		add(s.ID, "run", "a step sets either run or agent, never both")
	case s.Run == "" && s.Agent == nil:
		add(s.ID, "run", "a step must set either run or agent")
	}
	if s.Agent != nil && strings.TrimSpace(s.Agent.Prompt) == "" {
		add(s.ID, "agent.prompt", "must not be empty")
	}
	for _, n := range s.Needs {
		if n == s.ID {
			add(s.ID, "needs", fmt.Sprintf("step %q cannot depend on itself", s.ID))
			continue
		}
		if !known[n] {
			add(s.ID, "needs", fmt.Sprintf("unknown step %q", n))
		}
	}
	if s.WhenItem != "" && s.ForEach == "" {
		add(s.ID, "when_item", "when_item requires for_each on the same step")
	}
	if s.MaxParallel < 0 {
		add(s.ID, "max_parallel", "must not be negative")
	}
	if s.Retries < 0 {
		add(s.ID, "retries", "must not be negative")
	}
	for name, o := range s.Outputs {
		field := "outputs." + name
		switch o.From {
		case OutputFromFile:
			if o.Path == "" {
				add(s.ID, field, "from: file requires path")
			}
		case OutputFromStdout, OutputFromResult:
			if o.Path != "" {
				add(s.ID, field, fmt.Sprintf("path is only meaningful with from: file, not from: %s", o.From))
			}
		case "":
			add(s.ID, field, "from is required (stdout, result or file)")
		}
		if o.Select != "" && o.As != ParseJSON {
			add(s.ID, field, "select requires as: json")
		}
		if o.Select != "" && o.Regex != "" {
			add(s.ID, field, "set either select or regex, not both")
		}
	}
	for name, in := range s.Inputs {
		if in.Content == "" && in.Copy == "" {
			add(s.ID, "inputs."+name, "set either content or copy")
		}
	}
	if s.Agent != nil && s.Agent.Extract != nil {
		e := s.Agent.Extract
		if e.After == "" && e.Regex == "" {
			add(s.ID, "agent.extract", "set either after or regex")
		}
		if e.After != "" && e.Regex != "" {
			add(s.ID, "agent.extract", "set either after or regex, not both")
		}
		if e.Regex != "" {
			if _, err := regexp.Compile(e.Regex); err != nil {
				add(s.ID, "agent.extract.regex", err.Error())
			}
		}
	}
}

// findCycle returns the steps forming a dependency cycle, in order, or nil.
func findCycle(steps []Step) []string {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	colour := map[string]int{}
	deps := map[string][]string{}
	for _, s := range steps {
		colour[s.ID] = white
		deps[s.ID] = s.Needs
	}

	var stack []string
	var walk func(id string) []string
	walk = func(id string) []string {
		colour[id] = grey
		stack = append(stack, id)
		for _, d := range deps[id] {
			if _, ok := colour[d]; !ok {
				continue // unknown step, already reported
			}
			switch colour[d] {
			case grey:
				for i, s := range stack {
					if s == d {
						return append(append([]string{}, stack[i:]...), d)
					}
				}
			case white:
				if c := walk(d); c != nil {
					return c
				}
			}
		}
		stack = stack[:len(stack)-1]
		colour[id] = black
		return nil
	}

	for _, s := range steps {
		if colour[s.ID] == white {
			if c := walk(s.ID); c != nil {
				return c
			}
		}
	}
	return nil
}
