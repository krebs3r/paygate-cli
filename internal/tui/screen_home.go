package tui

import (
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/history"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

type homeAction struct{ key, label, target string }

var homeActions = []homeAction{
	{"n", "act.new", "new"},
	{"l", "act.lookup", "payments"},
	{"p", "act.profile", "profiles"},
	{"s", "act.settings", "settings"},
}

type homeScreen struct{ sel int }

func newHomeScreen() *homeScreen { return &homeScreen{} }

func (s *homeScreen) ID() string                    { return "home" }
func (s *homeScreen) Title() string                 { return t("menu.home") }
func (s *homeScreen) Shortcut() string              { return "h" }
func (s *homeScreen) Activate(*Context) tea.Cmd     { return nil }
func (s *homeScreen) Capturing() bool               { return false }
func (s *homeScreen) Back(*Context) (bool, tea.Cmd) { return false, nil }
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
		a := homeActions[s.sel]
		if a.label == "act.lookup" {
			return switchTo(a.target, "lookup")
		}
		return switchTo(a.target, nil)
	}
	return nil
}

func (s *homeScreen) View(ctx *Context, w, h int) string {
	if ctx.Session == nil {
		out := title(t("home.first")) + "\n\n" + ui.Label.Render(wrap(t("home.first.text"), min(w, 72))) + "\n\n"
		if ctx.SessionErr != nil && !errors.Is(ctx.SessionErr, config.ErrNoProfile) {
			out += ui.Warning.Render(t("home.session.err")) + "\n" + ui.Muted.Render(wrap(ctx.SessionErr.Error(), min(w, 72))) + "\n\n"
		}
		return out + selectable(ctx, ui.Bold.Render(t("home.add")), true, min(w, 40))
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
		out += selectable(ctx, row, i == s.sel, min(w, 40)) + "\n"
	}
	return out + recentPayments(ctx, w)
}

// recentPayments summarises today's payments and lists the latest ones.
func recentPayments(ctx *Context, w int) string {
	all, _ := history.Load()
	var mine []history.Record
	for _, r := range all {
		if r.Profile == ctx.Session.Name {
			mine = append(mine, r)
		}
	}
	if len(mine) == 0 {
		return ""
	}
	today, ok, failed, open := 0, 0, 0, 0
	now := time.Now()
	for _, r := range mine {
		if r.Time.Local().YearDay() != now.YearDay() || r.Time.Local().Year() != now.Year() {
			continue
		}
		today++
		switch paygate.OutcomeOf(&paygate.Payment{Status: r.Status, ResponseCode: paygate.Code(r.Code)}) {
		case paygate.OutcomeSuccess:
			ok++
		case paygate.OutcomeFailed:
			failed++
		default:
			open++
		}
	}
	out := "\n" + section(t("home.today")) + "  " + ui.Label.Render(fmt.Sprintf(t("home.today.line"), today, ok, failed, open)) + "\n"
	out += "\n" + section(t("home.recent")) + "  " + ui.Muted.Render("l") + "\n"
	for i, r := range mine {
		if i == 3 {
			break
		}
		out += "  " + listRow(r, w-4) + "\n"
	}
	return out
}
