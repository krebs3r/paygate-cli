package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/krebs3r/paygate-cli/internal/ui"
)

func brandName(b string) string {
	switch b {
	case "MASTERCARD":
		return "Mastercard"
	case "AMEX":
		return "Amex"
	case "VISA":
		return "Visa"
	}
	return b
}

func formatPAN(n string) string {
	if len(n) == 15 {
		return n[:4] + " " + n[4:10] + " " + n[10:]
	}
	var parts []string
	for i := 0; i < len(n); i += 4 {
		parts = append(parts, n[i:min(i+4, len(n))])
	}
	return strings.Join(parts, " ")
}

// ---- About ----------------------------------------------------------------

type aboutScreen struct{}

func newAboutScreen() *aboutScreen { return &aboutScreen{} }

func (s *aboutScreen) ID() string                       { return "about" }
func (s *aboutScreen) Title() string                    { return t("menu.about") }
func (s *aboutScreen) Shortcut() string                 { return "" }
func (s *aboutScreen) Activate(*Context) tea.Cmd        { return nil }
func (s *aboutScreen) Capturing() bool                  { return false }
func (s *aboutScreen) Back(*Context) (bool, tea.Cmd)    { return false, nil }
func (s *aboutScreen) Hints(*Context) []Hint            { return nil }
func (s *aboutScreen) Update(*Context, tea.Msg) tea.Cmd { return nil }
func (s *aboutScreen) View(ctx *Context, w, h int) string {
	return ui.Accent.Render("◆ ") + title("paygate · Paygate CLI") + " " + ui.Muted.Render(ctx.Version) + commitTag(ctx) + "\n\n" +
		ui.Label.Render(t("about.made")) + "\n" +
		ui.Label.Render(wrap(t("about.inspired"), w)) + "\n" +
		ui.Link.Render("https://github.com/fjbender/mollie-cli") + "\n\n" +
		ui.Label.Render(t("about.sister")) + "\n" +
		ui.Link.Render("https://paygate.paytest.dev") + "\n\n" +
		ui.Muted.Render(wrap(t("about.license"), w)) + "\n" +
		ui.Warning.Render("! ") + ui.Label.Render(wrap(t("about.official"), min(w-2, 76)))
}
