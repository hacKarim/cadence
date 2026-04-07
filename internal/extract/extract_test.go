package extract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hacKarim/cadence/internal/spec"
)

func TestPrimaryNoExtractReturnsWholeStdout(t *testing.T) {
	got, err := Primary("all of it\n", nil)
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	if got != "all of it\n" {
		t.Errorf("got %q", got)
	}
}

func TestPrimaryAfterMarker(t *testing.T) {
	out := "chatter\nmore chatter\n=====REPORT=====\nthe report\nline two\n"
	got, err := Primary(out, &spec.Extract{After: "=====REPORT====="})
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	if got != "the report\nline two\n" {
		t.Errorf("got %q, want the text after the marker line only", got)
	}
}

func TestPrimaryAfterMarkerMissing(t *testing.T) {
	_, err := Primary("no marker here", &spec.Extract{After: "=====REPORT====="})
	if err == nil {
		t.Fatal("Primary succeeded, want an error naming the marker")
	}
	if !strings.Contains(err.Error(), "=====REPORT=====") {
		t.Errorf("error %q should name the marker", err)
	}
}

func TestPrimaryRegexCapturesGroupOne(t *testing.T) {
	got, err := Primary("noise VERDICT=undervalued noise", &spec.Extract{Regex: `VERDICT=(\w+)`})
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	if got != "undervalued" {
		t.Errorf("got %q, want the first capture group", got)
	}
}

func TestPrimaryRegexNoMatchErrors(t *testing.T) {
	if _, err := Primary("nothing", &spec.Extract{Regex: `VERDICT=(\w+)`}); err == nil {
		t.Fatal("Primary succeeded, want an error when the regex does not match")
	}
}

func tempDirWith(t *testing.T, name, content string) string {
	t.Helper()
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestOutputFromFileJSON(t *testing.T) {
	d := tempDirWith(t, "candidates.json", `[{"ticker":"AAPL"}]`)
	got, err := Output(spec.OutputSpec{From: spec.OutputFromFile, Path: "candidates.json", As: spec.ParseJSON}, Sources{Dir: d})
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	arr, ok := got.([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("got %#v, want a one-element array", got)
	}
}

func TestOutputFromFileMissingErrors(t *testing.T) {
	d := t.TempDir()
	if _, err := Output(spec.OutputSpec{From: spec.OutputFromFile, Path: "nope.json", As: spec.ParseJSON}, Sources{Dir: d}); err == nil {
		t.Fatal("Output succeeded on a missing file")
	}
}

func TestOutputFromFileEscapingDirIsRejected(t *testing.T) {
	d := t.TempDir()
	_, err := Output(spec.OutputSpec{From: spec.OutputFromFile, Path: "../../etc/passwd", As: spec.ParseRaw}, Sources{Dir: d})
	if err == nil {
		t.Fatal("Output read a path outside the step directory")
	}
}

func TestOutputFromResultWithSelect(t *testing.T) {
	got, err := Output(
		spec.OutputSpec{From: spec.OutputFromResult, As: spec.ParseJSON, Select: "$.verdict"},
		Sources{Result: `{"verdict":"undervalued","p3":0.61}`},
	)
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if got != "undervalued" {
		t.Errorf("got %#v, want undervalued", got)
	}
}

func TestOutputSelectIndexesArrays(t *testing.T) {
	got, err := Output(
		spec.OutputSpec{From: spec.OutputFromResult, As: spec.ParseJSON, Select: "$.items[1].name"},
		Sources{Result: `{"items":[{"name":"a"},{"name":"b"}]}`},
	)
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if got != "b" {
		t.Errorf("got %#v, want b", got)
	}
}

func TestOutputSelectMissingPathErrors(t *testing.T) {
	_, err := Output(
		spec.OutputSpec{From: spec.OutputFromResult, As: spec.ParseJSON, Select: "$.nope"},
		Sources{Result: `{"verdict":"x"}`},
	)
	if err == nil {
		t.Fatal("Output succeeded, want an error for a path that is not present")
	}
}

func TestOutputSelectUnsupportedExpressionErrors(t *testing.T) {
	_, err := Output(
		spec.OutputSpec{From: spec.OutputFromResult, As: spec.ParseJSON, Select: "$..deep"},
		Sources{Result: `{"a":1}`},
	)
	if err == nil {
		t.Fatal("Output accepted an expression the resolver does not support")
	}
}

func TestOutputAsLines(t *testing.T) {
	got, err := Output(spec.OutputSpec{From: spec.OutputFromStdout, As: spec.ParseLines}, Sources{Stdout: "one\ntwo\n"})
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	lines, ok := got.([]string)
	if !ok || len(lines) != 2 || lines[1] != "two" {
		t.Errorf("got %#v, want [one two]", got)
	}
}

func TestOutputAsRaw(t *testing.T) {
	got, err := Output(spec.OutputSpec{From: spec.OutputFromStdout, As: spec.ParseRaw}, Sources{Stdout: " text "})
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if got != " text " {
		t.Errorf("got %#v", got)
	}
}

func TestOutputRegexOnStdout(t *testing.T) {
	got, err := Output(spec.OutputSpec{From: spec.OutputFromStdout, As: spec.ParseRaw, Regex: `id=(\d+)`}, Sources{Stdout: "x id=42 y"})
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if got != "42" {
		t.Errorf("got %#v, want 42", got)
	}
}
