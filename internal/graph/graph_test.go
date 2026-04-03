package graph

import (
	"reflect"
	"sort"
	"testing"

	"github.com/hacKarim/cadence/internal/spec"
)

// a -> b -> d, a -> c -> d
func diamond() []spec.Step {
	return []spec.Step{
		{ID: "a"},
		{ID: "b", Needs: []string{"a"}},
		{ID: "c", Needs: []string{"a"}},
		{ID: "d", Needs: []string{"b", "c"}},
	}
}

func sorted(s []string) []string { sort.Strings(s); return s }

func TestReadyReturnsRootsInitially(t *testing.T) {
	g := New(diamond())
	got := sorted(g.Ready(nil, nil))
	if !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("got %v, want [a]", got)
	}
}

func TestReadyUnlocksAfterDependencies(t *testing.T) {
	g := New(diamond())
	got := sorted(g.Ready(map[string]bool{"a": true}, nil))
	if !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Errorf("got %v, want [b c]", got)
	}
	got = sorted(g.Ready(map[string]bool{"a": true, "b": true, "c": true}, nil))
	if !reflect.DeepEqual(got, []string{"d"}) {
		t.Errorf("got %v, want [d]", got)
	}
}

func TestReadyExcludesAlreadyDone(t *testing.T) {
	g := New(diamond())
	done := map[string]bool{"a": true, "b": true}
	for _, id := range g.Ready(done, nil) {
		if done[id] {
			t.Errorf("Ready returned already-done step %q", id)
		}
	}
}

func TestReadyWithFailedDependencyReturnsNothing(t *testing.T) {
	g := New(diamond())
	done := map[string]bool{"a": true, "b": true, "c": true}
	failed := map[string]bool{"b": true}
	if got := g.Ready(done, failed); len(got) != 0 {
		t.Errorf("got %v, want nothing: d depends on failed b", got)
	}
}

func TestDownstreamIsTransitive(t *testing.T) {
	g := New(diamond())
	got := sorted(g.Downstream("a"))
	if !reflect.DeepEqual(got, []string{"b", "c", "d"}) {
		t.Errorf("got %v, want [b c d]", got)
	}
	if got := g.Downstream("d"); len(got) != 0 {
		t.Errorf("Downstream(d) = %v, want none", got)
	}
}

func TestTopoOrderRespectsNeeds(t *testing.T) {
	g := New(diamond())
	order := g.TopoOrder()
	if len(order) != 4 {
		t.Fatalf("got %d steps, want 4", len(order))
	}
	pos := map[string]int{}
	for i, id := range order {
		pos[id] = i
	}
	for _, pair := range [][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}} {
		if pos[pair[0]] >= pos[pair[1]] {
			t.Errorf("%s must come before %s in %v", pair[0], pair[1], order)
		}
	}
}
