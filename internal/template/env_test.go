package template

import (
	"path/filepath"
	"strings"
	"testing"
)

func scopeFixture() Scope {
	return Scope{
		Steps: map[string]StepView{
			"screen": {
				Result:  "screen output",
				Outputs: map[string]any{"candidates": []any{map[string]any{"ticker": "AAPL"}}},
				Dir:     "/runs/r1/screen/0/out",
			},
			"analyse": {
				Items: []ItemView{
					{Index: 0, Result: "report one", Outputs: map[string]any{"verdict": "undervalued"}},
					{Index: 1, Result: "report two", Outputs: map[string]any{"verdict": "value trap"}},
				},
			},
		},
	}
}

func TestRenderStepResult(t *testing.T) {
	got, err := Render("{{ .steps.screen.result }}", scopeFixture())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "screen output" {
		t.Errorf("got %q, want %q", got, "screen output")
	}
}

func TestRenderItemField(t *testing.T) {
	sc := scopeFixture()
	sc.Item = map[string]any{"ticker": "MSFT"}
	got, err := Render("{{ .item.ticker }}", sc)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "MSFT" {
		t.Errorf("got %q, want %q", got, "MSFT")
	}
}

func TestRenderItemViewFields(t *testing.T) {
	sc := scopeFixture()
	sc.Item = sc.Steps["analyse"].Items[0].asMap()
	got, err := Render("{{ .item.outputs.verdict }}|{{ .item.result }}", sc)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "undervalued|report one" {
		t.Errorf("got %q", got)
	}
}

func TestRenderJSONHelper(t *testing.T) {
	sc := scopeFixture()
	sc.Item = map[string]any{"ticker": "AAPL"}
	got, err := Render("{{ .item | json }}", sc)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != `{"ticker":"AAPL"}` {
		t.Errorf("got %q", got)
	}
}

func TestRenderFileHelper(t *testing.T) {
	got, err := Render(`{{ file .steps.screen "candidates.json" }}`, scopeFixture())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := filepath.Join("/runs/r1/screen/0/out", "candidates.json")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderMissingKeyIsError(t *testing.T) {
	if _, err := Render("{{ .steps.nosuch.result }}", scopeFixture()); err == nil {
		t.Fatal("Render succeeded, want an error for a missing key")
	}
}

func TestRenderUnknownHelperIsError(t *testing.T) {
	if _, err := Render("{{ readFile \"/etc/passwd\" }}", scopeFixture()); err == nil {
		t.Fatal("Render succeeded, want an error for an unknown helper")
	}
}

func TestRenderTrimAndIndentHelpers(t *testing.T) {
	got, err := Render(`{{ "  hi  " | trim }}`, scopeFixture())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "hi" {
		t.Errorf("trim gave %q", got)
	}
}

func TestRenderJSONDecodesArray(t *testing.T) {
	v, err := RenderJSON("{{ .steps.screen.outputs.candidates | json }}", scopeFixture())
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	arr, ok := v.([]any)
	if !ok {
		t.Fatalf("got %T, want []any", v)
	}
	if len(arr) != 1 {
		t.Errorf("len = %d, want 1", len(arr))
	}
}

func TestRenderJSONRejectsNonJSON(t *testing.T) {
	if _, err := RenderJSON("not json at all", scopeFixture()); err == nil {
		t.Fatal("RenderJSON succeeded on non-JSON text")
	} else if !strings.Contains(err.Error(), "JSON") {
		t.Errorf("error %q should mention JSON", err)
	}
}

func TestRenderJSONEvaluatesBareActionAsValue(t *testing.T) {
	v, err := RenderJSON("{{ .steps.screen.outputs.candidates }}", scopeFixture())
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	arr, ok := v.([]any)
	if !ok {
		t.Fatalf("got %T (%v), want []any: a bare action must evaluate to the value, not its Go formatting", v, v)
	}
	if len(arr) != 1 {
		t.Errorf("len = %d, want 1", len(arr))
	}
}

func TestRenderJSONDoesNotDoubleEncode(t *testing.T) {
	v, err := RenderJSON("{{ .steps.screen.outputs.candidates | json }}", scopeFixture())
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	if _, ok := v.([]any); !ok {
		t.Fatalf("got %T, want []any: an explicit json pipe must not be wrapped again", v)
	}
}

func TestRenderJSONAcceptsLiteralDocument(t *testing.T) {
	v, err := RenderJSON(`[{"ticker":"AAPL"}]`, scopeFixture())
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	if arr, ok := v.([]any); !ok || len(arr) != 1 {
		t.Errorf("got %#v, want a one-element array", v)
	}
}
