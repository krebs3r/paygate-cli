package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/history"
	"github.com/krebs3r/paygate-cli/internal/listen"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/reqlog"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

type profileMode int

const (
	modeList profileMode = iota
	modeForm
	modeBusy
	modeMatch
	modeDelete
)

// verifyPurpose is why the profiles screen checks credentials.
type verifyPurpose int

const (
	verifyCheck verifyPurpose = iota // v on a saved profile
	verifyAdd                        // before a new profile is saved
	verifyEdit                       // before changed credentials are saved
)

// verifyResultMsg reports a credential check started by the profiles screen.
type verifyResultMsg struct {
	purpose verifyPurpose
	name    string
	err     error
	match   *config.Profile
}

type profilesScreen struct {
	sel     int
	offset  int
	counts  map[string]profileCounts
	mode    profileMode
	form    *huh.Form
	spin    spinner.Model
	busy    string
	message string
	msgTone ui.Tone
	width   int

	// add and edit form values; editing is the name of the profile being
	// edited, empty while adding
	editing                                string
	fName, fBrand, fEnv, fMID, fKey, fAuth string
	fWantHMAC                              bool
	fHMAC                                  string
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
	s.counts = countLocal()
	return nil
}

// profileCounts is what is stored on this computer for a profile.
type profileCounts struct{ payments, webhooks, calls int }

// countLocal counts the stored payments, webhooks and API calls per profile.
func countLocal() map[string]profileCounts {
	out := map[string]profileCounts{}
	add := func(profile string, f func(*profileCounts)) {
		c := out[profile]
		f(&c)
		out[profile] = c
	}
	records, _ := history.Load()
	for _, r := range records {
		add(r.Profile, func(c *profileCounts) { c.payments++ })
	}
	events, _ := listen.Load()
	for _, e := range events {
		add(e.Profile, func(c *profileCounts) { c.webhooks++ })
	}
	entries, _ := reqlog.Load()
	for _, e := range entries {
		add(e.Profile, func(c *profileCounts) { c.calls++ })
	}
	return out
}

func (s *profilesScreen) Receive(ctx *Context, data any) tea.Cmd {
	switch data {
	case "add":
		s.editing = ""
		return s.startForm(ctx, "")
	case "edit":
		if ctx.Session != nil {
			return s.startEdit(ctx, ctx.Session.Name)
		}
	}
	return nil
}

func (s *profilesScreen) Back(*Context) (bool, tea.Cmd) {
	if s.mode != modeList && s.mode != modeBusy {
		s.mode, s.form, s.editing = modeList, nil, ""
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
		hints = append([]Hint{hints[0], hints[1], {"e", t("prof.edit")}}, hints[2:]...)
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
		s.editing = ""
		return s.startForm(ctx, "")
	case "e":
		if prof != nil {
			return s.startEdit(ctx, name)
		}
	case "enter":
		if prof == nil {
			s.editing = ""
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
		return s.verify(ctx, name, prof, key, verifyCheck)
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

// startEdit opens the form with a saved profile's name and connection.
func (s *profilesScreen) startEdit(ctx *Context, name string) tea.Cmd {
	p, ok := ctx.Cfg.Profiles[name]
	if !ok {
		return nil
	}
	s.editing = name
	s.fName, s.fBrand, s.fEnv, s.fMID, s.fKey, s.fAuth = name, p.Brand, p.Env, p.MerchantID, "", p.Auth
	s.fWantHMAC, s.fHMAC = false, ""
	if s.fAuth == "" {
		s.fAuth = string(paygate.AuthBasic)
	}
	return s.startForm(ctx, "")
}

// startForm opens the form to add a profile or, after startEdit, to edit
// one. errText is shown above it when the form comes back after a check.
func (s *profilesScreen) startForm(ctx *Context, errText string) tea.Cmd {
	if errText == "" && s.editing == "" {
		s.fName, s.fBrand, s.fEnv, s.fMID, s.fKey, s.fAuth = "", "nexi", "test", "", "", "basic"
		s.fWantHMAC, s.fHMAC = false, ""
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
	note, key := t("prof.form.note"), huh.NewInput().Title("REST "+t("f.key")).EchoMode(huh.EchoModePassword).Value(&s.fKey)
	if s.editing != "" {
		note = t("prof.edit.note")
		key = key.Description(t("prof.edit.key"))
	} else {
		key = key.Validate(required)
	}
	s.form = newForm(s.width,
		huh.NewGroup(
			huh.NewNote().Description(note),
			huh.NewInput().Title(t("prof.form.name")).Placeholder("shop-test").Value(&s.fName).Validate(func(v string) error {
				if !config.ValidName(v) {
					return errors.New(t("prof.form.name.err"))
				}
				if _, exists := ctx.Cfg.Profiles[v]; exists && v != s.editing {
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
			key,
			huh.NewSelect[string]().Title(t("f.auth")).Options(
				huh.NewOption(t("auth.basic"), "basic"),
				huh.NewOption(t("auth.oauth2"), "oauth2"),
			).Value(&s.fAuth),
			ui.LiveNote(s.authTarget),
		),
		huh.NewGroup(
			huh.NewConfirm().Title(t("prof.form.hmac.ask")).Description(t("prof.form.hmac.desc")).
				Affirmative(t("yes")).Negative(t("prof.form.hmac.later")).Value(&s.fWantHMAC),
		).WithHideFunc(func() bool { return s.editing != "" }),
		huh.NewGroup(
			huh.NewInput().Title(t("prof.form.hmac")).EchoMode(huh.EchoModePassword).Value(&s.fHMAC).Validate(required),
		).WithHideFunc(func() bool { return s.editing != "" || !s.fWantHMAC }),
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
			if s.editing != "" {
				return s.submitEdit(ctx)
			}
			p := &config.Profile{Brand: s.fBrand, Env: s.fEnv, MerchantID: config.CleanSecret(s.fMID), Auth: s.fAuth}
			s.pending = p
			return s.verify(ctx, s.fName, p, config.CleanSecret(s.fKey), verifyAdd)
		case modeMatch:
			if s.confirm && s.match != nil {
				s.pending.Brand, s.pending.Env, s.pending.Auth = s.match.Brand, s.match.Env, s.match.Auth
				if s.editing != "" {
					return s.applyEdit(ctx)
				}
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

// submitEdit saves an edited profile. Changed credentials are checked with
// Paygate first; a new name alone needs no check.
func (s *profilesScreen) submitEdit(ctx *Context) tea.Cmd {
	old := ctx.Cfg.Profiles[s.editing]
	if old == nil {
		s.editing = ""
		return nil
	}
	mid, key := config.CleanSecret(s.fMID), config.CleanSecret(s.fKey)
	if key == "" && old.Brand == s.fBrand && old.Env == s.fEnv && old.MerchantID == mid && old.Auth == s.fAuth {
		s.pending = nil
		return s.applyEdit(ctx)
	}
	if key == "" {
		var err error
		if key, err = config.APIKey(s.editing, old); err != nil {
			return s.startForm(ctx, err.Error())
		}
	}
	p := *old
	p.Brand, p.Env, p.MerchantID, p.Auth = s.fBrand, s.fEnv, mid, s.fAuth
	s.pending = &p
	return s.verify(ctx, s.fName, &p, key, verifyEdit)
}

// applyEdit renames the edited profile and takes over checked credentials.
func (s *profilesScreen) applyEdit(ctx *Context) tea.Cmd {
	from, to := s.editing, s.fName
	var moved core.RenameResult
	if to != from {
		res, err := core.RenameProfile(ctx.Cfg, from, to)
		if err != nil && ctx.Cfg.Profiles[to] == nil {
			return s.startForm(ctx, err.Error()) // nothing changed
		}
		if err != nil {
			s.editing = ""
			return tea.Batch(sessionChanged(), toast(err.Error(), ui.ToneDanger))
		}
		moved = res
	}
	p := ctx.Cfg.Profiles[to]
	if s.pending != nil {
		p.Brand, p.Env, p.MerchantID, p.Auth = s.pending.Brand, s.pending.Env, s.pending.MerchantID, s.pending.Auth
		if key := config.CleanSecret(s.fKey); key != "" {
			if _, err := config.StoreAPIKey(to, p, key); err != nil {
				return toast(err.Error(), ui.ToneDanger)
			}
		}
		if err := ctx.Cfg.Save(); err != nil {
			return toast(err.Error(), ui.ToneDanger)
		}
	}
	s.editing, s.fKey, s.pending, s.match, s.message = "", "", nil, nil, ""
	s.counts = countLocal()
	for i, n := range ctx.Cfg.Names() {
		if n == to {
			s.sel = i
		}
	}
	text := fmt.Sprintf(t("prof.updated"), to)
	if to != from {
		text = fmt.Sprintf(t("prof.renamed"), from, to, moved.Payments, moved.Webhooks)
	}
	cmds := []tea.Cmd{toast(text, ui.ToneSuccess)}
	if ctx.Session != nil && (ctx.Session.Name == from || ctx.Cfg.ActiveProfile == to) {
		cmds = append(cmds, sessionChanged())
	}
	return tea.Batch(cmds...)
}

func (s *profilesScreen) verify(ctx *Context, name string, p *config.Profile, key string, purpose verifyPurpose) tea.Cmd {
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
			return verifyResultMsg{purpose: purpose, name: name, err: err}
		}
		vctx, cancel := context.WithTimeout(reqlog.WithoutLog(context.Background()), core.ProbeTimeout)
		defer cancel()
		err = c.Verify(vctx)
		if err == nil || !errors.Is(err, paygate.ErrUnauthorized) {
			return verifyResultMsg{purpose: purpose, name: name, err: err}
		}
		match, _ := core.FindWorkingSettings(context.Background(), p, factory)
		return verifyResultMsg{purpose: purpose, name: name, err: err, match: match}
	}
	return tea.Batch(s.spin.Tick, check)
}

func (s *profilesScreen) onVerified(ctx *Context, msg verifyResultMsg) tea.Cmd {
	s.mode = modeList
	if msg.err == nil {
		switch msg.purpose {
		case verifyAdd:
			return s.save(ctx)
		case verifyEdit:
			return s.applyEdit(ctx)
		}
		_, p := s.selected(ctx)
		origin, _ := core.Origin(p)
		s.message, s.msgTone = fmt.Sprintf(t("prof.verified"), strings.TrimPrefix(origin, "https://")), ui.ToneSuccess
		return nil
	}
	if msg.match != nil && msg.purpose != verifyCheck {
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
	if msg.purpose != verifyCheck {
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
	if key := config.CleanSecret(s.fHMAC); s.fWantHMAC && key != "" {
		if _, err := config.StoreHMACKey(s.fName, p, key); err != nil {
			return toast(err.Error(), ui.ToneDanger)
		}
	}
	ctx.Cfg.Profiles[s.fName] = p
	ctx.Cfg.ActiveProfile = s.fName
	if err := ctx.Cfg.Save(); err != nil {
		return toast(err.Error(), ui.ToneDanger)
	}
	s.fKey, s.fHMAC, s.pending, s.match = "", "", nil, nil
	s.message = ""
	s.counts = countLocal()
	for i, n := range ctx.Cfg.Names() {
		if n == s.fName {
			s.sel = i
		}
	}
	return tea.Batch(sessionChanged(), toast(fmt.Sprintf(t("prof.saved"), s.fName), ui.ToneSuccess))
}

func (s *profilesScreen) delete(ctx *Context, name string) tea.Cmd {
	config.DeleteHMACKey(name, ctx.Cfg.Profiles[name])
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
		what := t("prof.form.title")
		if s.editing != "" {
			what = fmt.Sprintf(t("prof.edit.title"), s.editing)
		}
		out = title(t("prof.title")) + ui.Muted.Render("  ·  ") + ui.Label.Render(what) + "\n\n"
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
	out = title(t("prof.title")) + ui.Muted.Render("  · "+fmt.Sprintf(t("prof.count"), len(names))) + "\n\n"
	rows := make([][]string, len(names))
	for i, n := range names {
		rows[i] = profileCells(n, ctx.Cfg.Profiles[n])
	}
	headings := []string{t("prof.col.name"), t("prof.col.env"), t("prof.col.platform"), t("prof.col.merchant"), t("prof.col.auth")}
	cols := newColumns(headings, rows)
	out += ui.Muted.Render("    "+cols.row(headings...)+t("prof.col.hmac")) + "\n"

	// The detail of the selected profile and a message stay below the list.
	message := ""
	if s.message != "" {
		style := ui.Success
		if s.msgTone == ui.ToneDanger {
			style = ui.Danger
		}
		message = "\n" + style.Render(wrap(s.message, min(w, 74)))
	}
	room := max(h-lipgloss.Height(out)-3-lipgloss.Height(message), 3)
	s.sel = clamp(s.sel, len(names))
	if s.sel < s.offset {
		s.offset = s.sel
	}
	if s.sel >= s.offset+room {
		s.offset = s.sel - room + 1
	}
	for i := s.offset; i < len(names) && i < s.offset+room; i++ {
		n, p, c := names[i], ctx.Cfg.Profiles[names[i]], rows[i]
		mark := "  "
		if ctx.Session != nil && n == ctx.Session.Name {
			mark = ui.Success.Render("● ")
		}
		hmac := ui.Muted.Render("–")
		if p.HMACStore != "" {
			hmac = ui.Success.Render(t("prof.hmac.set"))
		}
		row := mark + cols.row(c[0], c[1], ui.Label.Render(c[2]), c[3], ui.Label.Render(c[4])) + hmac
		out += selectable(ctx, ansi.Truncate(row, max(w-2, 10), "…"), i == s.sel, w) + "\n"
	}
	if rest := len(names) - s.offset - room; rest > 0 {
		out += ui.Muted.Render("    "+fmt.Sprintf(t("prof.more"), rest)) + "\n"
	}
	return out + "\n" + s.detail(ctx, names[s.sel], w) + message
}

// profileCells are the values of a profile's list columns but the last.
func profileCells(name string, p *config.Profile) []string {
	return []string{truncate(name, 24), ui.EnvBadge(p.IsProd()), platformName(p.Brand), truncate(p.MerchantID, 28), authName(p.Auth)}
}

// detail tells where the selected profile's calls go, where its key is and
// what is stored for it on this computer.
func (s *profilesScreen) detail(ctx *Context, name string, w int) string {
	p := ctx.Cfg.Profiles[name]
	label := func(k string) string { return "  " + ui.Label.Render(padCells(t(k), 8)) }
	key := t("prof.key.keychain")
	if p.SecretStore == config.StoreFile {
		key = t("prof.key.file")
	}
	origin, _ := core.Origin(p)
	out := label("prof.api") + strings.TrimPrefix(origin, "https://") + ui.Muted.Render(" · "+key)
	c := s.counts[name]
	return ansi.Truncate(out, w, "…") + "\n" + label("prof.local") + fmt.Sprintf(t("prof.local.counts"), c.payments, c.webhooks, c.calls)
}

// platformName is the short platform name for lists.
func platformName(brand string) string {
	return strings.TrimSuffix(paygate.BrandLabel(paygate.Brand(brand)), " Paygate")
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
