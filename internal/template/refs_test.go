package template

import (
	"reflect"
	"testing"
)

func TestRefsFindsStepIDs(t *testing.T) {
	rs, err := Refs("{{ .steps.a.result }} and {{ .steps.b.outputs.x }}")
	if err != nil {
		t.Fatalf("Refs: %v", err)
	}
	if !reflect.DeepEqual(rs.Steps, []string{"a", "b"}) {
		t.Errorf("got %v, want [a b]", rs.Steps)
	}
}

func TestRefsFindsStepIDsInsideHelperCall(t *testing.T) {
	rs, err := Refs(`{{ file .steps.screen "x.json" }}`)
	if err != nil {
		t.Fatalf("Refs: %v", err)
	}
	if !reflect.DeepEqual(rs.Steps, []string{"screen"}) {
		t.Errorf("got %v, want [screen]", rs.Steps)
	}
}

func TestRefsFindsStepIDsInsideConditional(t *testing.T) {
	rs, err := Refs(`{{ if gt (len .steps.challenge.items) 0 }}yes{{ end }}`)
	if err != nil {
		t.Fatalf("Refs: %v", err)
	}
	if !reflect.DeepEqual(rs.Steps, []string{"challenge"}) {
		t.Errorf("got %v, want [challenge]", rs.Steps)
	}
}

func TestRefsDetectsItem(t *testing.T) {
	rs, err := Refs("{{ .item.ticker }}")
	if err != nil {
		t.Fatalf("Refs: %v", err)
	}
	if !rs.UsesItem {
		t.Error("UsesItem = false, want true")
	}
}

func TestRefsIgnoresLiteralText(t *testing.T) {
	rs, err := Refs("just prose mentioning .steps.a and .item")
	if err != nil {
		t.Fatalf("Refs: %v", err)
	}
	if len(rs.Steps) != 0 || rs.UsesItem {
		t.Errorf("got steps=%v usesItem=%v, want none", rs.Steps, rs.UsesItem)
	}
}

func TestRefsOnMalformedTemplateErrors(t *testing.T) {
	if _, err := Refs("{{ .steps.a.result "); err == nil {
		t.Fatal("Refs succeeded on a malformed template")
	}
}

// The needs check is only worth anything if it cannot be walked around. These
// are the idioms that reach the whole scope without naming a step, and the
// checker has to notice them rather than report "no references".
func TestRefsFlagsReferencesItCannotResolve(t *testing.T) {
	cases := map[string]string{
		"index on the steps map":   `{{ (index .steps "secret").result }}`,
		"with over the steps map":  `{{ with .steps }}{{ .secret.result }}{{ end }}`,
		"variable bound to steps":  `{{ $s := .steps }}{{ $s.secret.result }}`,
		"range over the steps map": `{{ range $k, $v := .steps }}{{ $v.result }}{{ end }}`,
		"index on the root scope":  `{{ index . "item" }}`,
	}
	for name, tmpl := range cases {
		t.Run(name, func(t *testing.T) {
			rs, err := Refs(tmpl)
			if err != nil {
				t.Fatalf("Refs: %v", err)
			}
			if len(rs.Opaque) == 0 {
				t.Errorf("Refs reported nothing opaque for %s: %q", name, tmpl)
			}
		})
	}
}

func TestRefsAcceptsConcreteReferences(t *testing.T) {
	for _, tmpl := range []string{
		`{{ .steps.a.result }}`,
		`{{ with .steps.a }}{{ .result }}{{ end }}`,
		`{{ range .steps.a.items }}{{ .result }}{{ end }}`,
		`{{ file .steps.a "x.json" }}`,
		`{{ if gt (len .steps.a.items) 0 }}y{{ end }}`,
	} {
		rs, err := Refs(tmpl)
		if err != nil {
			t.Fatalf("Refs(%q): %v", tmpl, err)
		}
		if len(rs.Opaque) != 0 {
			t.Errorf("Refs(%q) flagged %v, want none", tmpl, rs.Opaque)
		}
		if len(rs.Steps) != 1 || rs.Steps[0] != "a" {
			t.Errorf("Refs(%q).Steps = %v, want [a]", tmpl, rs.Steps)
		}
	}
}
