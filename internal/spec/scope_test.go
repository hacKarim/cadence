package spec

import "testing"

func TestValidateRejectsOutOfScopeStepRef(t *testing.T) {
	wf := mustParse(t, `
name: w
steps:
  - id: a
    run: /bin/true
  - id: b
    run: /bin/true
  - id: c
    needs: [b]
    run: echo '{{ .steps.a.result }}'
`)
	wantInvalid(t, wf, "c", "a", "needs")
}

func TestValidateAllowsTransitiveNeedsRef(t *testing.T) {
	wf := mustParse(t, `
name: w
steps:
  - id: a
    run: /bin/true
  - id: b
    needs: [a]
    run: /bin/true
  - id: c
    needs: [b]
    run: echo '{{ .steps.a.result }}'
`)
	if err := wf.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRejectsItemRefWithoutForEach(t *testing.T) {
	wf := mustParse(t, `
name: w
steps:
  - id: a
    run: echo '{{ .item.ticker }}'
`)
	wantInvalid(t, wf, "item", "for_each")
}

func TestValidateRejectsItemRefInsideForEachItself(t *testing.T) {
	wf := mustParse(t, `
name: w
steps:
  - id: a
    run: /bin/true
  - id: b
    needs: [a]
    for_each: '{{ .item.x }}'
    run: /bin/true
`)
	wantInvalid(t, wf, "for_each")
}

func TestValidateRejectsMalformedTemplate(t *testing.T) {
	wf := mustParse(t, `
name: w
steps:
  - id: a
    run: "echo {{ .steps.a.result "
`)
	wantInvalid(t, wf, "a")
}
