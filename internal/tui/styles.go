package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/hacKarim/cadence/internal/store"
)

var (
	styleTitle    = lipgloss.NewStyle().Bold(true)
	styleDim      = lipgloss.NewStyle().Faint(true)
	styleWarn     = lipgloss.NewStyle().Bold(true)
	styleSelected = lipgloss.NewStyle().Bold(true).Reverse(true)
)

// glyph is the one-character marker shown beside a step or item state.
func glyph(s store.State) string {
	switch s {
	case store.StateSucceeded:
		return "+"
	case store.StateCached:
		return "="
	case store.StateRunning:
		return ">"
	case store.StateFailed:
		return "x"
	case store.StateSkipped:
		return "-"
	case store.StateCancelled:
		return "/"
	default:
		return "."
	}
}
