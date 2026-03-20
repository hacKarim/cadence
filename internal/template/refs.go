package template

import (
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

// Refs reports which steps a template reads and whether it reads the current
// for_each item. The workflow validator uses this to prove, before anything
// runs, that a step can only see what its needs list grants it.
func Refs(tmpl string) (steps []string, usesItem bool, err error) {
	trees, err := parse.Parse("refs", tmpl, "{{", "}}", parseFuncs)
	if err != nil {
		return nil, false, err
	}
	seen := map[string]bool{}
	for _, t := range trees {
		if t.Root == nil {
			continue
		}
		walk(t.Root, seen, &usesItem)
	}
	for id := range seen {
		steps = append(steps, id)
	}
	return steps, usesItem, nil
}

func noteField(idents []string, seen map[string]bool, usesItem *bool) {
	if len(idents) == 0 {
		return
	}
	switch idents[0] {
	case "steps":
		if len(idents) >= 2 {
			seen[idents[1]] = true
		}
	case "item":
		*usesItem = true
	}
}

func walk(n parse.Node, seen map[string]bool, usesItem *bool) {
	switch v := n.(type) {
	case nil:
		return
	case *parse.ListNode:
		if v == nil {
			return
		}
		for _, c := range v.Nodes {
			walk(c, seen, usesItem)
		}
	case *parse.ActionNode:
		walk(v.Pipe, seen, usesItem)
	case *parse.PipeNode:
		if v == nil {
			return
		}
		for _, c := range v.Cmds {
			walk(c, seen, usesItem)
		}
	case *parse.CommandNode:
		for _, a := range v.Args {
			walk(a, seen, usesItem)
		}
	case *parse.FieldNode:
		noteField(v.Ident, seen, usesItem)
	case *parse.ChainNode:
		walk(v.Node, seen, usesItem)
	case *parse.IfNode:
		walkBranch(&v.BranchNode, seen, usesItem)
	case *parse.RangeNode:
		walkBranch(&v.BranchNode, seen, usesItem)
	case *parse.WithNode:
		walkBranch(&v.BranchNode, seen, usesItem)
	case *parse.TemplateNode:
		walk(v.Pipe, seen, usesItem)
	}
}

func walkBranch(b *parse.BranchNode, seen map[string]bool, usesItem *bool) {
	walk(b.Pipe, seen, usesItem)
	walk(b.List, seen, usesItem)
	walk(b.ElseList, seen, usesItem)
}
