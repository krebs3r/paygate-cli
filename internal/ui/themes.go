package ui

import "github.com/charmbracelet/lipgloss"

// Theme is a colour scheme of the app. Every colour has a light and a dark
// variant; the status colours (success, warning, danger) carry meaning and
// stay the same in all themes.
type Theme struct {
	ID                                   string
	Accent, Accent2                      lipgloss.AdaptiveColor
	Text, Muted, Muted2, Border, Frame   lipgloss.AdaptiveColor
	Selection, SelectionFg, SelectionBar lipgloss.AdaptiveColor
	NeutralBg                            lipgloss.AdaptiveColor
	Logo                                 [6]lipgloss.AdaptiveColor // gradient of the logo, top to bottom
}

// status colours shared by all themes.
var (
	statusSuccess   = lipgloss.AdaptiveColor{Light: "#16a34a", Dark: "#22c55e"}
	statusWarning   = lipgloss.AdaptiveColor{Light: "#d97706", Dark: "#f59e0b"}
	statusDanger    = lipgloss.AdaptiveColor{Light: "#dc2626", Dark: "#ef4444"}
	statusSuccessBg = lipgloss.AdaptiveColor{Light: "#dcfce7", Dark: "#0c2a17"}
	statusDangerBg  = lipgloss.AdaptiveColor{Light: "#fee2e2", Dark: "#2a0f10"}
	statusWarningBg = lipgloss.AdaptiveColor{Light: "#fef3c7", Dark: "#2a1d05"}
)

// Themes lists the colour schemes; the first one is the default.
var Themes = []Theme{
	{
		// The palette of the Paygate Payment Tester (css/style.css). The
		// selection band is lighter than dark backgrounds and stays a clean
		// blue (#005faf) in terminals with 256 colours such as Terminal.app.
		ID:           "blue",
		Accent:       lipgloss.AdaptiveColor{Light: "#2563eb", Dark: "#3d7eff"},
		Accent2:      lipgloss.AdaptiveColor{Light: "#1d4ed8", Dark: "#6fb3ff"},
		Text:         lipgloss.AdaptiveColor{Light: "#0f172a", Dark: "#dce8f5"},
		Muted:        lipgloss.AdaptiveColor{Light: "#94a3b8", Dark: "#5c6e88"},
		Muted2:       lipgloss.AdaptiveColor{Light: "#475569", Dark: "#8fa3bf"},
		Border:       lipgloss.AdaptiveColor{Light: "#b0bdd0", Dark: "#2a3448"},
		Frame:        lipgloss.AdaptiveColor{Light: "#94a3b8", Dark: "#3a4a66"},
		Selection:    lipgloss.AdaptiveColor{Light: "#dbeafe", Dark: "#1f4e9c"},
		SelectionFg:  lipgloss.AdaptiveColor{Light: "#0f172a", Dark: "#ffffff"},
		SelectionBar: lipgloss.AdaptiveColor{Light: "#2563eb", Dark: "#9cc9ff"},
		NeutralBg:    lipgloss.AdaptiveColor{Light: "#e2e8f0", Dark: "#1e2737"},
		// From light blue to violet (dark) or from blue to purple (light).
		Logo: [6]lipgloss.AdaptiveColor{
			{Light: "#1d4ed8", Dark: "#93c5fd"},
			{Light: "#2563eb", Dark: "#6fb3ff"},
			{Light: "#3b5bdb", Dark: "#4f8dff"},
			{Light: "#4338ca", Dark: "#3d7eff"},
			{Light: "#5b21b6", Dark: "#6366f1"},
			{Light: "#6d28d9", Dark: "#8b5cf6"},
		},
	},
	{
		// Blurple (#5865f2) with lavender greys; the logo runs to fuchsia.
		// With 256 colours the tones stay blurple (#5f5fff, #5f5fd7).
		ID:           "blurple",
		Accent:       lipgloss.AdaptiveColor{Light: "#5865f2", Dark: "#5865f2"},
		Accent2:      lipgloss.AdaptiveColor{Light: "#4752c4", Dark: "#9ba3ff"},
		Text:         lipgloss.AdaptiveColor{Light: "#1e1f3b", Dark: "#e3e5fc"},
		Muted:        lipgloss.AdaptiveColor{Light: "#9a9cc4", Dark: "#6e72a8"},
		Muted2:       lipgloss.AdaptiveColor{Light: "#55588a", Dark: "#a3a7d9"},
		Border:       lipgloss.AdaptiveColor{Light: "#c5c8ee", Dark: "#393c6b"},
		Frame:        lipgloss.AdaptiveColor{Light: "#9a9cc4", Dark: "#4a4e8a"},
		Selection:    lipgloss.AdaptiveColor{Light: "#e3e6ff", Dark: "#4752c4"},
		SelectionFg:  lipgloss.AdaptiveColor{Light: "#1e1f3b", Dark: "#ffffff"},
		SelectionBar: lipgloss.AdaptiveColor{Light: "#5865f2", Dark: "#c9ccff"},
		NeutralBg:    lipgloss.AdaptiveColor{Light: "#e3e6ff", Dark: "#2b2d5a"},
		Logo: [6]lipgloss.AdaptiveColor{
			{Light: "#4752c4", Dark: "#c9ccff"},
			{Light: "#5865f2", Dark: "#9ba3ff"},
			{Light: "#6a5ae0", Dark: "#7983f5"},
			{Light: "#8a4fd8", Dark: "#5865f2"},
			{Light: "#ab3fb8", Dark: "#a66ef0"},
			{Light: "#c2338a", Dark: "#eb459e"},
		},
	},
}

var current Theme

func init() { SetTheme("") }

// SetTheme switches all colours and text styles to the theme with the given
// ID; an empty or unknown ID selects the default.
func SetTheme(id string) {
	th := Themes[0]
	for _, t := range Themes {
		if t.ID == id {
			th = t
		}
	}
	current = th
	ColAccent, ColAccent2 = th.Accent, th.Accent2
	ColText, ColMuted, ColMuted2, ColBorder, ColFrame = th.Text, th.Muted, th.Muted2, th.Border, th.Frame
	ColSelection, ColSelectionFg, ColSelectionBar = th.Selection, th.SelectionFg, th.SelectionBar
	ColSuccess, ColWarning, ColDanger = statusSuccess, statusWarning, statusDanger
	colSuccessBg, colDangerBg, colWarningBg, colNeutralBg = statusSuccessBg, statusDangerBg, statusWarningBg, th.NeutralBg
	logoGradient = th.Logo[:]

	Bold = lipgloss.NewStyle().Bold(true).Foreground(ColText)
	Text = lipgloss.NewStyle().Foreground(ColText)
	Muted = lipgloss.NewStyle().Foreground(ColMuted)
	Label = lipgloss.NewStyle().Foreground(ColMuted2)
	Accent = lipgloss.NewStyle().Foreground(ColAccent)
	Link = lipgloss.NewStyle().Foreground(ColAccent2)
	Success = lipgloss.NewStyle().Foreground(ColSuccess)
	Warning = lipgloss.NewStyle().Foreground(ColWarning)
	Danger = lipgloss.NewStyle().Foreground(ColDanger)
}

// ThemeID returns the ID of the active theme.
func ThemeID() string { return current.ID }

// Swatch previews a theme with the colours of its logo.
func Swatch(id string) string {
	for _, t := range Themes {
		if t.ID != id {
			continue
		}
		var out string
		for _, c := range t.Logo {
			out += lipgloss.NewStyle().Foreground(c).Render("▰")
		}
		return out
	}
	return ""
}
