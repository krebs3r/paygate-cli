package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/krebs3r/paygate-cli/internal/ui"
)

func title(s string) string   { return ui.Bold.Render(s) }
func section(s string) string { return ui.Muted.Render(s) }

// box draws a rounded panel of total width w.
func box(content string, w int, border lipgloss.TerminalColor) string {
	if w < 10 {
		w = 10
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Padding(0, 1).
		Width(w - 2).
		Render(content)
}

// kv renders aligned label/value rows; empty values are skipped.
func kv(rows [][2]string) string {
	w := 0
	for _, r := range rows {
		if lw := lipgloss.Width(r[0]); lw > w {
			w = lw
		}
	}
	var out []string
	for _, r := range rows {
		if r[1] == "" {
			continue
		}
		out = append(out, ui.Label.Render(r[0]+strings.Repeat(" ", w-lipgloss.Width(r[0])))+"   "+r[1])
	}
	return strings.Join(out, "\n")
}

// selectable renders one list row, highlighted when selected.
func selectable(row string, selected bool, w int) string {
	if !selected {
		return "  " + row
	}
	return lipgloss.NewStyle().Background(ui.ColSelection).Width(w).Render(ui.Accent.Render("› ") + row)
}

// wrap word-wraps text to width w.
func wrap(s string, w int) string {
	return lipgloss.NewStyle().Width(w).Render(s)
}

func newSpinner() spinner.Model {
	s := spinner.New()
	s.Spinner = spinner.Spinner{Frames: []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}, FPS: spinner.Dot.FPS}
	s.Style = ui.Accent
	return s
}

// formKeyMap lets esc cancel an embedded form instead of quitting the app.
func formKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))
	return km
}

// newForm builds an embedded form in the app's look.
func newForm(w int, groups ...*huh.Group) *huh.Form {
	if w > 76 {
		w = 76
	}
	return huh.NewForm(groups...).
		WithTheme(ui.FormTheme()).
		WithKeyMap(formKeyMap()).
		WithShowHelp(true).
		WithWidth(w)
}

func clamp(i, n int) int {
	if n == 0 {
		return 0
	}
	if i < 0 {
		return n - 1
	}
	if i >= n {
		return 0
	}
	return i
}
