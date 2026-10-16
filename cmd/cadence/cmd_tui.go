package main

import (
	"fmt"
	"io"

	"github.com/hacKarim/cadence/internal/sandbox"
	"github.com/hacKarim/cadence/internal/tui"
)

func cmdTUI(stderr io.Writer) int {
	st, err := openStore()
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return 1
	}
	defer st.Close()

	sb := sandbox.Detect()
	if err := tui.Run(st, sb.Name(), sb.Enforced()); err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	return 0
}
