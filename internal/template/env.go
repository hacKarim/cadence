// Package template renders the small, deliberately restricted template
// language used inside workflow files. Every value a step can see is placed
// in the scope explicitly, which is what makes step isolation auditable.
package template

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	gotemplate "text/template"
	"text/template/parse"
)

// ItemView is one element of a fan-out step's result list.
type ItemView struct {
	Index   int
	Input   any
	Result  string
	Outputs map[string]any
}

func (i ItemView) asMap() map[string]any {
	return map[string]any{
		"index":   i.Index,
		"input":   i.Input,
		"result":  i.Result,
		"outputs": orEmpty(i.Outputs),
	}
}

// StepView is everything a downstream step may learn about a completed step.
type StepView struct {
	Result  string
	Outputs map[string]any
	Items   []ItemView
	Dir     string
}

func (s StepView) asMap() map[string]any {
	items := make([]any, len(s.Items))
	for i, it := range s.Items {
		items[i] = it.asMap()
	}
	return map[string]any{
		"result":  s.Result,
		"outputs": orEmpty(s.Outputs),
		"items":   items,
		"dir":     s.Dir,
	}
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// Scope is the complete set of values visible to one template render. Item is
// the current element of a for_each list, which may be a raw JSON value or an
// ItemView produced by an upstream fan-out step.
type Scope struct {
	Steps map[string]StepView
	Item  any
}

func (s Scope) data() map[string]any {
	steps := make(map[string]any, len(s.Steps))
	for id, v := range s.Steps {
		steps[id] = v.asMap()
	}
	d := map[string]any{"steps": steps}
	if s.Item != nil {
		d["item"] = s.Item
	}
	return d
}

// funcs is the entire helper set. Nothing here can read the filesystem, shell
// out, or reach a value that is not already in the scope.
var funcs = gotemplate.FuncMap{
	"json":    toJSON,
	"trim":    func(s string) string { return strings.TrimSpace(s) },
	"lines":   func(s string) []string { return strings.Split(strings.TrimRight(s, "\n"), "\n") },
	"indent":  indent,
	"default": func(fallback, v any) any { return orDefault(fallback, v) },
	"file":    stepFile,
}

func toJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func indent(n int, s string) string {
	pad := strings.Repeat(" ", n)
	out := make([]string, 0)
	for _, l := range strings.Split(s, "\n") {
		out = append(out, pad+l)
	}
	return strings.Join(out, "\n")
}

func orDefault(fallback, v any) any {
	switch x := v.(type) {
	case nil:
		return fallback
	case string:
		if x == "" {
			return fallback
		}
	}
	return v
}

// stepFile resolves a path inside a completed step's output directory. It is a
// helper rather than a method because Go templates can only call exported
// methods, and `.steps.screen.File` reads worse than `file .steps.screen`.
func stepFile(step any, name string) (string, error) {
	m, ok := step.(map[string]any)
	if !ok {
		return "", fmt.Errorf("file: want a step, got %T", step)
	}
	dir, _ := m["dir"].(string)
	if dir == "" {
		return "", fmt.Errorf("file: that step has no output directory yet")
	}
	if strings.Contains(name, "..") || filepath.IsAbs(name) {
		return "", fmt.Errorf("file: %q must be a relative path inside the step directory", name)
	}
	return filepath.Join(dir, name), nil
}

// Render executes tmpl against sc. A reference to something not in the scope
// is an error, never an empty string.
func Render(tmpl string, sc Scope) (string, error) {
	t, err := gotemplate.New("cadence").Funcs(funcs).Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, sc.data()); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// RenderJSON evaluates tmpl as a value rather than as text.
//
// A template like `{{ .steps.screen.outputs.candidates }}` is meant to yield
// the list itself, but text/template would print it with Go's default
// formatting, which is not JSON. So a template that is exactly one action is
// evaluated with json applied to its pipeline. Anything else is rendered
// normally and parsed, which is how a literal JSON document in a workflow
// file still works.
func RenderJSON(tmpl string, sc Scope) (any, error) {
	// Padding from a YAML block scalar or an indented value must not change
	// the meaning of an otherwise identical template.
	trimmed := strings.TrimSpace(tmpl)
	effective := trimmed
	if wrapped, ok := wrapSingleAction(trimmed); ok {
		effective = wrapped
	}
	s, err := Render(effective, sc)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &v); err != nil {
		return nil, fmt.Errorf("expected JSON, got %.80q: %w", s, err)
	}
	// A step that printed a JSON array on stdout has a result which is a
	// string whose contents are that array. Decoding it once more is what
	// the user meant, and the only alternative is an error blaming them for
	// producing exactly the list they produced.
	if str, ok := v.(string); ok {
		var inner any
		if err := json.Unmarshal([]byte(strings.TrimSpace(str)), &inner); err == nil {
			if _, isArray := inner.([]any); isArray {
				return inner, nil
			}
		}
	}
	return v, nil
}

// wrapSingleAction rewrites `{{ pipeline }}` as `{{ pipeline | json }}`. It
// declines when the template is not a lone action, or when the pipeline
// already ends in json, which would double-encode the value.
func wrapSingleAction(tmpl string) (string, bool) {
	trees, err := parse.Parse("wrap", tmpl, "{{", "}}", parseFuncs)
	if err != nil {
		return "", false
	}
	t, ok := trees["wrap"]
	if !ok || t.Root == nil || len(t.Root.Nodes) != 1 {
		return "", false
	}
	action, ok := t.Root.Nodes[0].(*parse.ActionNode)
	if !ok || action.Pipe == nil || len(action.Pipe.Cmds) == 0 {
		return "", false
	}
	// A pipeline that ends in a helper already produces text, and appending
	// json to it would encode that text as a JSON string.
	last := action.Pipe.Cmds[len(action.Pipe.Cmds)-1]
	if len(last.Args) > 0 {
		if _, isHelper := last.Args[0].(*parse.IdentifierNode); isHelper {
			return "", false
		}
	}
	if len(action.Pipe.Decl) > 0 {
		return "", false
	}
	return "{{ " + action.Pipe.String() + " | json }}", true
}
