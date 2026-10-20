package tui

import (
	"fmt"
	"strings"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/krebs3r/paygate-cli/internal/cards"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

// ---- Test cards -----------------------------------------------------------

type cardsScreen struct{ sel int }

func newCardsScreen() *cardsScreen { return &cardsScreen{} }

func (s *cardsScreen) ID() string                { return "cards" }
func (s *cardsScreen) Title() string             { return t("menu.cards") }
func (s *cardsScreen) Shortcut() string          { return "t" }
func (s *cardsScreen) Activate(*Context) tea.Cmd { return nil }
func (s *cardsScreen) Capturing() bool           { return false }
func (s *cardsScreen) Back(*Context) bool        { return false }
func (s *cardsScreen) Hints(*Context) []Hint {
	return []Hint{{"↑↓", t("key.select")}, {"y", t("cards.copy")}}
}

func (s *cardsScreen) Update(ctx *Context, msg tea.Msg) tea.Cmd {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch km.String() {
	case "up", "k":
		s.sel = clamp(s.sel-1, len(cards.Cards))
	case "down", "j":
		s.sel = clamp(s.sel+1, len(cards.Cards))
	case "y", "c":
		if err := clipboard.WriteAll(cards.Cards[s.sel].Number); err != nil {
			return toast(err.Error(), ui.ToneDanger)
		}
		return toast(t("cards.copied"), ui.ToneSuccess)
	}
	return nil
}

func (s *cardsScreen) View(ctx *Context, w, h int) string {
	out := title(t("cards.title")) + "\n\n"
	head := fmt.Sprintf("  %-22s %-11s %-20s %s", t("cards.alias"), t("cards.brand"), t("cards.number"), t("cards.scenario"))
	out += ui.Muted.Render(truncate(head, w)) + "\n"
	for i, c := range cards.Cards {
		row := fmt.Sprintf("%-22s %-11s %-20s %s", c.Alias, brandName(c.Brand), formatPAN(c.Number), t(c.Scenario)+" · "+t(c.Expect))
		out += selectable(truncate(row, w-2), i == s.sel, w) + "\n"
	}
	out += "\n" + ui.Accent.Render("› ") + ui.Label.Render(wrap(t("cards.hint"), w-2))
	if ctx.IsProd() {
		out += "\n" + ui.Danger.Render("! ") + ui.Label.Render(wrap(t("cards.prod"), w-2))
	}
	return out
}

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
func (s *aboutScreen) Back(*Context) bool               { return false }
func (s *aboutScreen) Hints(*Context) []Hint            { return nil }
func (s *aboutScreen) Update(*Context, tea.Msg) tea.Cmd { return nil }
func (s *aboutScreen) View(ctx *Context, w, h int) string {
	return ui.Accent.Render("◆ ") + title("paygate · Paygate CLI") + " " + ui.Muted.Render(ctx.Version) + "\n\n" +
		ui.Label.Render(t("about.made")) + "\n" +
		ui.Label.Render(wrap(t("about.inspired"), w)) + "\n" +
		ui.Link.Render("https://github.com/fjbender/mollie-cli") + "\n\n" +
		ui.Label.Render(t("about.sister")) + "\n" +
		ui.Link.Render("https://paygate.paytest.dev") + "\n\n" +
		ui.Muted.Render(wrap(t("about.license"), w)) + "\n" +
		ui.Muted.Render(wrap(t("about.official"), w))
}
