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

// A step must not be able to reach the whole scope without naming what it
// reads. Each of these renders another step's result at run time, so the
// validator has to refuse them.
func TestValidateRejectsScopeEvasion(t *testing.T) {
	for name, prompt := range map[string]string{
		"index":    `{{ (index .steps "a").result }}`,
		"with":     `{{ with .steps }}{{ .a.result }}{{ end }}`,
		"variable": `{{ $s := .steps }}{{ $s.a.result }}`,
		"range":    `{{ range $k, $v := .steps }}{{ $v.result }}{{ end }}`,
	} {
		t.Run(name, func(t *testing.T) {
			wf := mustParse(t, `
name: w
steps:
  - id: a
    run: /bin/true
  - id: b
    run: /bin/true
  - id: c
    needs: [b]
    agent:
      prompt: '`+prompt+`'
`)
			wantInvalid(t, wf, "c")
		})
	}
}
