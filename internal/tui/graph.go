// Package tui renders cadence's terminal interface. The rendering functions
// are pure and take a width, so layout is tested without a terminal and the
// Bubble Tea model stays a thin shell over them.
package tui

import (
	"fmt"
	"strings"

	"github.com/hacKarim/cadence/internal/sandbox"
	"github.com/hacKarim/cadence/internal/store"
)

// RenderGraph draws a run: one line per step, with fan-out progress, cost, and
// an honest note when the sandbox cannot enforce isolation.
func RenderGraph(tree *store.RunTree, width int, backend sandbox.Backend, enforced bool) string {
	if width < 20 {
		width = 20
	}
	var b strings.Builder

	head := fmt.Sprintf("run %d  %s  %s", tree.Run.ID, tree.Run.Workflow, tree.Run.State)
	b.WriteString(clip(styleTitle.Render(head), width) + "\n")

	sandboxLine := fmt.Sprintf("sandbox: %s", backend)
	if !enforced {
		sandboxLine += "  (not enforced)"
		b.WriteString(clip(styleWarn.Render(sandboxLine), width) + "\n")
	} else {
		b.WriteString(clip(styleDim.Render(sandboxLine), width) + "\n")
	}
	b.WriteString(strings.Repeat("-", min(width, 60)) + "\n")

	var cost float64
	for _, s := range tree.Steps {
		for _, it := range s.Items {
			cost += it.CostUSD
		}
	}

	nameW := 18
	if width < 60 {
		nameW = 10
	}
	for _, s := range tree.Steps {
		name := truncate(s.StepID, nameW)
		line := fmt.Sprintf("%s %-*s %s", glyph(s.State), nameW, name, s.State)
		if len(s.Items) > 1 {
			done := 0
			for _, it := range s.Items {
				if it.State.OK() && it.State != store.StateSkipped {
					done++
				}
			}
			line += fmt.Sprintf("  [%d/%d]", done, len(s.Items))
		}
		b.WriteString(clip(line, width) + "\n")
		for _, note := range []string{s.Error, s.Note} {
			if note != "" {
				b.WriteString(clip(styleDim.Render("    "+note), width) + "\n")
			}
		}
	}

	b.WriteString(strings.Repeat("-", min(width, 60)) + "\n")
	b.WriteString(clip(styleDim.Render(fmt.Sprintf("cost so far: $%.4f", cost)), width))
	return b.String()
}

// RenderRunList draws the recent runs with one highlighted.
func RenderRunList(runs []store.Run, width int, selected int) string {
	if len(runs) == 0 {
		return styleDim.Render("no runs yet")
	}
	if width < 20 {
		width = 20
	}
	var b strings.Builder
	b.WriteString(clip(styleTitle.Render("recent runs"), width) + "\n")
	for i, r := range runs {
		took := ""
		if r.EndedAt != nil {
			took = r.EndedAt.Sub(r.StartedAt).Round(1e8).String()
		}
		// The marker is a character, not only a style: lipgloss drops styling
		// when the output is not a terminal, and the selection must still be
		// visible when a run is piped to a file or read by a test.
		marker := "  "
		if i == selected {
			marker = "> "
		}
		line := fmt.Sprintf("%s%s %-5d %-20s %-10s %-8s %s",
			marker, glyph(r.State), r.ID, truncate(r.Workflow, 20), r.State, took,
			r.StartedAt.Format("01-02 15:04"))
		line = clip(line, width)
		if i == selected {
			line = styleSelected.Render(line)
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// clip trims a rendered line to the terminal width, counting the characters a
// reader sees rather than the escape sequences around them.
func clip(s string, width int) string {
	plain := stripANSI(s)
	if len([]rune(plain)) <= width {
		return s
	}
	// Once the text must be cut, styling is dropped: slicing a styled string
	// would leave a dangling escape sequence.
	return string([]rune(plain)[:width])
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "~"
}

func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEscape = true
		case inEscape && (r == 'm' || r == 'K'):
			inEscape = false
		case !inEscape:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
