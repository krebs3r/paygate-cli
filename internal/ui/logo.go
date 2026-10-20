package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var bigLogo = []string{
	"██████╗  █████╗ ██╗   ██╗ ██████╗  █████╗ ████████╗███████╗",
	"██╔══██╗██╔══██╗╚██╗ ██╔╝██╔════╝ ██╔══██╗╚══██╔══╝██╔════╝",
	"██████╔╝███████║ ╚████╔╝ ██║  ███╗███████║   ██║   █████╗  ",
	"██╔═══╝ ██╔══██║  ╚██╔╝  ██║   ██║██╔══██║   ██║   ██╔══╝  ",
	"██║     ██║  ██║   ██║   ╚██████╔╝██║  ██║   ██║   ███████╗",
	"╚═╝     ╚═╝  ╚═╝   ╚═╝    ╚═════╝ ╚═╝  ╚═╝   ╚═╝   ╚══════╝",
}

var smallLogo = []string{
	"█▀█ ▄▀█ █▄█ █▀▀ ▄▀█ ▀█▀ █▀▀",
	"█▀▀ █▀█  █  █▄█ █▀█  █  ██▄",
}

// logoGradient runs from light blue to violet (dark terminals) or from blue
// to purple (light terminals).
var logoGradient = []lipgloss.AdaptiveColor{
	{Light: "#1d4ed8", Dark: "#93c5fd"},
	{Light: "#2563eb", Dark: "#6fb3ff"},
	{Light: "#3b5bdb", Dark: "#4f8dff"},
	{Light: "#4338ca", Dark: "#3d7eff"},
	{Light: "#5b21b6", Dark: "#6366f1"},
	{Light: "#6d28d9", Dark: "#8b5cf6"},
}

// BigLogoWidth is the width of the large logo in cells.
var BigLogoWidth = lipgloss.Width(bigLogo[0])

// BigLogo renders the large logo. shift moves the gradient down by that many
// lines, which animates a shimmer when called with increasing values.
func BigLogo(shift int) string {
	return gradient(bigLogo, shift)
}

// SmallLogo renders the two-line logo for the sidebar.
func SmallLogo() string {
	return lipgloss.NewStyle().Foreground(logoGradient[1]).Render(smallLogo[0]) + "\n" +
		lipgloss.NewStyle().Foreground(logoGradient[4]).Render(smallLogo[1])
}

// SmallLogoWidth is the width of the small logo in cells.
var SmallLogoWidth = lipgloss.Width(smallLogo[0])

func gradient(lines []string, shift int) string {
	out := make([]string, len(lines))
	n := len(logoGradient)
	for i, l := range lines {
		c := logoGradient[((i-shift)%n+n)%n]
		out[i] = lipgloss.NewStyle().Foreground(c).Render(l)
	}
	return strings.Join(out, "\n")
}
