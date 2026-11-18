package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/history"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

type homeAction struct{ key, label, target string }

var homeActions = []homeAction{
	{"n", "act.new", "new"},
	{"t", "act.tour", "tour"},
	{"u", "act.dcc", "dcc"},
	{"l", "act.lookup", "payments"},
	{"p", "act.profile", "profiles"},
	{"s", "act.settings", "settings"},
}

// actionsFor are the quick actions of the start screen: the last one starts
// or ends demo mode. The demo has only its own profile, so there is none to
// switch to.
func actionsFor(ctx *Context) []homeAction {
	var out []homeAction
	if ctx.Demo() != nil {
		out = append(out, homeActions[1]) // the tour first: it is what the demo is for
	}
	for _, a := range homeActions {
		if ctx.Demo() != nil && (a.label == "act.profile" || a.label == "act.tour") {
			continue
		}
		out = append(out, a)
	}
	if ctx.Demo() != nil {
		return append(out, homeAction{"z", "act.scenarios", "settings:scenarios"}, homeAction{"x", "act.demo.stop", "demo:stop"})
	}
	return append(out, homeAction{"d", "act.demo.start", "demo:start"})
}

type homeScreen struct {
	sel   int
	first int // welcome screen: 0 start the demo, 1 add a profile
}

func newHomeScreen() *homeScreen { return &homeScreen{} }

func (s *homeScreen) ID() string                    { return "home" }
func (s *homeScreen) Title() string                 { return t("menu.home") }
func (s *homeScreen) Shortcut() string              { return "h" }
func (s *homeScreen) Activate(*Context) tea.Cmd     { return nil }
func (s *homeScreen) Capturing() bool               { return false }
func (s *homeScreen) Back(*Context) (bool, tea.Cmd) { return false, nil }
func (s *homeScreen) Hints(ctx *Context) []Hint {
	if ctx.Session == nil {
		return []Hint{{"↑↓", t("key.select")}, {"enter", t("key.open")}}
	}
	if ctx.Demo() != nil {
		return []Hint{{"↑↓", t("key.select")}, {"enter", t("key.open")}, {"x", t("act.demo.stop")}}
	}
	return []Hint{{"↑↓", t("key.select")}, {"enter", t("key.open")}}
}

func (s *homeScreen) Update(ctx *Context, msg tea.Msg) tea.Cmd {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if ctx.Session == nil {
		switch km.String() {
		case "up", "k", "down", "j":
			s.first = 1 - s.first
		case "d":
			return startDemo(ctx)
		case "a":
			return switchTo("profiles", "add")
		case "enter":
			if s.first == 0 {
				return startDemo(ctx)
			}
			return switchTo("profiles", "add")
		}
		return nil
	}
	actions := actionsFor(ctx)
	switch km.String() {
	case "up", "k":
		s.sel = clamp(s.sel-1, len(actions))
	case "down", "j":
		s.sel = clamp(s.sel+1, len(actions))
	case "x":
		return stopDemo(ctx)
	case "z":
		if ctx.Demo() != nil {
			return switchTo("settings", "scenarios")
		}
	case "d":
		if ctx.Demo() == nil {
			return startDemo(ctx)
		}
	case "enter":
		s.sel = clamp(s.sel, len(actions))
		a := actions[s.sel]
		switch a.target {
		case "demo:start":
			s.sel = 0
			return startDemo(ctx)
		case "demo:stop":
			s.sel = 0
			return stopDemo(ctx)
		case "settings:scenarios":
			return switchTo("settings", "scenarios")
		}
		if a.label == "act.lookup" {
			return switchTo(a.target, "lookup")
		}
		return switchTo(a.target, nil)
	}
	return nil
}

func (s *homeScreen) View(ctx *Context, w, h int) string {
	if ctx.Session == nil {
		return s.welcome(ctx, w)
	}

	p := ctx.Session.Profile
	auth := t("auth.basic")
	if p.Auth == string(paygate.AuthOAuth2) {
		auth = t("auth.oauth2")
	}
	platform, host := paygate.BrandLabel(paygate.Brand(p.Brand)), ctx.Session.Host()
	heading, border := t("home.welcome"), ui.ColBorder
	if d := ctx.Demo(); d != nil {
		platform += ui.Muted.Render(" · " + t("home.demo.sim"))
		host = t("home.demo.host") + ui.Muted.Render(" · "+d.Host())
		heading, border = t("home.demo.title"), ui.ColDemo
	}
	card := section(t("home.active")) + "\n" + kv([][2]string{
		{t("f.profile"), ui.Bold.Render(ctx.Session.Name)},
		{t("f.platform"), platform},
		{t("f.environment"), ui.EnvBadge(p.Env)},
		{t("f.host"), host},
		{t("f.merchant"), p.MerchantID},
		{t("f.auth"), auth},
	})

	out := title(heading) + "\n\n" + box(card, min(w, 64), border) + "\n"
	switch {
	case ctx.Verified && ctx.Demo() != nil:
		out += ui.Success.Render("✓ ") + ui.Label.Render(t("home.demo.ready")) + "\n"
	case ctx.Verified:
		out += ui.Success.Render("✓ ") + ui.Label.Render(t("home.verified")) + "\n"
	case ctx.VerifyDone && ctx.VerifyErr != nil:
		out += ui.Danger.Render("! ") + ui.Label.Render(t("home.unverified")+" ") + ui.Muted.Render(wrap(ctx.VerifyErr.Error(), min(w, 64))) + "\n"
	}
	if ctx.Demo() != nil {
		out += lipgloss.NewStyle().Foreground(ui.ColDemo).Render("◆ ") + ui.Label.Render(wrap(t("home.demo.safe"), min(w-2, 72))) + "\n"
	}
	if p.IsProd() {
		out += ui.Danger.Render("! ") + ui.Label.Render(t("home.prod")) + "\n"
	}
	out += "\n" + section(t("home.actions")) + "\n"
	actions := actionsFor(ctx)
	s.sel = clamp(s.sel, len(actions))
	for i, a := range actions {
		row := ui.Accent.Render(a.key) + "  " + t(a.label)
		out += selectable(ctx, row, i == s.sel, min(w, 40)) + "\n"
	}
	return out + recentPayments(ctx, w)
}

// welcome is the first screen without a profile: start the demo, or
// connect a merchant.
func (s *homeScreen) welcome(ctx *Context, w int) string {
	out := title(t("home.first")) + "\n\n" + ui.Label.Render(wrap(t("home.choose"), min(w, 72))) + "\n\n"
	if ctx.SessionErr != nil && !errors.Is(ctx.SessionErr, config.ErrNoProfile) {
		out += ui.Warning.Render(t("home.session.err")) + "\n" + ui.Muted.Render(wrap(ctx.SessionErr.Error(), min(w, 72))) + "\n\n"
	}
	options := []struct {
		key, label, desc, badge string
		color                   lipgloss.TerminalColor
	}{
		{"d", t("home.demo.opt"), t("home.demo.opt.d"), ui.EnvBadge(config.EnvDemo), ui.ColDemo},
		{"a", t("home.add.long"), t("home.add.d"), "", ui.ColAccent},
	}
	cards := make([]string, len(options))
	for i, o := range options {
		cards[i] = optionCard(o.key, o.label, o.desc, o.badge, i == s.first, o.color, min(w, 66))
	}
	return out + strings.Join(cards, "\n\n")
}

// optionCard is one choice of the welcome screen: a framed card with key,
// label and description; the selected one is framed in its colour.
func optionCard(key, label, desc, badge string, selected bool, color lipgloss.TerminalColor, w int) string {
	inner := w - 4 // border and padding
	head := ui.Accent.Render(key) + "  "
	var border lipgloss.TerminalColor = ui.ColBorder
	if selected {
		head += ui.Bold.Render(label)
		border = color
	} else {
		head += ui.Label.Render(label)
	}
	if badge != "" {
		head += strings.Repeat(" ", max(inner-lipgloss.Width(head)-lipgloss.Width(badge), 2)) + badge
	}
	body := lipgloss.NewStyle().PaddingLeft(3).Render(ui.Muted.Render(wrap(desc, inner-3)))
	return box(head+"\n"+body, w, border)
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
	recent := mine[:min(len(mine), 3)]
	cols := paymentColumns(recent, w-4)
	for _, r := range recent {
		out += "  " + listRow(r, cols, w-4) + "\n"
	}
	return out
}
