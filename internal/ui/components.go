package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Version is set by main for the banner.
var Version = "dev"

// Tone is the semantic colour of a badge or line.
type Tone int

const (
	ToneNeutral Tone = iota
	ToneSuccess
	ToneWarning
	ToneDanger
	ToneAccent
)

func toneColors(t Tone) (fg, bg lipgloss.TerminalColor) {
	switch t {
	case ToneSuccess:
		return ColSuccess, colSuccessBg
	case ToneWarning:
		return ColWarning, colWarningBg
	case ToneDanger:
		return ColDanger, colDangerBg
	case ToneAccent:
		return ColAccent2, colNeutralBg
	default:
		return ColMuted2, colNeutralBg
	}
}

// Badge renders a compact coloured label such as " OK ".
func Badge(text string, t Tone) string {
	fg, bg := toneColors(t)
	return lipgloss.NewStyle().Bold(true).Foreground(fg).Background(bg).Padding(0, 1).Render(text)
}

// EnvBadge renders the TEST / PROD marker.
func EnvBadge(prod bool) string {
	if prod {
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffffff")).Background(ColDanger).Padding(0, 1).Render("PROD")
	}
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#1a1204")).Background(ColWarning).Padding(0, 1).Render("TEST")
}

// Header is the one-line banner shown above interactive output.
func Header(brand, profile, merchantID string, prod bool) string {
	left := Accent.Render("◆") + " " + Bold.Render("paygate") + " " + Muted.Render(Version)
	right := EnvBadge(prod) + "  " + Label.Render(brand+" · "+profile) + Muted.Render(" ("+merchantID+")")
	gap := 4
	return " " + left + strings.Repeat(" ", gap) + right
}

// Line helpers for status output.
func OK(msg string) string   { return " " + Success.Render("✓") + " " + msg }
func Fail(msg string) string { return " " + Danger.Render("✗") + " " + msg }
func Warn(msg string) string { return " " + Warning.Render("!") + " " + msg }
func Info(msg string) string { return " " + Accent.Render("›") + " " + msg }

// Next renders a "Next  cmd · cmd" hint line.
func Next(cmds ...string) string {
	parts := make([]string, len(cmds))
	for i, c := range cmds {
		parts[i] = Link.Render(c)
	}
	return " " + Muted.Render("Next") + "  " + strings.Join(parts, Muted.Render("  ·  "))
}

// Row is one label/value pair of a result card.
type Row struct {
	Label string
	Value string
}

// Card renders a rounded box with a title line (badge, title, amount) and
// aligned rows.
type Card struct {
	Badge  string
	Title  string
	Amount string
	Rows   []Row
	Footer string
}

func (c Card) String() string {
	labelW := 0
	for _, r := range c.Rows {
		if w := lipgloss.Width(r.Label); w > labelW {
			labelW = w
		}
	}
	var lines []string
	for _, r := range c.Rows {
		if r.Value == "" {
			continue
		}
		lines = append(lines, Label.Render(padRight(r.Label, labelW))+"   "+r.Value)
	}
	if c.Footer != "" {
		lines = append(lines, "", c.Footer)
	}
	body := strings.Join(lines, "\n")

	head := strings.TrimSpace(c.Badge + "  " + Bold.Render(c.Title))
	inner := lipgloss.Width(body)
	minW := lipgloss.Width(head) + lipgloss.Width(c.Amount) + 6
	if inner < minW {
		inner = minW
	}
	if inner < 48 {
		inner = 48
	}
	if c.Amount != "" {
		pad := inner - lipgloss.Width(head) - lipgloss.Width(c.Amount)
		head += strings.Repeat(" ", pad) + Bold.Render(c.Amount)
	}
	content := head
	if body != "" {
		content += "\n\n" + body
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColBorder).
		Padding(0, 2).
		Width(inner + 4)
	return indentBlock(box.Render(content), " ")
}

// ErrorBox renders an error with optional detail lines and a hint.
func ErrorBox(title string, details []string, hint string) string {
	var b strings.Builder
	b.WriteString(Fail(Bold.Render(title)))
	for _, d := range details {
		b.WriteString("\n   " + Muted.Render("│") + " " + d)
	}
	if hint != "" {
		b.WriteString("\n   " + Accent.Render("→") + " " + Label.Render(hint))
	}
	return b.String()
}

// Section renders a muted section heading.
func Section(title string) string {
	return " " + Label.Render(strings.ToUpper(title))
}

func padRight(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func indentBlock(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// Println writes a line to stdout.
func Println(a ...any) { fmt.Fprintln(Stdout, a...) }

// Eprintln writes a line to stderr.
func Eprintln(a ...any) { fmt.Fprintln(Stderr, a...) }
