// Command cadence runs declarative workflows of shell and agent steps.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/hacKarim/cadence/internal/spec"
	"github.com/hacKarim/cadence/internal/store"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

const usage = `cadence: run workflows of shell and agent steps on a schedule.

usage:
  cadence run <workflow.yaml> [--resume <run-id>] [--force] [--max-parallel N]
  cadence validate <workflow.yaml>
  cadence runs [workflow]
  cadence logs <run-id> [step]
  cadence            launch the interactive interface
  cadence doctor
  cadence cache prune [--older-than 168h]
  cadence version
`

// run is the whole CLI, with its streams injected so it is testable.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return cmdTUI(stderr)
	}
	switch args[0] {
	case "run":
		return cmdRun(args[1:], stdout, stderr)
	case "validate":
		return cmdValidate(args[1:], stdout, stderr)
	case "runs":
		return cmdRuns(args[1:], stdout, stderr)
	case "logs":
		return cmdLogs(args[1:], stdout, stderr)
	case "doctor":
		return cmdDoctor(args[1:], stdout, stderr)
	case "cache":
		return cmdCache(args[1:], stdout, stderr)
	case "version":
		return cmdVersion(stdout)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "cadence: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

// dataDir is where the database, run directories and cache live.
func dataDir() string {
	if d := os.Getenv("CADENCE_DATA"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".cadence"
	}
	return filepath.Join(home, ".cadence")
}

func openStore() (*store.Store, error) {
	d := dataDir()
	if err := os.MkdirAll(d, 0o750); err != nil {
		return nil, err
	}
	return store.Open(filepath.Join(d, "cadence.db"))
}

// loadWorkflow reads, parses and validates a workflow file, returning it with
// the hash that identifies this exact definition.
func loadWorkflow(path string) (*spec.Workflow, string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	wf, err := spec.Parse(b)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", path, err)
	}
	if err := wf.Validate(); err != nil {
		return nil, "", fmt.Errorf("%s:\n%w", path, err)
	}
	sum := sha256.Sum256(b)
	return wf, hex.EncodeToString(sum[:]), nil
}
