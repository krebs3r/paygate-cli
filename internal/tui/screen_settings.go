package tui

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/core"
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
		{"theme", "set.theme", false},
		{"splash", "set.splash", false},
		{"currency", "set.currency", true},
		{"language", "set.pagelang", true},
		{"return_url", "set.return", true},
		{"cancel_url", "set.cancel", true},
		{"webhook_url", "set.webhook", true},
		{"hmac_key", "set.hmac", true},
		{"customer_ip", "set.ip", true},
		{"reset", "set.reset", false},
	}
}

type settingsScreen struct {
	sel     int
	form    *huh.Form
	editing string
	value   string
	choice  string // hmac_key: replace or remove
	confirm bool
	width   int
}

func newSettingsScreen() *settingsScreen { return &settingsScreen{width: 70} }

func (s *settingsScreen) ID() string                { return "settings" }
func (s *settingsScreen) Title() string             { return t("menu.settings") }
func (s *settingsScreen) Shortcut() string          { return "s" }
func (s *settingsScreen) Activate(*Context) tea.Cmd { return nil }
func (s *settingsScreen) Capturing() bool           { return s.form != nil }
func (s *settingsScreen) Back(*Context) (bool, tea.Cmd) {
	if s.form != nil {
		s.form = nil
		return true, nil
	}
	return false, nil
}

func (s *settingsScreen) Hints(*Context) []Hint {
	if s.form != nil && s.editing == "reset" {
		return []Hint{{"← →", t("key.select")}, {"enter", t("key.open")}, {"esc", t("key.cancel")}}
	}
	if s.form != nil {
		return []Hint{{"enter", t("key.open")}, {"esc", t("key.cancel")}}
	}
	return []Hint{{"↑↓", t("key.select")}, {"enter", t("set.change")}}
}

// Receive opens the factory reset from the command palette.
func (s *settingsScreen) Receive(ctx *Context, data any) tea.Cmd {
	if data != "reset" {
		return nil
	}
	items := s.visible(ctx)
	for i, st := range items {
		if st.id == "reset" {
			s.sel = i
			return s.edit(ctx, st)
		}
	}
	return nil
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
	case "reset":
		return ui.Muted.Render(t("set.reset.what")), false
	case "theme":
		return themeLabel(ui.ThemeID()), ui.ThemeID() == ui.Themes[0].ID
	case "splash":
		if cfg.NoSplash {
			return t("set.off"), false
		}
		return t("set.on"), true
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
	case "hmac_key":
		// From the profile only: the view is drawn often, the keychain is slow.
		switch {
		case os.Getenv("PAYGATE_HMAC_KEY") != "":
			return t("set.hmac.env"), false
		case p.HMACStore == config.StoreKeychain:
			return t("set.hmac.keychain"), false
		case p.HMACStore == config.StoreFile:
			return t("set.hmac.file"), false
		}
		return ui.Muted.Render(t("set.hmac.none")), false
	case "customer_ip":
		if p.CustomerIP == "" {
			return core.LocalIP() + " " + t("set.ip.auto"), true
		}
		return p.CustomerIP, false
	}
	return "", true
}

func (s *settingsScreen) Update(ctx *Context, msg tea.Msg) tea.Cmd {
	if s.form != nil {
		f, cmd := s.form.Update(msg)
		s.form = f.(*huh.Form)
		switch s.form.State {
		case huh.StateAborted:
			s.form, s.value = nil, ""
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
	case "reset":
		s.confirm = false
		dir, _ := config.Dir()
		field = huh.NewConfirm().Title(t("set.reset.q")).
			Description(fmt.Sprintf(t("set.reset.d"), config.Tilde(dir))).
			Affirmative(t("set.reset.yes")).Negative(t("no")).
			Value(&s.confirm)
	case "theme":
		s.value = ui.ThemeID()
		var opts []huh.Option[string]
		for _, th := range ui.Themes {
			opts = append(opts, huh.NewOption(fmt.Sprintf("%-10s", themeLabel(th.ID))+ui.Swatch(th.ID), th.ID))
		}
		field = huh.NewSelect[string]().Title(t(st.label)).Options(opts...).Value(&s.value)
	case "splash":
		s.value = "on"
		if ctx.Cfg.NoSplash {
			s.value = "off"
		}
		field = huh.NewSelect[string]().Title(t(st.label)).Options(
			huh.NewOption(t("set.on"), "on"),
			huh.NewOption(t("set.off"), "off"),
		).Value(&s.value)
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
	case "hmac_key":
		s.value, s.choice = "", "replace"
		input := huh.NewGroup(huh.NewInput().Title(t(st.label)).Description(t("set.hmac.desc")).
			EchoMode(huh.EchoModePassword).Value(&s.value))
		if ctx.Session.Profile.HMACStore == "" {
			s.form = newForm(s.width, input)
			return s.form.Init()
		}
		s.form = newForm(s.width,
			huh.NewGroup(huh.NewSelect[string]().Title(t("set.hmac.action")).Options(
				huh.NewOption(t("set.hmac.replace"), "replace"),
				huh.NewOption(t("set.hmac.remove"), "remove"),
			).Value(&s.choice)),
			input.WithHideFunc(func() bool { return s.choice != "replace" }),
		)
		return s.form.Init()
	case "customer_ip":
		s.value = ctx.Session.Profile.CustomerIP
		field = huh.NewInput().Title(t(st.label)).Description(t("set.ip.desc")).Placeholder(core.LocalIP()).Value(&s.value).Validate(func(v string) error {
			if v = strings.TrimSpace(v); v != "" && net.ParseIP(v) == nil {
				return errors.New(t("set.ip.invalid"))
			}
			return nil
		})
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
	if s.editing == "reset" {
		if !s.confirm {
			return nil
		}
		return s.reset()
	}
	if s.editing == "hmac_key" {
		return s.applyHMACKey(ctx)
	}
	v := strings.TrimSpace(s.value)
	if s.editing == "theme" {
		setTheme(ctx.Cfg, v)
	} else if s.editing == "splash" {
		ctx.Cfg.NoSplash = v == "off"
	} else if s.editing == "ui_language" {
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
		case "customer_ip":
			p.CustomerIP = v
		}
	}
	if err := ctx.Cfg.Save(); err != nil {
		return toast(err.Error(), ui.ToneDanger)
	}
	return toast(t("set.saved"), ui.ToneSuccess)
}

// applyHMACKey stores, replaces or removes the webhook HMAC key of the
// active profile. An empty entry changes nothing.
func (s *settingsScreen) applyHMACKey(ctx *Context) tea.Cmd {
	name, p := ctx.Session.Name, ctx.Session.Profile
	key := config.CleanSecret(s.value)
	s.value = ""
	done := "set.hmac.saved"
	switch {
	case s.choice == "remove" && p.HMACStore != "":
		config.DeleteHMACKey(name, p)
		done = "set.hmac.removed"
	case key == "":
		return nil
	default:
		if _, err := config.StoreHMACKey(name, p, key); err != nil {
			return toast(err.Error(), ui.ToneDanger)
		}
	}
	if err := ctx.Cfg.Save(); err != nil {
		return toast(err.Error(), ui.ToneDanger)
	}
	// This app's listener runs for the active profile only (it stops on a
	// switch), so it checks the next webhooks with the new key.
	if ctx.Listen != nil {
		k, _ := config.HMACKey(name, p)
		ctx.Listen.Server.SetKey(k)
	}
	return toast(t(done), ui.ToneSuccess)
}

// themeLabel names a colour theme.
func themeLabel(id string) string {
	return t("theme." + id)
}

// setTheme switches the colours at once and remembers the theme; the
// default is stored as empty.
func setTheme(cfg *config.Config, id string) {
	ui.SetTheme(id)
	cfg.Theme = ui.ThemeID()
	if cfg.Theme == ui.Themes[0].ID {
		cfg.Theme = ""
	}
}

// reset deletes all data and starts over on the welcome screen.
func (s *settingsScreen) reset() tea.Cmd {
	if _, err := config.Reset(); err != nil {
		return toast(err.Error(), ui.ToneDanger)
	}
	s.sel = 0
	return tea.Batch(sessionChanged(), switchTo("home", nil), toast(t("set.reset.done"), ui.ToneSuccess))
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
		case st.id == "reset":
			out += "\n" + section(t("set.data")) + "\n"
		}
		lastProfile = st.profile
		val, def := s.current(ctx, st.id)
		if def && st.id != "ui_language" && st.id != "splash" && st.id != "customer_ip" {
			val += ui.Muted.Render("  " + t("set.default"))
		}
		label := fmt.Sprintf("%-26s ", truncate(t(st.label), 26))
		if st.id == "reset" {
			label = ui.Danger.Render(label)
		}
		row := label + val
		out += selectable(ctx, row, i == s.sel, w) + "\n"
	}
	if ctx.Session == nil {
		out += "\n" + ui.Muted.Render(t("set.noprofile"))
	}
	return out
}
