package spec

import (
	"strings"
	"testing"
)

// mustParse parses y and fails the test if the document itself is malformed.
func mustParse(t *testing.T, y string) *Workflow {
	t.Helper()
	wf, err := Parse([]byte(y))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return wf
}

// wantInvalid validates wf and requires an error mentioning each fragment.
func wantInvalid(t *testing.T, wf *Workflow, fragments ...string) error {
	t.Helper()
	err := wf.Validate()
	if err == nil {
		t.Fatal("Validate returned nil, want an error")
	}
	for _, f := range fragments {
		if !strings.Contains(err.Error(), f) {
			t.Errorf("error %q does not mention %q", err.Error(), f)
		}
	}
	return err
}

func TestValidateAcceptsGoodWorkflow(t *testing.T) {
	wf := mustParse(t, researchFunnelYAML)
	if err := wf.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRejectsEmptyID(t *testing.T) {
	wf := mustParse(t, "name: w\nsteps:\n  - id: \"\"\n    run: /bin/true\n")
	wantInvalid(t, wf, "id")
}

func TestValidateRejectsNonIdentifierID(t *testing.T) {
	for _, bad := range []string{"a b", "1step", "a/b", "a.b"} {
		wf := mustParse(t, "name: w\nsteps:\n  - id: \""+bad+"\"\n    run: /bin/true\n")
		wantInvalid(t, wf, bad)
	}
}

func TestValidateRejectsDuplicateID(t *testing.T) {
	wf := mustParse(t, "name: w\nsteps:\n  - id: screen\n    run: /bin/true\n  - id: screen\n    run: /bin/true\n")
	wantInvalid(t, wf, "screen", "duplicate")
}

func TestValidateRejectsUnknownNeeds(t *testing.T) {
	wf := mustParse(t, "name: w\nsteps:\n  - id: a\n    needs: [ghost]\n    run: /bin/true\n")
	wantInvalid(t, wf, "ghost")
}

func TestValidateRejectsSelfNeeds(t *testing.T) {
	wf := mustParse(t, "name: w\nsteps:\n  - id: a\n    needs: [a]\n    run: /bin/true\n")
	wantInvalid(t, wf, "a")
}

func TestValidateRejectsCycle(t *testing.T) {
	wf := mustParse(t, `
name: w
steps:
  - id: a
    needs: [c]
    run: /bin/true
  - id: b
    needs: [a]
    run: /bin/true
  - id: c
    needs: [b]
    run: /bin/true
`)
	err := wantInvalid(t, wf, "cycle")
	for _, id := range []string{"a", "b", "c"} {
		if !strings.Contains(err.Error(), id) {
			t.Errorf("cycle error %q omits step %q", err.Error(), id)
		}
	}
}

func TestValidateRejectsBothRunAndAgent(t *testing.T) {
	wf := mustParse(t, "name: w\nsteps:\n  - id: a\n    run: /bin/true\n    agent:\n      prompt: hi\n")
	wantInvalid(t, wf, "run", "agent")
}

func TestValidateRejectsNeitherRunNorAgent(t *testing.T) {
	wf := mustParse(t, "name: w\nsteps:\n  - id: a\n    needs: []\n")
	wantInvalid(t, wf, "run", "agent")
}

func TestValidateRejectsBadCron(t *testing.T) {
	wf := mustParse(t, "name: w\nschedule: \"0 9 * * *\"\nsteps:\n  - id: a\n    run: /bin/true\n")
	wantInvalid(t, wf, "schedule")
}

func TestValidateAcceptsSixFieldCron(t *testing.T) {
	wf := mustParse(t, "name: w\nschedule: \"0 0 9 * * *\"\nsteps:\n  - id: a\n    run: /bin/true\n")
	if err := wf.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRejectsUnknownTimezone(t *testing.T) {
	wf := mustParse(t, "name: w\ntimezone: Mars/Olympus\nsteps:\n  - id: a\n    run: /bin/true\n")
	wantInvalid(t, wf, "timezone")
}

func TestValidateRejectsWhenItemWithoutForEach(t *testing.T) {
	wf := mustParse(t, "name: w\nsteps:\n  - id: a\n    run: /bin/true\n    when_item: '{{ true }}'\n")
	wantInvalid(t, wf, "when_item", "for_each")
}

func TestValidateRejectsOutputFileWithoutPath(t *testing.T) {
	wf := mustParse(t, "name: w\nsteps:\n  - id: a\n    run: /bin/true\n    outputs:\n      x:\n        from: file\n")
	wantInvalid(t, wf, "path")
}

func TestValidateRejectsSelectWithoutJSON(t *testing.T) {
	wf := mustParse(t, "name: w\nsteps:\n  - id: a\n    run: /bin/true\n    outputs:\n      x:\n        from: stdout\n        as: raw\n        select: \"$.a\"\n")
	wantInvalid(t, wf, "select", "json")
}

func TestValidateReportsAllErrors(t *testing.T) {
	wf := mustParse(t, `
name: w
timezone: Mars/Olympus
steps:
  - id: "bad id"
    needs: [ghost]
`)
	err := wantInvalid(t, wf)
	var errs ValidationErrors
	if !asValidationErrors(err, &errs) {
		t.Fatalf("Validate returned %T, want ValidationErrors", err)
	}
	if len(errs) < 3 {
		t.Errorf("got %d errors (%v), want at least 3 reported at once", len(errs), errs)
	}
}
