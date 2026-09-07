package main

import (
	"flag"
	"fmt"
	"io"
	"time"
)

func cmdCache(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "prune" {
		fmt.Fprintln(stderr, "usage: cadence cache prune [--older-than 168h]")
		return 2
	}
	fs := flag.NewFlagSet("cache prune", flag.ContinueOnError)
	fs.SetOutput(stderr)
	older := fs.Duration("older-than", 168*time.Hour, "delete cache entries older than this")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	st, err := openStore()
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return 1
	}
	defer st.Close()

	n, err := st.PruneCache(time.Now().Add(-*older))
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "pruned %d cache entries\n", n)
	return 0
}
