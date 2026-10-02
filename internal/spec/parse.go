package spec

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// Parse decodes a workflow document and applies documented defaults. It does
// not validate the result; call Workflow.Validate for that.
func Parse(data []byte) (*Workflow, error) {
	var wf Workflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		return nil, err
	}
	wf.applyDefaults()
	return &wf, nil
}

func (w *Workflow) applyDefaults() {
	if w.Concurrency == "" {
		w.Concurrency = ConcurrencySkip
	}
	for i := range w.Steps {
		s := &w.Steps[i]
		if s.Cache == "" {
			s.Cache = CacheNever
		}
		if s.OnItemFailure == "" {
			s.OnItemFailure = ItemFailureContinue
		}
		if s.Timeout == 0 {
			s.Timeout = w.Defaults.Timeout
		}
		if s.Retries == 0 {
			s.Retries = w.Defaults.Retries
		}
		if s.Backoff == nil {
			s.Backoff = w.Defaults.Backoff
		}
		if s.Workdir == "" {
			s.Workdir = w.Defaults.Workdir
		}
		if s.Agent != nil && s.Agent.Model == "" {
			s.Agent.Model = w.Defaults.Model
		}
	}
}

// --- enums -------------------------------------------------------------

func (m *ConcurrencyMode) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	switch ConcurrencyMode(s) {
	case ConcurrencySkip, ConcurrencyQueue, ConcurrencyAllow:
		*m = ConcurrencyMode(s)
		return nil
	}
	return enumError("concurrency", s, "skip", "queue", "allow")
}

func (m *CacheMode) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	switch CacheMode(s) {
	case CacheNever, CacheDaily, CacheAlways:
		*m = CacheMode(s)
		return nil
	}
	return enumError("cache", s, "never", "daily", "always")
}

func (m *ItemFailureMode) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	switch ItemFailureMode(s) {
	case ItemFailureContinue, ItemFailureFailFast:
		*m = ItemFailureMode(s)
		return nil
	}
	return enumError("on_item_failure", s, "continue", "fail_fast")
}

func (m *OutputSource) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	switch OutputSource(s) {
	case OutputFromStdout, OutputFromResult, OutputFromFile:
		*m = OutputSource(s)
		return nil
	}
	return enumError("from", s, "stdout", "result", "file")
}

func (m *ParseMode) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	switch ParseMode(s) {
	case ParseRaw, ParseJSON, ParseLines:
		*m = ParseMode(s)
		return nil
	}
	return enumError("as", s, "raw", "json", "lines")
}

func (m *NotifyEvent) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	switch NotifyEvent(s) {
	case NotifySuccess, NotifyFailure:
		*m = NotifyEvent(s)
		return nil
	}
	return enumError("notify.on", s, "success", "failure")
}

// --- shorthand forms ---------------------------------------------------

// UnmarshalYAML accepts both the scalar shorthand (content) and the mapping
// form ({copy: ...}).
func (i *InputSpec) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		return n.Decode(&i.Content)
	}
	var raw struct {
		Content string `yaml:"content"`
		Copy    string `yaml:"copy"`
	}
	if err := n.Decode(&raw); err != nil {
		return err
	}
	if raw.Content != "" && raw.Copy != "" {
		return fmt.Errorf("input: set either content or copy, not both")
	}
	i.Content, i.Copy = raw.Content, raw.Copy
	return nil
}

// UnmarshalYAML decodes an output declaration, defaulting As to raw.
func (o *OutputSpec) UnmarshalYAML(n *yaml.Node) error {
	var raw struct {
		From   OutputSource `yaml:"from"`
		Path   string       `yaml:"path"`
		As     ParseMode    `yaml:"as"`
		Select string       `yaml:"select"`
		Regex  string       `yaml:"regex"`
	}
	if err := n.Decode(&raw); err != nil {
		return err
	}
	if raw.As == "" {
		raw.As = ParseRaw
	}
	o.From, o.Path, o.As, o.Select, o.Regex = raw.From, raw.Path, raw.As, raw.Select, raw.Regex
	return nil
}

// --- duration-bearing structs ------------------------------------------

func parseDur(field, v string) (time.Duration, error) {
	if v == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a duration (use forms like 30s, 20m, 2h)", field, v)
	}
	return d, nil
}

// UnmarshalYAML decodes a backoff block, converting its duration strings.
func (b *Backoff) UnmarshalYAML(n *yaml.Node) error {
	var raw struct {
		Base   string  `yaml:"base"`
		Factor float64 `yaml:"factor"`
		Max    string  `yaml:"max"`
	}
	if err := n.Decode(&raw); err != nil {
		return err
	}
	var err error
	if b.Base, err = parseDur("backoff.base", raw.Base); err != nil {
		return err
	}
	if b.Max, err = parseDur("backoff.max", raw.Max); err != nil {
		return err
	}
	b.Factor = raw.Factor
	return nil
}

// UnmarshalYAML decodes workflow defaults, converting its duration strings.
func (d *Defaults) UnmarshalYAML(n *yaml.Node) error {
	var raw struct {
		Timeout string   `yaml:"timeout"`
		Retries int      `yaml:"retries"`
		Backoff *Backoff `yaml:"backoff"`
		Workdir string   `yaml:"workdir"`
		Model   string   `yaml:"model"`
	}
	if err := n.Decode(&raw); err != nil {
		return err
	}
	t, err := parseDur("defaults.timeout", raw.Timeout)
	if err != nil {
		return err
	}
	d.Timeout, d.Retries, d.Backoff, d.Workdir, d.Model = t, raw.Retries, raw.Backoff, raw.Workdir, raw.Model
	return nil
}

// UnmarshalYAML decodes a step, converting its duration strings and mapping
// the snake_case document keys onto the Go field names.
func (s *Step) UnmarshalYAML(n *yaml.Node) error {
	var raw struct {
		ID            string                `yaml:"id"`
		Needs         []string              `yaml:"needs"`
		Run           string                `yaml:"run"`
		Agent         *AgentSpec            `yaml:"agent"`
		ForEach       string                `yaml:"for_each"`
		WhenItem      string                `yaml:"when_item"`
		If            string                `yaml:"if"`
		MaxParallel   int                   `yaml:"max_parallel"`
		Cache         CacheMode             `yaml:"cache"`
		Isolate       bool                  `yaml:"isolate"`
		Workdir       string                `yaml:"workdir"`
		Env           map[string]string     `yaml:"env"`
		Allow         []string              `yaml:"allow"`
		Inputs        map[string]InputSpec  `yaml:"inputs"`
		Outputs       map[string]OutputSpec `yaml:"outputs"`
		Timeout       string                `yaml:"timeout"`
		Retries       int                   `yaml:"retries"`
		Backoff       *Backoff              `yaml:"backoff"`
		OnItemFailure ItemFailureMode       `yaml:"on_item_failure"`
	}
	if err := n.Decode(&raw); err != nil {
		return err
	}
	t, err := parseDur(fmt.Sprintf("step %q: timeout", raw.ID), raw.Timeout)
	if err != nil {
		return err
	}
	s.ID, s.Needs, s.Run, s.Agent = raw.ID, raw.Needs, raw.Run, raw.Agent
	s.ForEach, s.WhenItem, s.If = raw.ForEach, raw.WhenItem, raw.If
	s.MaxParallel, s.Cache, s.Isolate, s.Workdir = raw.MaxParallel, raw.Cache, raw.Isolate, raw.Workdir
	s.Env, s.Inputs, s.Outputs, s.Allow = raw.Env, raw.Inputs, raw.Outputs, raw.Allow
	s.Timeout, s.Retries, s.Backoff, s.OnItemFailure = t, raw.Retries, raw.Backoff, raw.OnItemFailure
	return nil
}

// UnmarshalYAML decodes an agent block, mapping its snake_case keys.
func (a *AgentSpec) UnmarshalYAML(n *yaml.Node) error {
	var raw struct {
		Prompt              string   `yaml:"prompt"`
		Model               string   `yaml:"model"`
		Tools               []string `yaml:"tools"`
		Extract             *Extract `yaml:"extract"`
		ElevatedPermissions bool     `yaml:"elevated_permissions"`
	}
	if err := n.Decode(&raw); err != nil {
		return err
	}
	a.Prompt, a.Model, a.Tools, a.Extract, a.ElevatedPermissions = raw.Prompt, raw.Model, raw.Tools, raw.Extract, raw.ElevatedPermissions
	return nil
}

// UnmarshalYAML decodes a workflow, mapping its snake_case keys.
func (w *Workflow) UnmarshalYAML(n *yaml.Node) error {
	var raw struct {
		Name           string          `yaml:"name"`
		Description    string          `yaml:"description"`
		Schedule       string          `yaml:"schedule"`
		Timezone       string          `yaml:"timezone"`
		Concurrency    ConcurrencyMode `yaml:"concurrency"`
		Catchup        bool            `yaml:"catchup"`
		RequireSandbox bool            `yaml:"require_sandbox"`
		Defaults       Defaults        `yaml:"defaults"`
		Notify         *NotifySpec     `yaml:"notify"`
		Steps          []Step          `yaml:"steps"`
	}
	if err := n.Decode(&raw); err != nil {
		return err
	}
	w.Name, w.Description, w.Schedule, w.Timezone = raw.Name, raw.Description, raw.Schedule, raw.Timezone
	w.Concurrency, w.Catchup, w.RequireSandbox = raw.Concurrency, raw.Catchup, raw.RequireSandbox
	w.Defaults, w.Notify, w.Steps = raw.Defaults, raw.Notify, raw.Steps
	return nil
}

// UnmarshalYAML decodes a notify block. The "on" key is quoted here because
// YAML 1.1 readers may treat a bare on as a boolean.
func (ns *NotifySpec) UnmarshalYAML(n *yaml.Node) error {
	var raw struct {
		On      []NotifyEvent `yaml:"on"`
		Slack   string        `yaml:"slack"`
		Discord string        `yaml:"discord"`
	}
	if err := n.Decode(&raw); err != nil {
		return err
	}
	ns.On, ns.Slack, ns.Discord = raw.On, raw.Slack, raw.Discord
	return nil
}
