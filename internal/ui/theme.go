// Package ui renders the CLI's terminal output: the palette shared with the
// Paygate Payment Tester, badges, result cards, spinners and forms.
package ui

import (
	"io"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
	"github.com/muesli/termenv"
	"golang.org/x/term"
)

// Palette of the Paygate Payment Tester (css/style.css), dark and light.
var (
	ColAccent  = lipgloss.AdaptiveColor{Light: "#2563eb", Dark: "#3d7eff"}
	ColAccent2 = lipgloss.AdaptiveColor{Light: "#1d4ed8", Dark: "#6fb3ff"}
	ColSuccess = lipgloss.AdaptiveColor{Light: "#16a34a", Dark: "#22c55e"}
	ColWarning = lipgloss.AdaptiveColor{Light: "#d97706", Dark: "#f59e0b"}
	ColDanger  = lipgloss.AdaptiveColor{Light: "#dc2626", Dark: "#ef4444"}
	ColText    = lipgloss.AdaptiveColor{Light: "#0f172a", Dark: "#dce8f5"}
	ColMuted   = lipgloss.AdaptiveColor{Light: "#94a3b8", Dark: "#5c6e88"}
	ColMuted2  = lipgloss.AdaptiveColor{Light: "#475569", Dark: "#8fa3bf"}
	ColBorder  = lipgloss.AdaptiveColor{Light: "#b0bdd0", Dark: "#2a3448"}
	// ColFrame draws the app frame.
	ColFrame = lipgloss.AdaptiveColor{Light: "#94a3b8", Dark: "#3a4a66"}
	// ColSelection is the background of the selected list row.
	ColSelection = lipgloss.AdaptiveColor{Light: "#dbeafe", Dark: "#162036"}

	colSuccessBg = lipgloss.AdaptiveColor{Light: "#dcfce7", Dark: "#0c2a17"}
	colDangerBg  = lipgloss.AdaptiveColor{Light: "#fee2e2", Dark: "#2a0f10"}
	colWarningBg = lipgloss.AdaptiveColor{Light: "#fef3c7", Dark: "#2a1d05"}
	colNeutralBg = lipgloss.AdaptiveColor{Light: "#e2e8f0", Dark: "#1e2737"}
)

// Text styles.
var (
	Bold    = lipgloss.NewStyle().Bold(true).Foreground(ColText)
	Text    = lipgloss.NewStyle().Foreground(ColText)
	Muted   = lipgloss.NewStyle().Foreground(ColMuted)
	Label   = lipgloss.NewStyle().Foreground(ColMuted2)
	Accent  = lipgloss.NewStyle().Foreground(ColAccent)
	Link    = lipgloss.NewStyle().Foreground(ColAccent2)
	Success = lipgloss.NewStyle().Foreground(ColSuccess)
	Warning = lipgloss.NewStyle().Foreground(ColWarning)
	Danger  = lipgloss.NewStyle().Foreground(ColDanger)
)

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
