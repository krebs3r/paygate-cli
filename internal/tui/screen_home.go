package tui

import (
	"errors"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

type homeAction struct{ key, label, target string }

var homeActions = []homeAction{
	{"n", "act.new", "new"},
	{"l", "act.lookup", "payments"},
	{"p", "act.profile", "profiles"},
	{"t", "act.cards", "cards"},
	{"s", "act.settings", "settings"},
}

type homeScreen struct{ sel int }

func newHomeScreen() *homeScreen { return &homeScreen{} }

func (s *homeScreen) ID() string                { return "home" }
func (s *homeScreen) Title() string             { return t("menu.home") }
func (s *homeScreen) Shortcut() string          { return "h" }
func (s *homeScreen) Activate(*Context) tea.Cmd { return nil }
func (s *homeScreen) Capturing() bool           { return false }
func (s *homeScreen) Back(*Context) bool        { return false }
func (s *homeScreen) Hints(ctx *Context) []Hint {
	if ctx.Session == nil {
		return []Hint{{"enter", t("home.add")}}
	}
	return []Hint{{"↑↓", t("key.select")}, {"enter", t("key.open")}}
}

func (s *homeScreen) Update(ctx *Context, msg tea.Msg) tea.Cmd {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if ctx.Session == nil {
		if km.String() == "enter" {
			return switchTo("profiles", "add")
		}
		return nil
	}
	switch km.String() {
	case "up", "k":
		s.sel = clamp(s.sel-1, len(homeActions))
	case "down", "j":
		s.sel = clamp(s.sel+1, len(homeActions))
	case "enter":
		return switchTo(homeActions[s.sel].target, nil)
	}
	return nil
}

func (s *homeScreen) View(ctx *Context, w, h int) string {
	if ctx.Session == nil {
		out := title(t("home.first")) + "\n\n" + ui.Label.Render(wrap(t("home.first.text"), min(w, 72))) + "\n\n"
		if ctx.SessionErr != nil && !errors.Is(ctx.SessionErr, config.ErrNoProfile) {
			out += ui.Warning.Render(t("home.session.err")) + "\n" + ui.Muted.Render(wrap(ctx.SessionErr.Error(), min(w, 72))) + "\n\n"
		}
		return out + selectable(ui.Bold.Render(t("home.add")), true, min(w, 40))
	}

	p := ctx.Session.Profile
	auth := t("auth.basic")
	if p.Auth == string(paygate.AuthOAuth2) {
		auth = t("auth.oauth2")
	}
	card := section(t("home.active")) + "\n" + kv([][2]string{
		{t("f.profile"), ui.Bold.Render(ctx.Session.Name)},
		{t("f.platform"), paygate.BrandLabel(paygate.Brand(p.Brand))},
		{t("f.environment"), ui.EnvBadge(p.IsProd())},
		{t("f.host"), ctx.Session.Host()},
		{t("f.merchant"), p.MerchantID},
		{t("f.auth"), auth},
	})
	border := ui.ColBorder
	if p.IsProd() {
		border = ui.ColDanger
	}

	out := title(t("home.welcome")) + "\n\n" + box(card, min(w, 64), border) + "\n"
	switch {
	case ctx.Verified:
		out += ui.Success.Render("✓ ") + ui.Label.Render(t("home.verified")) + "\n"
	case ctx.VerifyDone && ctx.VerifyErr != nil:
		out += ui.Danger.Render("! ") + ui.Label.Render(t("home.unverified")+" ") + ui.Muted.Render(wrap(ctx.VerifyErr.Error(), min(w, 64))) + "\n"
	}
	if p.IsProd() {
		out += ui.Danger.Render("! ") + ui.Label.Render(t("home.prod")) + "\n"
	}
	out += "\n" + section(t("home.actions")) + "\n"
	for i, a := range homeActions {
		row := ui.Accent.Render(a.key) + "  " + t(a.label)
		out += selectable(row, i == s.sel, min(w, 40)) + "\n"
	}
	return out
}
