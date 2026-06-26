package engine

import (
	"fmt"
	"strings"

	"github.com/hacKarim/cadence/internal/spec"
	"github.com/hacKarim/cadence/internal/template"
)

// expand resolves a step's for_each into the list of items to run. A step
// without for_each is a fan-out of exactly one nil item, so the engine keeps a
// single execution path.
func (e *Engine) expand(st *spec.Step, sc template.Scope) ([]any, error) {
	if st.ForEach == "" {
		return []any{nil}, nil
	}
	v, err := template.RenderJSON(st.ForEach, sc)
	if err != nil {
		return nil, fmt.Errorf("step %q: for_each did not produce a list: %w", st.ID, err)
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("step %q: for_each must produce a list, got %s", st.ID, jsonKind(v))
	}
	return arr, nil
}

// jsonKind names a decoded JSON value for an error message, so a user reading
// a failed run learns what their for_each actually produced.
func jsonKind(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		return "a boolean"
	case float64:
		return "a number"
	case string:
		return "a string"
	case map[string]any:
		return "an object"
	default:
		return fmt.Sprintf("%T", t)
	}
}

// evalBool renders a condition template and interprets the result. Anything
// other than a clear true is false, and a template error is an error.
func evalBool(tmpl string, sc template.Scope) (bool, error) {
	s, err := template.Render(tmpl, sc)
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(s) {
	case "true":
		return true, nil
	case "false", "", "<no value>":
		return false, nil
	default:
		return false, nil
	}
}
