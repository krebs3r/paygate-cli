// Package ui renders the CLI's terminal output: the palette shared with the
// Paygate Payment Tester, badges, result cards, spinners and forms.
package ui

import (
	"io"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-isatty"
	"github.com/muesli/termenv"
	"golang.org/x/term"
)

// Colours of the active theme (see SetTheme), dark and light.
var (
	ColAccent, ColAccent2             lipgloss.AdaptiveColor
	ColSuccess, ColWarning, ColDanger lipgloss.AdaptiveColor
	ColText, ColMuted, ColMuted2      lipgloss.AdaptiveColor
	ColBorder                         lipgloss.AdaptiveColor
	// ColFrame draws the app frame.
	ColFrame lipgloss.AdaptiveColor
	// Selected rows: a band with bright text and an accent bar.
	ColSelection, ColSelectionFg, ColSelectionBar lipgloss.AdaptiveColor

	colSuccessBg, colDangerBg, colWarningBg, colNeutralBg lipgloss.AdaptiveColor
)

// Text styles of the active theme.
var Bold, Text, Muted, Label, Accent, Link, Success, Warning, Danger lipgloss.Style

// Selected renders the selected row of a list as a band of width w with an
// accent bar. The row's own colours are dropped: they would interrupt the
// band and some are hard to read on it.
func Selected(row string, w int) string {
	band := lipgloss.NewStyle().Background(ColSelection)
	text := ansi.Truncate(ansi.Strip(row), max(w-2, 0), "…")
	return band.Foreground(ColSelectionBar).Render("▌") +
		band.Foreground(ColSelectionFg).Bold(true).Width(max(w-1, 0)).Render(" "+text)
}

// SelectedIdle marks the selected row of a list without the keyboard focus:
// only the accent bar, the row keeps its colours.
func SelectedIdle(row string) string {
	return lipgloss.NewStyle().Foreground(ColAccent).Render("▌") + " " + row
}

// Stdout and Stderr are the writers all commands print to.
var (
	Stdout io.Writer = os.Stdout
	Stderr io.Writer = os.Stderr
)

// DisableColor switches every style to plain text (--no-color, NO_COLOR).
func DisableColor() {
	lipgloss.SetColorProfile(termenv.Ascii)
}

// IsTTY reports whether f is an interactive terminal.
func IsTTY(f *os.File) bool {
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

// Interactive reports whether prompts can be shown (stdin and stdout are TTYs).
func Interactive() bool {
	return IsTTY(os.Stdin) && IsTTY(os.Stdout)
}

// StderrIsTTY reports whether progress output on stderr can be animated.
func StderrIsTTY() bool { return IsTTY(os.Stderr) }

// Width returns the terminal width (80 if unknown).
func Width() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	return 80
}
