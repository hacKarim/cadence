package spec

import (
	"os"
	"path/filepath"
	"testing"
)

// The shipped examples are documentation. If they stop parsing, the README
// describes a spec language the engine does not implement.
func TestShippedExamplesParseAndValidate(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "examples", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no examples found: examples/*.yaml")
	}
	for _, p := range paths {
		t.Run(filepath.Base(p), func(t *testing.T) {
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			wf, err := Parse(b)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if err := wf.Validate(); err != nil {
				t.Fatalf("validate:\n%v", err)
			}
		})
	}
}
