package main

import (
	"fmt"
	"io"
)

func cmdValidate(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: cadence validate <workflow.yaml>")
		return 2
	}
	wf, hash, err := loadWorkflow(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s: ok, %d steps (%s)\n", wf.Name, len(wf.Steps), hash[:12])
	if wf.Schedule != "" {
		fmt.Fprintf(stdout, "  schedule %s (%s)\n", wf.Schedule, orDefault(wf.Timezone, "local time"))
	}
	for i := range wf.Steps {
		s := &wf.Steps[i]
		kind := "run"
		if s.IsAgent() {
			kind = "agent"
		}
		line := fmt.Sprintf("  %-18s %-5s", s.ID, kind)
		if s.ForEach != "" {
			line += " fan-out"
		}
		if s.Isolate {
			line += " isolated"
		}
		fmt.Fprintln(stdout, line)
	}
	return 0
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
