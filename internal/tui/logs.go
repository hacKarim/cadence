package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/hacKarim/cadence/internal/jail"
	"github.com/hacKarim/cadence/internal/store"
)

// ReadItemLog returns the full stdout log an item produced, from the
// engine-owned meta directory beside its jail.
func ReadItemLog(it store.StepItem) string {
	if it.JailDir == "" {
		return ""
	}
	p := filepath.Join(jail.MetaDirFor(it.JailDir), "stdout.log")
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

// RenderLogs draws one step's items with their output.
func RenderLogs(step store.StepRun, width int) string {
	if width < 20 {
		width = 20
	}
	var b strings.Builder
	b.WriteString(clip(styleTitle.Render(fmt.Sprintf("%s  [%s]", step.StepID, step.State)), width) + "\n")
	if step.Error != "" {
		b.WriteString(clip(styleWarn.Render(step.Error), width) + "\n")
	}
	for _, it := range step.Items {
		b.WriteString("\n")
		head := fmt.Sprintf("item %d  [%s]", it.Index, it.State)
		if it.CostUSD > 0 {
			head += fmt.Sprintf("  $%.4f  %d/%d tokens", it.CostUSD, it.InputTokens, it.OutputTokens)
		}
		b.WriteString(clip(styleDim.Render(head), width) + "\n")
		if it.Error != "" {
			b.WriteString(clip("  "+it.Error, width) + "\n")
		}
		body := it.Result
		if body == "" {
			body = it.Excerpt
		}
		if body == "" {
			body = ReadItemLog(it)
		}
		b.WriteString(renderBody(body, width))
	}
	return b.String()
}

// renderBody prints an agent's result as markdown when glamour can, and as
// plain clipped text otherwise.
func renderBody(body string, width int) string {
	if strings.TrimSpace(body) == "" {
		return styleDim.Render("  (no output)") + "\n"
	}
	if out, err := glamour.Render(body, "dark"); err == nil {
		var b strings.Builder
		for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			b.WriteString(clip(l, width) + "\n")
		}
		return b.String()
	}
	var b strings.Builder
	for _, l := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		b.WriteString(clip("  "+l, width) + "\n")
	}
	return b.String()
}
