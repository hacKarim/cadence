// Package graph turns a workflow's needs declarations into a dependency graph
// and answers the one question the engine asks repeatedly: which steps can
// start now.
package graph

import "github.com/hacKarim/cadence/internal/spec"

// Graph is an immutable view of a workflow's step dependencies.
type Graph struct {
	ids      []string
	needs    map[string][]string
	dependOn map[string][]string
}

// New builds the graph. It assumes the workflow already validated, so every
// needs entry names a real step and there are no cycles.
func New(steps []spec.Step) *Graph {
	g := &Graph{
		needs:    make(map[string][]string, len(steps)),
		dependOn: make(map[string][]string, len(steps)),
	}
	for _, s := range steps {
		g.ids = append(g.ids, s.ID)
		g.needs[s.ID] = append([]string{}, s.Needs...)
	}
	for _, s := range steps {
		for _, n := range s.Needs {
			g.dependOn[n] = append(g.dependOn[n], s.ID)
		}
	}
	return g
}

// IDs returns the step ids in document order.
func (g *Graph) IDs() []string { return append([]string{}, g.ids...) }

// Needs returns the direct dependencies of a step.
func (g *Graph) Needs(id string) []string { return g.needs[id] }

// Ready returns the steps that have not finished yet and whose dependencies
// have all finished successfully. A step with a failed dependency is never
// ready: the engine marks it skipped instead.
func (g *Graph) Ready(done, failed map[string]bool) []string {
	var out []string
	for _, id := range g.ids {
		if done[id] || failed[id] {
			continue
		}
		ok := true
		for _, n := range g.needs[id] {
			if !done[n] || failed[n] {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, id)
		}
	}
	return out
}

// Downstream returns every step that transitively depends on id.
func (g *Graph) Downstream(id string) []string {
	seen := map[string]bool{}
	var walk func(string)
	walk = func(cur string) {
		for _, d := range g.dependOn[cur] {
			if seen[d] {
				continue
			}
			seen[d] = true
			walk(d)
		}
	}
	walk(id)
	out := make([]string, 0, len(seen))
	for _, id := range g.ids {
		if seen[id] {
			out = append(out, id)
		}
	}
	return out
}

// TopoOrder returns the steps in an order where every step follows its
// dependencies. Ties keep document order, so runs read predictably.
func (g *Graph) TopoOrder() []string {
	done := map[string]bool{}
	var out []string
	for len(out) < len(g.ids) {
		ready := g.Ready(done, nil)
		if len(ready) == 0 {
			break
		}
		for _, id := range ready {
			done[id] = true
			out = append(out, id)
		}
	}
	return out
}
