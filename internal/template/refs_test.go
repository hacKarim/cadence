package template

import (
	"reflect"
	"sort"
	"testing"
)

func TestRefsFindsStepIDs(t *testing.T) {
	steps, _, err := Refs("{{ .steps.a.result }} and {{ .steps.b.outputs.x }}")
	if err != nil {
		t.Fatalf("Refs: %v", err)
	}
	sort.Strings(steps)
	if !reflect.DeepEqual(steps, []string{"a", "b"}) {
		t.Errorf("got %v, want [a b]", steps)
	}
}

func TestRefsFindsStepIDsInsideHelperCall(t *testing.T) {
	steps, _, err := Refs(`{{ file .steps.screen "x.json" }}`)
	if err != nil {
		t.Fatalf("Refs: %v", err)
	}
	if !reflect.DeepEqual(steps, []string{"screen"}) {
		t.Errorf("got %v, want [screen]", steps)
	}
}

func TestRefsFindsStepIDsInsideConditional(t *testing.T) {
	steps, _, err := Refs(`{{ if gt (len .steps.challenge.items) 0 }}yes{{ end }}`)
	if err != nil {
		t.Fatalf("Refs: %v", err)
	}
	if !reflect.DeepEqual(steps, []string{"challenge"}) {
		t.Errorf("got %v, want [challenge]", steps)
	}
}

func TestRefsDetectsItem(t *testing.T) {
	_, usesItem, err := Refs("{{ .item.ticker }}")
	if err != nil {
		t.Fatalf("Refs: %v", err)
	}
	if !usesItem {
		t.Error("usesItem = false, want true")
	}
}

func TestRefsIgnoresLiteralText(t *testing.T) {
	steps, usesItem, err := Refs("just prose mentioning .steps.a and .item")
	if err != nil {
		t.Fatalf("Refs: %v", err)
	}
	if len(steps) != 0 || usesItem {
		t.Errorf("got steps=%v usesItem=%v, want none", steps, usesItem)
	}
}

func TestRefsOnMalformedTemplateErrors(t *testing.T) {
	if _, _, err := Refs("{{ .steps.a.result "); err == nil {
		t.Fatal("Refs succeeded on a malformed template")
	}
}
