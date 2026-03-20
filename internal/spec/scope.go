package spec

import (
	"fmt"
	"sort"

	"github.com/hacKarim/cadence/internal/template"
)

// templateFields returns every template-bearing field of a step, paired with
// the field name used in error messages.
func templateFields(s *Step) map[string]string {
	f := map[string]string{}
	if s.Run != "" {
		f["run"] = s.Run
	}
	if s.ForEach != "" {
		f["for_each"] = s.ForEach
	}
	if s.WhenItem != "" {
		f["when_item"] = s.WhenItem
	}
	if s.If != "" {
		f["if"] = s.If
	}
	if s.Agent != nil && s.Agent.Prompt != "" {
		f["agent.prompt"] = s.Agent.Prompt
	}
	for name, in := range s.Inputs {
		if in.Content != "" {
			f["inputs."+name] = in.Content
		}
		if in.Copy != "" {
			f["inputs."+name+".copy"] = in.Copy
		}
	}
	for name, v := range s.Env {
		f["env."+name] = v
	}
	return f
}

// closure returns every step reachable through needs from id, transitively.
func closure(byID map[string]*Step, id string) map[string]bool {
	out := map[string]bool{}
	var walk func(string)
	walk = func(cur string) {
		s, ok := byID[cur]
		if !ok {
			return
		}
		for _, n := range s.Needs {
			if out[n] {
				continue
			}
			out[n] = true
			walk(n)
		}
	}
	walk(id)
	return out
}

// validateScopes proves, before anything runs, that each step's templates can
// only reach steps its needs list grants it. Without this check needs would be
// decorative and the isolation guarantee would rest on convention.
func (w *Workflow) validateScopes(add func(step, field, msg string)) {
	byID := map[string]*Step{}
	for i := range w.Steps {
		byID[w.Steps[i].ID] = &w.Steps[i]
	}

	for i := range w.Steps {
		s := &w.Steps[i]
		if s.ID == "" {
			continue
		}
		allowed := closure(byID, s.ID)

		fields := templateFields(s)
		names := make([]string, 0, len(fields))
		for n := range fields {
			names = append(names, n)
		}
		sort.Strings(names)

		for _, field := range names {
			refs, usesItem, err := template.Refs(fields[field])
			if err != nil {
				add(s.ID, field, fmt.Sprintf("template does not parse: %v", err))
				continue
			}
			sort.Strings(refs)
			for _, r := range refs {
				if _, known := byID[r]; !known {
					add(s.ID, field, fmt.Sprintf("references unknown step %q", r))
					continue
				}
				if !allowed[r] {
					add(s.ID, field, fmt.Sprintf("references step %q, which is not in the needs closure of %q; add it to needs", r, s.ID))
				}
			}
			if usesItem {
				switch {
				case field == "for_each":
					add(s.ID, field, "for_each cannot reference .item: it is what produces the items")
				case s.ForEach == "":
					add(s.ID, field, "references .item but the step has no for_each")
				}
			}
		}
	}
}
