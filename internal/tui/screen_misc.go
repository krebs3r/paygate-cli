package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

const (
	mollieCLIURL = "https://github.com/fjbender/mollie-cli"
	paytestURL   = "https://paygate.paytest.dev"
)

type aboutScreen struct {
	links []link // where the links are drawn, relative to the view
}

// link is a URL on the screen that opens with a click.
type link struct {
	area
	url string
}

func newAboutScreen() *aboutScreen { return &aboutScreen{} }

func (s *aboutScreen) ID() string                    { return "about" }
func (s *aboutScreen) Title() string                 { return t("menu.about") }
func (s *aboutScreen) Shortcut() string              { return "" }
func (s *aboutScreen) Activate(*Context) tea.Cmd     { return nil }
func (s *aboutScreen) Capturing() bool               { return false }
func (s *aboutScreen) Back(*Context) (bool, tea.Cmd) { return false, nil }
func (s *aboutScreen) Hints(*Context) []Hint         { return nil }

// Update opens a link on a click: the terminal cannot while the app owns
// the mouse.
func (s *aboutScreen) Update(ctx *Context, msg tea.Msg) tea.Cmd {
	mm, ok := msg.(tea.MouseMsg)
	if !ok || mm.Action != tea.MouseActionPress || mm.Button != tea.MouseButtonLeft {
		return nil
	}
	for _, l := range s.links {
		if l.contains(mm.X-ctx.ContentX, mm.Y-ctx.ContentY) {
			return openLink(l.url)
		}
	}
	return nil
}

func (s *aboutScreen) View(ctx *Context, w, h int) string {
	s.links = s.links[:0]
	out := ui.Accent.Render("◆ ") + title("paygate · Paygate CLI") + " " + ui.Muted.Render(ctx.Version) + commitTag(ctx) + "\n\n" +
		ui.Label.Render(t("about.made")) + "\n" +
		ui.Label.Render(wrap(t("about.inspired"), w)) + "\n"
	out = s.link(out, mollieCLIURL, w) + "\n\n" +
		ui.Label.Render(t("about.sister")) + "\n"
	return s.link(out, paytestURL, w) + "\n\n" +
		ui.Muted.Render(wrap(t("about.license"), w)) + "\n" +
		ui.Warning.Render("! ") + ui.Label.Render(wrap(t("about.official"), min(w-2, 76)))
}

// link appends a URL to out, which ends with a line break, and notes where
// it is drawn.
func (s *aboutScreen) link(out, url string, w int) string {
	u := hardWrap(url, w)
	s.links = append(s.links, link{area{x: 0, y: lipgloss.Height(out) - 1, w: lipgloss.Width(u), h: lipgloss.Height(u)}, url})
	return out + ui.Link.Render(u)
}
