package template

import (
	"sort"
	"text/template/parse"
)

// parseFuncs is the helper set plus the names text/template defines itself.
// parse.Parse validates function names against the maps it is given, so the
// builtins have to be listed here or a template using them fails to parse.
var parseFuncs = func() map[string]any {
	m := map[string]any{}
	for name := range funcs {
		m[name] = struct{}{}
	}
	for _, name := range []string{
		"and", "call", "html", "index", "slice", "js", "len", "not", "or",
		"print", "printf", "println", "urlquery", "eq", "ge", "gt", "le", "lt", "ne",
	} {
		m[name] = struct{}{}
	}
	return m
}()

// RefSet is what a template reads.
//
// Opaque is the important field. A template can reach the whole scope without
// naming anything, for example by ranging over .steps or pulling a key out of
// it with index. Those references cannot be resolved to a step id, so they are
// collected here and the validator refuses them. Without that, the needs check
// would only catch the honest spellings.
type RefSet struct {
	Steps    []string
	UsesItem bool
	Opaque   []string
}

// Refs reports which steps a template reads, whether it reads the current
// for_each item, and any reference it cannot pin to a named step.
func Refs(tmpl string) (RefSet, error) {
	var rs RefSet
	trees, err := parse.Parse("refs", tmpl, "{{", "}}", parseFuncs)
	if err != nil {
		return rs, err
	}
	seen := map[string]bool{}
	w := &walker{seen: seen, rs: &rs}
	for _, t := range trees {
		if t.Root != nil {
			w.walk(t.Root)
		}
	}
	for id := range seen {
		rs.Steps = append(rs.Steps, id)
	}
	sort.Strings(rs.Steps)
	sort.Strings(rs.Opaque)
	return rs, nil
}

type walker struct {
	seen map[string]bool
	rs   *RefSet
}

func (w *walker) opaque(what string) {
	for _, o := range w.rs.Opaque {
		if o == what {
			return
		}
	}
	w.rs.Opaque = append(w.rs.Opaque, what)
}

func (w *walker) field(idents []string) {
	if len(idents) == 0 {
		return
	}
	switch idents[0] {
	case "steps":
		if len(idents) == 1 {
			// `.steps` on its own: ranged over, bound to a variable, or
			// handed to index. Whatever follows is not statically visible.
			w.opaque("the whole .steps map")
			return
		}
		w.seen[idents[1]] = true
	case "item":
		w.rs.UsesItem = true
	}
}

func (w *walker) walk(n parse.Node) {
	switch v := n.(type) {
	case nil:
		return
	case *parse.ListNode:
		if v == nil {
			return
		}
		for _, c := range v.Nodes {
			w.walk(c)
		}
	case *parse.ActionNode:
		w.walk(v.Pipe)
	case *parse.PipeNode:
		if v == nil {
			return
		}
		for _, c := range v.Cmds {
			w.walk(c)
		}
	case *parse.CommandNode:
		w.command(v)
	case *parse.FieldNode:
		w.field(v.Ident)
	case *parse.ChainNode:
		// A chain off something that is not a plain field, such as
		// (index .steps "x").result, cannot be resolved statically. The
		// inner node is walked, which flags the bare .steps.
		w.walk(v.Node)
	case *parse.IfNode:
		w.branch(&v.BranchNode)
	case *parse.RangeNode:
		w.branch(&v.BranchNode)
	case *parse.WithNode:
		w.branch(&v.BranchNode)
	case *parse.TemplateNode:
		w.walk(v.Pipe)
	}
}

// command inspects a call. index applied to the root scope reaches whatever
// the caller names at run time, so it is opaque for the same reason.
func (w *walker) command(c *parse.CommandNode) {
	if len(c.Args) >= 2 {
		if id, ok := c.Args[0].(*parse.IdentifierNode); ok && id.Ident == "index" {
			if _, isDot := c.Args[1].(*parse.DotNode); isDot {
				w.opaque("index over the whole scope")
			}
		}
	}
	for _, a := range c.Args {
		w.walk(a)
	}
}

func (w *walker) branch(b *parse.BranchNode) {
	w.walk(b.Pipe)
	w.walk(b.List)
	w.walk(b.ElseList)
}
