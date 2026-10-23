package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/krebs3r/paygate-cli/internal/webhooks"
)

// WebhookKindBadge renders ENHANCED, BASIC or UNKNOWN as a badge.
func WebhookKindBadge(k webhooks.Kind) string {
	switch k {
	case webhooks.Enhanced:
		return Badge("ENHANCED", ToneAccent)
	case webhooks.Basic:
		return Badge("BASIC", ToneNeutral)
	}
	return Badge("UNKNOWN", ToneDanger)
}

// WebhookFinding renders one check result: an icon, the header or field it
// is about and the detail, wrapped at width below its first line.
func WebhookFinding(f webhooks.Finding, width int) string {
	icon := map[webhooks.Level]string{
		webhooks.Pass: Success.Render("✓"),
		webhooks.Info: Muted.Render("–"),
		webhooks.Warn: Warning.Render("!"),
		webhooks.Fail: Danger.Render("✗"),
	}[f.Level]
	if f.Name == "" {
		return " " + icon + "  " + WrapWords(f.Detail, width-4, "    ")
	}
	name := padRight(f.Name, 29)
	if lipgloss.Width(name) == lipgloss.Width(f.Name) {
		name += " "
	}
	col := 4 + lipgloss.Width(name)
	return " " + icon + "  " + Label.Render(name) + WrapWords(f.Detail, max(width-col, 20), strings.Repeat(" ", col))
}

// WrapWords breaks text into lines of at most width cells; continuation
// lines start with indent.
func WrapWords(text string, width int, indent string) string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		if line != "" && lipgloss.Width(line)+1+lipgloss.Width(word) > width {
			lines = append(lines, line)
			line = word
			continue
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"+indent)
}
