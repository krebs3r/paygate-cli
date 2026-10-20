package tui

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"github.com/krebs3r/paygate-cli/internal/i18n"
	"github.com/krebs3r/paygate-cli/internal/money"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

type setting struct {
	id      string
	label   string
	profile bool
}

func settingsList() []setting {
	return []setting{
		{"ui_language", "set.language", false},
		{"currency", "set.currency", true},
		{"language", "set.pagelang", true},
		{"return_url", "set.return", true},
		{"cancel_url", "set.cancel", true},
		{"webhook_url", "set.webhook", true},
	}
}

type settingsScreen struct {
	sel     int
	form    *huh.Form
	editing string
	value   string
	width   int
}

func newSettingsScreen() *settingsScreen { return &settingsScreen{width: 70} }

func (s *settingsScreen) ID() string                { return "settings" }
func (s *settingsScreen) Title() string             { return t("menu.settings") }
func (s *settingsScreen) Shortcut() string          { return "s" }
func (s *settingsScreen) Activate(*Context) tea.Cmd { return nil }
func (s *settingsScreen) Capturing() bool           { return s.form != nil }
func (s *settingsScreen) Back(*Context) bool {
	if s.form != nil {
		s.form = nil
		return true
	}
	return false
}

func (s *settingsScreen) Hints(*Context) []Hint {
	if s.form != nil {
		return []Hint{{"enter", t("key.open")}, {"esc", t("key.cancel")}}
	}
	return []Hint{{"↑↓", t("key.select")}, {"enter", t("set.change")}}
}

func (s *settingsScreen) visible(ctx *Context) []setting {
	var out []setting
	for _, st := range settingsList() {
		if st.profile && ctx.Session == nil {
			continue
		}
		out = append(out, st)
	}
	return out
}

func (s *settingsScreen) current(ctx *Context, id string) (value string, isDefault bool) {
	cfg := ctx.Cfg
	switch id {
	case "ui_language":
		switch cfg.UILanguage {
		case "en":
			return "English", false
		case "de":
			return "Deutsch", false
		}
		return t("set.auto"), true
	}
	p := ctx.Session.Profile
	switch id {
	case "currency":
		return p.DefaultCurrency(), p.Currency == ""
	case "language":
		return p.DefaultLanguage(), p.Language == ""
	case "return_url":
		return p.Return(), p.ReturnURL == ""
	case "cancel_url":
		return p.Cancel(), p.CancelURL == ""
	case "webhook_url":
		return p.Webhook(), p.WebhookURL == ""
	}
	return "", true
}

func (s *settingsScreen) Update(ctx *Context, msg tea.Msg) tea.Cmd {
	if s.form != nil {
		f, cmd := s.form.Update(msg)
		s.form = f.(*huh.Form)
		switch s.form.State {
		case huh.StateAborted:
			s.form = nil
		case huh.StateCompleted:
			s.form = nil
			return s.apply(ctx)
		}
		return cmd
	}
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	items := s.visible(ctx)
	switch km.String() {
	case "up", "k":
		s.sel = clamp(s.sel-1, len(items))
	case "down", "j":
		s.sel = clamp(s.sel+1, len(items))
	case "enter":
		s.sel = clamp(s.sel, len(items))
		return s.edit(ctx, items[s.sel])
	}
	return nil
}

func (s *settingsScreen) edit(ctx *Context, st setting) tea.Cmd {
	s.editing = st.id
	var field huh.Field
	switch st.id {
	case "ui_language":
		s.value = ctx.Cfg.UILanguage
		if s.value == "" {
			s.value = "auto"
		}
		field = huh.NewSelect[string]().Title(t(st.label)).Options(
			huh.NewOption(t("set.auto"), "auto"),
			huh.NewOption("English", "en"),
			huh.NewOption("Deutsch", "de"),
		).Value(&s.value)
	case "currency":
		s.value = ctx.Session.Profile.DefaultCurrency()
		field = huh.NewSelect[string]().Title(t(st.label)).Options(huh.NewOptions(money.Common...)...).Value(&s.value)
	case "language":
		s.value = ctx.Session.Profile.DefaultLanguage()
		field = huh.NewSelect[string]().Title(t(st.label)).Options(
			huh.NewOption("English", "en"), huh.NewOption("Deutsch", "de"), huh.NewOption("Français", "fr"),
			huh.NewOption("Español", "es"), huh.NewOption("Italiano", "it"), huh.NewOption("Nederlands", "nl"),
		).Value(&s.value)
	default:
		s.value, _ = s.current(ctx, st.id)
		field = huh.NewInput().Title(t(st.label)).Description(t("set.https")).Value(&s.value).Validate(func(v string) error {
			if v = strings.TrimSpace(v); v != "" && !strings.HasPrefix(v, "https://") {
				return errors.New(t("set.https"))
			}
			return nil
		})
	}
	s.form = newForm(s.width, huh.NewGroup(field))
	return s.form.Init()
}

func (s *settingsScreen) apply(ctx *Context) tea.Cmd {
	v := strings.TrimSpace(s.value)
	if s.editing == "ui_language" {
		if v == "auto" {
			v = ""
		}
		ctx.Cfg.UILanguage = v
		i18n.Set(i18n.Detect(v))
	} else {
		p := ctx.Session.Profile
		switch s.editing {
		case "currency":
			p.Currency = v
		case "language":
			p.Language = v
		case "return_url":
			p.ReturnURL = v
		case "cancel_url":
			p.CancelURL = v
		case "webhook_url":
			p.WebhookURL = v
		}
	}
	if err := ctx.Cfg.Save(); err != nil {
		return toast(err.Error(), ui.ToneDanger)
	}
	return toast(t("set.saved"), ui.ToneSuccess)
}

func (s *settingsScreen) View(ctx *Context, w, h int) string {
	s.width = w
	out := title(t("set.title")) + "\n\n"
	if s.form != nil {
		return out + s.form.View()
	}
	items := s.visible(ctx)
	s.sel = clamp(s.sel, len(items))
	lastProfile := false
	for i, st := range items {
		switch {
		case i == 0:
			out += section(t("set.app")) + "\n"
		case st.profile && !lastProfile:
			out += "\n" + section(fmt.Sprintf(t("set.profile"), strings.ToUpper(ctx.Session.Name))) + "\n"
		}
		lastProfile = st.profile
		val, def := s.current(ctx, st.id)
		if def && st.id != "ui_language" {
			val += ui.Muted.Render("  " + t("set.default"))
		}
		row := fmt.Sprintf("%-26s ", truncate(t(st.label), 26)) + val
		out += selectable(row, i == s.sel, w) + "\n"
	}
	if ctx.Session == nil {
		out += "\n" + ui.Muted.Render(t("set.noprofile"))
	}
	return out
}
