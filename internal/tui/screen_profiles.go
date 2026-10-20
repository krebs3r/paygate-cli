package tui

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/reqlog"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

var profileNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$`)

type profileMode int

const (
	modeList profileMode = iota
	modeForm
	modeBusy
	modeMatch
	modeDelete
)

// verifyResultMsg reports a credential check started by the profiles screen.
type verifyResultMsg struct {
	adding bool
	name   string
	err    error
	match  *config.Profile
}

type profilesScreen struct {
	sel     int
	mode    profileMode
	form    *huh.Form
	spin    spinner.Model
	busy    string
	message string
	msgTone ui.Tone
	width   int

	// add form values
	fName, fBrand, fEnv, fMID, fKey, fAuth string
	pending                                *config.Profile
	match                                  *config.Profile
	confirm                                bool
	deleting                               string
}

func newProfilesScreen() *profilesScreen {
	return &profilesScreen{spin: newSpinner(), width: 70}
}

func (s *profilesScreen) ID() string       { return "profiles" }
func (s *profilesScreen) Title() string    { return t("menu.profiles") }
func (s *profilesScreen) Shortcut() string { return "p" }
func (s *profilesScreen) Capturing() bool {
	return s.mode == modeForm || s.mode == modeMatch || s.mode == modeDelete
}

func (s *profilesScreen) Activate(ctx *Context) tea.Cmd {
	names := ctx.Cfg.Names()
	for i, n := range names {
		if ctx.Session != nil && n == ctx.Session.Name {
			s.sel = i
		}
	}
	return nil
}

func (s *profilesScreen) Receive(ctx *Context, data any) tea.Cmd {
	if data == "add" {
		return s.startForm(ctx, "")
	}
	return nil
}

func (s *profilesScreen) Back(*Context) (bool, tea.Cmd) {
	if s.mode != modeList && s.mode != modeBusy {
		s.mode, s.form = modeList, nil
		return true, nil
	}
	return false, nil
}

func (s *profilesScreen) Hints(ctx *Context) []Hint {
	switch s.mode {
	case modeForm:
		return []Hint{{"enter", t("key.next")}, {"shift+tab", t("key.back")}, {"esc", t("key.cancel")}}
	case modeMatch, modeDelete:
		return []Hint{{"← →", t("key.select")}, {"enter", t("key.open")}, {"esc", t("key.cancel")}}
	case modeBusy:
		return nil
	}
	hints := []Hint{{"a", t("prof.add")}}
	if len(ctx.Cfg.Profiles) > 0 {
		hints = append([]Hint{{"↑↓", t("key.select")}, {"enter", t("prof.switch")}}, hints...)
		hints = append(hints, Hint{"v", t("prof.verify")}, Hint{"d", t("prof.delete")})
	}
	return hints
}

func (s *profilesScreen) selected(ctx *Context) (string, *config.Profile) {
	names := ctx.Cfg.Names()
	if len(names) == 0 {
		return "", nil
	}
	s.sel = clamp(s.sel, len(names))
	return names[s.sel], ctx.Cfg.Profiles[names[s.sel]]
}

func (s *profilesScreen) Update(ctx *Context, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		s.spin, cmd = s.spin.Update(msg)
		return cmd
	case verifyResultMsg:
		return s.onVerified(ctx, msg)
	}

	switch s.mode {
	case modeForm, modeMatch, modeDelete:
		return s.updateForm(ctx, msg)
	case modeBusy:
		return nil
	}

	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	name, prof := s.selected(ctx)
	switch km.String() {
	case "up", "k":
		s.sel = clamp(s.sel-1, len(ctx.Cfg.Profiles))
	case "down", "j":
		s.sel = clamp(s.sel+1, len(ctx.Cfg.Profiles))
	case "a":
		return s.startForm(ctx, "")
	case "enter":
		if prof == nil {
			return s.startForm(ctx, "")
		}
		ctx.Cfg.ActiveProfile = name
		if err := ctx.Cfg.Save(); err != nil {
			return toast(err.Error(), ui.ToneDanger)
		}
		s.message = ""
		return tea.Batch(sessionChanged(), toast(fmt.Sprintf(t("prof.switched"), name), ui.ToneSuccess))
	case "v":
		if prof == nil {
			return nil
		}
		key, err := config.APIKey(name, prof)
		if err != nil {
			s.message, s.msgTone = err.Error(), ui.ToneDanger
			return nil
		}
		return s.verify(ctx, name, prof, key, false)
	case "d":
		if prof == nil {
			return nil
		}
		s.deleting, s.confirm, s.mode = name, false, modeDelete
		s.form = newForm(s.width, huh.NewGroup(huh.NewConfirm().
			Title(fmt.Sprintf(t("prof.delete.q"), name)).
			Description(t("prof.delete.d")).
			Affirmative(t("yes")).Negative(t("no")).
			Value(&s.confirm)))
		return s.form.Init()
	}
	return nil
}

func (s *profilesScreen) startForm(ctx *Context, errText string) tea.Cmd {
	if errText == "" {
		s.fName, s.fBrand, s.fEnv, s.fMID, s.fKey, s.fAuth = "", "nexi", "test", "", "", "basic"
		if len(ctx.Cfg.Profiles) == 0 {
			s.fName = "default"
		}
	}
	// From the welcome screen the form comes before the first View.
	if ctx.Width > 0 {
		s.width = ctx.Width
	}
	s.message, s.msgTone = errText, ui.ToneDanger
	s.mode = modeForm
	s.form = newForm(s.width,
		huh.NewGroup(
			huh.NewNote().Description(t("prof.form.note")),
			huh.NewInput().Title(t("prof.form.name")).Placeholder("shop-test").Value(&s.fName).Validate(func(v string) error {
				if !profileNameRe.MatchString(v) {
					return errors.New(t("prof.form.name.err"))
				}
				if _, exists := ctx.Cfg.Profiles[v]; exists {
					return errors.New(t("prof.form.exists"))
				}
				return nil
			}),
			huh.NewSelect[string]().Title(t("f.platform")).Options(
				huh.NewOption("Nexi Paygate      paygate.nexigroup.com", "nexi"),
				huh.NewOption("Computop Paygate  computop-paygate.com", "computop"),
			).Value(&s.fBrand),
			huh.NewSelect[string]().Title(t("f.environment")).Options(
				huh.NewOption(t("env.test"), "test"),
				huh.NewOption(t("env.prod"), "prod"),
			).Value(&s.fEnv),
			ui.LiveNote(s.apiTarget),
		),
		huh.NewGroup(
			huh.NewInput().Title(t("f.merchant")).Value(&s.fMID).Validate(required),
			huh.NewInput().Title("REST "+t("f.key")).EchoMode(huh.EchoModePassword).Value(&s.fKey).Validate(required),
			huh.NewSelect[string]().Title(t("f.auth")).Options(
				huh.NewOption(t("auth.basic"), "basic"),
				huh.NewOption(t("auth.oauth2"), "oauth2"),
			).Value(&s.fAuth),
			ui.LiveNote(s.authTarget),
		),
	)
	return s.form.Init()
}

// apiTarget and authTarget say where the calls go with the choice in the
// form, while it is made.
func (s *profilesScreen) apiTarget() string {
	api, _, err := core.Endpoints(&config.Profile{Brand: s.fBrand, Env: s.fEnv})
	if err != nil {
		return ""
	}
	return "API " + api
}

func (s *profilesScreen) authTarget() string {
	if s.fAuth != string(paygate.AuthOAuth2) {
		return t("auth.basic.to")
	}
	_, token, err := core.Endpoints(&config.Profile{Brand: s.fBrand, Env: s.fEnv})
	if err != nil {
		return ""
	}
	return fmt.Sprintf(t("auth.token.to"), token)
}

func required(v string) error {
	if config.CleanSecret(v) == "" {
		return errors.New(t("prof.form.empty"))
	}
	return nil
}

func (s *profilesScreen) updateForm(ctx *Context, msg tea.Msg) tea.Cmd {
	f, cmd := s.form.Update(msg)
	s.form = f.(*huh.Form)
	switch s.form.State {
	case huh.StateAborted:
		s.mode, s.form = modeList, nil
		return nil
	case huh.StateCompleted:
		mode := s.mode
		s.mode, s.form = modeList, nil
		switch mode {
		case modeForm:
			p := &config.Profile{Brand: s.fBrand, Env: s.fEnv, MerchantID: config.CleanSecret(s.fMID), Auth: s.fAuth}
			s.pending = p
			return s.verify(ctx, s.fName, p, config.CleanSecret(s.fKey), true)
		case modeMatch:
			if s.confirm && s.match != nil {
				s.pending.Brand, s.pending.Env, s.pending.Auth = s.match.Brand, s.match.Env, s.match.Auth
				return s.save(ctx)
			}
			return s.startForm(ctx, t("prof.nomatch"))
		case modeDelete:
			if s.confirm {
				return s.delete(ctx, s.deleting)
			}
		}
		return nil
	}
	return cmd
}

func (s *profilesScreen) verify(ctx *Context, name string, p *config.Profile, key string, adding bool) tea.Cmd {
	origin, _ := core.Origin(p)
	s.mode = modeBusy
	s.busy = fmt.Sprintf(t("prof.verifying"), strings.TrimPrefix(origin, "https://"))
	version := ctx.Version
	check := func() tea.Msg {
		factory := func(p *config.Profile) (*paygate.Client, error) {
			return core.NewClient(p, key, core.ClientOptions{Version: version, Profile: name})
		}
		c, err := factory(p)
		if err != nil {
			return verifyResultMsg{adding: adding, name: name, err: err}
		}
		vctx, cancel := context.WithTimeout(reqlog.WithoutLog(context.Background()), core.ProbeTimeout)
		defer cancel()
		err = c.Verify(vctx)
		if err == nil || !errors.Is(err, paygate.ErrUnauthorized) {
			return verifyResultMsg{adding: adding, name: name, err: err}
		}
		match, _ := core.FindWorkingSettings(context.Background(), p, factory)
		return verifyResultMsg{adding: adding, name: name, err: err, match: match}
	}
	return tea.Batch(s.spin.Tick, check)
}

func (s *profilesScreen) onVerified(ctx *Context, msg verifyResultMsg) tea.Cmd {
	s.mode = modeList
	if msg.err == nil {
		if msg.adding {
			return s.save(ctx)
		}
		_, p := s.selected(ctx)
		origin, _ := core.Origin(p)
		s.message, s.msgTone = fmt.Sprintf(t("prof.verified"), strings.TrimPrefix(origin, "https://")), ui.ToneSuccess
		return nil
	}
	if msg.match != nil && msg.adding {
		s.match, s.confirm, s.mode = msg.match, true, modeMatch
		what := paygate.BrandLabel(paygate.Brand(msg.match.Brand)) + " · " + envLabel(msg.match.Env) + " · " + authName(msg.match.Auth)
		desc := ""
		if msg.match.IsProd() {
			desc = t("prof.prodwarn")
		}
		s.form = newForm(s.width, huh.NewGroup(huh.NewConfirm().
			Title(fmt.Sprintf(t("prof.match"), what)).
			Description(strings.TrimSpace(t("prof.match.use")+"\n"+desc)).
			Affirmative(t("yes")).Negative(t("no")).
			Value(&s.confirm)))
		return s.form.Init()
	}
	text := msg.err.Error()
	if errors.Is(msg.err, paygate.ErrUnauthorized) {
		text = t("prof.rejected") + " " + t("prof.nomatch")
	}
	if msg.adding {
		return s.startForm(ctx, text)
	}
	s.message, s.msgTone = text, ui.ToneDanger
	return nil
}

func (s *profilesScreen) save(ctx *Context) tea.Cmd {
	p := s.pending
	if _, err := config.StoreAPIKey(s.fName, p, config.CleanSecret(s.fKey)); err != nil {
		return toast(err.Error(), ui.ToneDanger)
	}
	ctx.Cfg.Profiles[s.fName] = p
	ctx.Cfg.ActiveProfile = s.fName
	if err := ctx.Cfg.Save(); err != nil {
		return toast(err.Error(), ui.ToneDanger)
	}
	s.fKey, s.pending, s.match = "", nil, nil
	s.message = ""
	for i, n := range ctx.Cfg.Names() {
		if n == s.fName {
			s.sel = i
		}
	}
	return tea.Batch(sessionChanged(), toast(fmt.Sprintf(t("prof.saved"), s.fName), ui.ToneSuccess))
}

func (s *profilesScreen) delete(ctx *Context, name string) tea.Cmd {
	delete(ctx.Cfg.Profiles, name)
	config.DeleteAPIKey(name)
	if ctx.Cfg.ActiveProfile == name {
		ctx.Cfg.ActiveProfile = ""
		if names := ctx.Cfg.Names(); len(names) > 0 {
			ctx.Cfg.ActiveProfile = names[0]
		}
	}
	if err := ctx.Cfg.Save(); err != nil {
		return toast(err.Error(), ui.ToneDanger)
	}
	s.sel = clamp(s.sel, len(ctx.Cfg.Profiles))
	return tea.Batch(sessionChanged(), toast(fmt.Sprintf(t("prof.deleted"), name), ui.ToneSuccess))
}

func (s *profilesScreen) View(ctx *Context, w, h int) string {
	s.width = w
	out := title(t("prof.title")) + "\n\n"
	if s.mode == modeForm {
		out = title(t("prof.title")) + ui.Muted.Render("  ·  ") + ui.Label.Render(t("prof.form.title")) + "\n\n"
	}
	switch s.mode {
	case modeForm, modeMatch, modeDelete:
		if s.message != "" && s.mode == modeForm {
			out += ui.Danger.Render("! ") + ui.Label.Render(wrap(s.message, min(w-2, 74))) + "\n\n"
		}
		return out + s.form.View()
	case modeBusy:
		return out + spinView(s.spin) + " " + ui.Label.Render(s.busy)
	}

	names := ctx.Cfg.Names()
	if len(names) == 0 {
		return out + ui.Label.Render(wrap(t("prof.empty"), min(w, 72)))
	}
	for i, n := range names {
		p := ctx.Cfg.Profiles[n]
		mark := "  "
		if ctx.Session != nil && n == ctx.Session.Name {
			mark = ui.Success.Render("● ")
		}
		row := mark + fmt.Sprintf("%-16s ", truncate(n, 16)) + ui.EnvBadge(p.IsProd()) + "  " +
			ui.Label.Render(fmt.Sprintf("%-9s", p.Brand)) + " " + p.MerchantID + ui.Muted.Render("  · "+authName(p.Auth))
		out += selectable(ctx, row, i == s.sel, w) + "\n"
	}
	if s.message != "" {
		style := ui.Success
		if s.msgTone == ui.ToneDanger {
			style = ui.Danger
		}
		out += "\n" + style.Render(wrap(s.message, min(w, 74)))
	}
	return out
}

func envLabel(env string) string {
	if env == "prod" {
		return "PROD"
	}
	return "TEST"
}

func authName(a string) string {
	if a == string(paygate.AuthOAuth2) {
		return "OAuth2"
	}
	return "Basic"
}
