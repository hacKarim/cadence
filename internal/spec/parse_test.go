package spec

import (
	"testing"
	"time"
)

const minimalYAML = `
name: tiny
steps:
  - id: hello
    run: echo hi
`

func TestParseMinimalWorkflow(t *testing.T) {
	wf, err := Parse([]byte(minimalYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if wf.Name != "tiny" {
		t.Errorf("Name = %q, want %q", wf.Name, "tiny")
	}
	if len(wf.Steps) != 1 {
		t.Fatalf("len(Steps) = %d, want 1", len(wf.Steps))
	}
	if wf.Steps[0].ID != "hello" {
		t.Errorf("Steps[0].ID = %q, want %q", wf.Steps[0].ID, "hello")
	}
	if wf.Steps[0].Run != "echo hi" {
		t.Errorf("Steps[0].Run = %q, want %q", wf.Steps[0].Run, "echo hi")
	}
}

func TestParseAppliesDefaults(t *testing.T) {
	wf, err := Parse([]byte(minimalYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if wf.Concurrency != ConcurrencySkip {
		t.Errorf("Concurrency = %q, want %q", wf.Concurrency, ConcurrencySkip)
	}
	if wf.Steps[0].Cache != CacheNever {
		t.Errorf("Cache = %q, want %q", wf.Steps[0].Cache, CacheNever)
	}
	if wf.Steps[0].OnItemFailure != ItemFailureContinue {
		t.Errorf("OnItemFailure = %q, want %q", wf.Steps[0].OnItemFailure, ItemFailureContinue)
	}
}

func TestParseInputShorthand(t *testing.T) {
	wf, err := Parse([]byte(`
name: w
steps:
  - id: a
    run: /bin/true
    inputs:
      report.md: '{{ .item.result }}'
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := wf.Steps[0].Inputs["report.md"]
	if got.Content != "{{ .item.result }}" {
		t.Errorf("Content = %q, want %q", got.Content, "{{ .item.result }}")
	}
	if got.Copy != "" {
		t.Errorf("Copy = %q, want empty", got.Copy)
	}
}

func TestParseInputCopyForm(t *testing.T) {
	wf, err := Parse([]byte(`
name: w
steps:
  - id: a
    run: /bin/true
    inputs:
      data.json: { copy: '{{ file .steps.screen "candidates.json" }}' }
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := wf.Steps[0].Inputs["data.json"]
	if got.Copy == "" {
		t.Errorf("Copy is empty, want the template")
	}
	if got.Content != "" {
		t.Errorf("Content = %q, want empty", got.Content)
	}
}

func TestParseDurationFields(t *testing.T) {
	wf, err := Parse([]byte(`
name: w
defaults:
  timeout: 20m
  backoff: { base: 2s, factor: 2, max: 60s }
steps:
  - id: a
    run: /bin/true
    timeout: 90s
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if wf.Defaults.Timeout != 20*time.Minute {
		t.Errorf("Defaults.Timeout = %v, want 20m", wf.Defaults.Timeout)
	}
	if wf.Defaults.Backoff == nil {
		t.Fatal("Defaults.Backoff is nil")
	}
	if wf.Defaults.Backoff.Base != 2*time.Second {
		t.Errorf("Backoff.Base = %v, want 2s", wf.Defaults.Backoff.Base)
	}
	if wf.Defaults.Backoff.Max != 60*time.Second {
		t.Errorf("Backoff.Max = %v, want 60s", wf.Defaults.Backoff.Max)
	}
	if wf.Defaults.Backoff.Factor != 2 {
		t.Errorf("Backoff.Factor = %v, want 2", wf.Defaults.Backoff.Factor)
	}
	if wf.Steps[0].Timeout != 90*time.Second {
		t.Errorf("Steps[0].Timeout = %v, want 90s", wf.Steps[0].Timeout)
	}
}

func TestParseRejectsUnknownEnumValue(t *testing.T) {
	_, err := Parse([]byte(`
name: w
steps:
  - id: a
    run: /bin/true
    cache: hourly
`))
	if err == nil {
		t.Fatal("Parse succeeded, want an error naming the allowed values")
	}
}

const researchFunnelYAML = `
name: research-funnel
description: Screen deterministically, analyse in parallel, challenge, decide.
schedule: "0 0 7 * * 1-5"
timezone: Europe/Paris
concurrency: skip
catchup: false

defaults:
  timeout: 20m
  retries: 1
  backoff: { base: 2s, factor: 2, max: 60s }

notify:
  on: [failure]
  slack: ${SLACK_WEBHOOK}

steps:
  - id: screen
    run: ./screen.py --out candidates.json
    workdir: ./tools
    outputs:
      candidates:
        from: file
        path: candidates.json
        as: json

  - id: analyse
    needs: [screen]
    for_each: '{{ .steps.screen.outputs.candidates }}'
    max_parallel: 4
    cache: daily
    isolate: true
    inputs:
      figures.json: '{{ .item | json }}'
    agent:
      prompt: |
        Read figures.json and analyse {{ .item.ticker }}.
        End your report after a line containing =====REPORT=====
      extract: { after: "=====REPORT=====" }
      tools: [Read, Write, WebSearch]
    outputs:
      verdict:
        from: result
        as: json
        select: "$.verdict"

  - id: challenge
    needs: [analyse]
    for_each: '{{ .steps.analyse.items }}'
    when_item: '{{ eq .item.outputs.verdict "undervalued" }}'
    isolate: true
    inputs:
      report.md: '{{ .item.result }}'
    agent:
      prompt: |
        You are the sceptic. Read report.md.
      tools: [Read]

  - id: publish
    needs: [challenge]
    if: '{{ gt (len .steps.challenge.items) 0 }}'
    run: ./publish.sh
`

func TestParseResearchFunnelExample(t *testing.T) {
	wf, err := Parse([]byte(researchFunnelYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(wf.Steps) != 4 {
		t.Fatalf("len(Steps) = %d, want 4", len(wf.Steps))
	}
	if wf.Steps[1].Agent == nil {
		t.Fatal("Steps[1].Agent is nil, want an agent step")
	}
	if wf.Steps[1].Agent.Extract == nil || wf.Steps[1].Agent.Extract.After != "=====REPORT=====" {
		t.Errorf("Steps[1].Agent.Extract.After not parsed")
	}
	if wf.Steps[1].Cache != CacheDaily {
		t.Errorf("Steps[1].Cache = %q, want daily", wf.Steps[1].Cache)
	}
	if wf.Steps[1].MaxParallel != 4 {
		t.Errorf("Steps[1].MaxParallel = %d, want 4", wf.Steps[1].MaxParallel)
	}
	if got := wf.Steps[0].Outputs["candidates"]; got.From != OutputFromFile || got.Path != "candidates.json" || got.As != ParseJSON {
		t.Errorf("screen outputs.candidates = %+v", got)
	}
	if got := wf.Steps[1].Outputs["verdict"]; got.From != OutputFromResult || got.Select != "$.verdict" {
		t.Errorf("analyse outputs.verdict = %+v", got)
	}
	if wf.Notify == nil {
		t.Fatal("Notify is nil")
	}
	if len(wf.Notify.On) != 1 || wf.Notify.On[0] != NotifyFailure {
		t.Errorf("Notify.On = %v, want [failure]", wf.Notify.On)
	}
}

func TestParseStepAllowList(t *testing.T) {
	wf, err := Parse([]byte(`
name: w
steps:
  - id: a
    run: /bin/true
    allow:
      - ~/.config/tool
      - /opt/data
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := wf.Steps[0].Allow; len(got) != 2 || got[1] != "/opt/data" {
		t.Errorf("Allow = %v", got)
	}
}

// The README documents ${VAR} expansion and both shipped examples rely on it
// for their webhook URLs. Without it the webhook POSTs to the literal string.
func TestParseExpandsEnvReferencesInNotifyAndEnv(t *testing.T) {
	t.Setenv("CADENCE_TEST_HOOK", "https://hooks.example/abc")
	t.Setenv("CADENCE_TEST_REGION", "eu-west-1")
	wf, err := Parse([]byte(`
name: w
notify:
  on: [failure]
  slack: ${CADENCE_TEST_HOOK}
steps:
  - id: a
    run: /bin/true
    env:
      REGION: ${CADENCE_TEST_REGION}
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if wf.Notify.Slack != "https://hooks.example/abc" {
		t.Errorf("notify.slack = %q, want the expanded value", wf.Notify.Slack)
	}
	if wf.Steps[0].Env["REGION"] != "eu-west-1" {
		t.Errorf("env.REGION = %q, want the expanded value", wf.Steps[0].Env["REGION"])
	}
}

// Shell commands are full of dollar signs. Expanding them would corrupt every
// command substitution in every workflow.
func TestParseDoesNotExpandInsideRunCommands(t *testing.T) {
	t.Setenv("HOME", "/home/someone")
	wf, err := Parse([]byte("name: w\nsteps:\n  - id: a\n    run: 'git log \"$(git describe)\"..HEAD ${HOME}'\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if wf.Steps[0].Run != `git log "$(git describe)"..HEAD ${HOME}` {
		t.Errorf("run was rewritten: %q", wf.Steps[0].Run)
	}
}

func TestParseLeavesUnsetVariablesVisible(t *testing.T) {
	wf, err := Parse([]byte("name: w\nnotify:\n  on: [failure]\n  slack: ${CADENCE_DEFINITELY_UNSET}\nsteps:\n  - id: a\n    run: /bin/true\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if wf.Notify.Slack != "" {
		t.Errorf("slack = %q, want empty so an unset variable is not posted as a literal", wf.Notify.Slack)
	}
}
