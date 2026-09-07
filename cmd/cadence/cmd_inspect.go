package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hacKarim/cadence/internal/store"
)

func cmdRuns(args []string, stdout, stderr io.Writer) int {
	st, err := openStore()
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return 1
	}
	defer st.Close()

	workflow := ""
	if len(args) > 0 {
		workflow = args[0]
	}
	runs, err := st.ListRuns(workflow, 30)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	if len(runs) == 0 {
		fmt.Fprintln(stdout, "no runs yet")
		return 0
	}
	for _, r := range runs {
		took := ""
		if r.EndedAt != nil {
			took = r.EndedAt.Sub(r.StartedAt).Round(100 * 1e6).String()
		}
		fmt.Fprintf(stdout, "%-5d %-20s %-10s %-8s %s\n",
			r.ID, r.Workflow, r.State, took, r.StartedAt.Format("2006-01-02 15:04"))
	}
	return 0
}

func cmdLogs(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "usage: cadence logs <run-id> [step]")
		return 2
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		fmt.Fprintf(stderr, "run id must be a number, got %q\n", args[0])
		return 2
	}
	st, err := openStore()
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return 1
	}
	defer st.Close()

	tree, err := st.RunTree(id)
	if err != nil {
		fmt.Fprintf(stderr, "run %d not found\n", id)
		return 1
	}
	only := ""
	if len(args) > 1 {
		only = args[1]
	}
	for _, s := range tree.Steps {
		if only != "" && s.StepID != only {
			continue
		}
		fmt.Fprintf(stdout, "\n== %s [%s]\n", s.StepID, s.State)
		if s.Error != "" {
			fmt.Fprintf(stdout, "   %s\n", s.Error)
		}
		for _, it := range s.Items {
			fmt.Fprintf(stdout, "   item %d [%s]\n", it.Index, it.State)
			if it.Error != "" {
				fmt.Fprintf(stdout, "     %s\n", it.Error)
			}
			if it.JailDir != "" {
				logPath := filepath.Join(filepath.Dir(it.JailDir), "meta", "stdout.log")
				if b, err := os.ReadFile(logPath); err == nil && len(b) > 0 {
					fmt.Fprintf(stdout, "%s\n", indent(string(b), "     "))
				}
			}
		}
	}
	return 0
}

// printRunTree renders the per-step outcome of a finished run.
func printRunTree(w io.Writer, st *store.Store, id int64) {
	tree, err := st.RunTree(id)
	if err != nil {
		return
	}
	for _, s := range tree.Steps {
		detail := ""
		if n := len(s.Items); n > 1 {
			detail = fmt.Sprintf(" (%d items)", n)
		}
		fmt.Fprintf(w, "  %-20s %s%s\n", s.StepID, s.State, detail)
		if s.Error != "" {
			fmt.Fprintf(w, "    %s\n", s.Error)
		}
	}
}

func indent(s, pad string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n")
}
