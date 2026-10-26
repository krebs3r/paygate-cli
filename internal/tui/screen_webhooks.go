package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/history"
	"github.com/krebs3r/paygate-cli/internal/listen"
	"github.com/krebs3r/paygate-cli/internal/money"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/ui"
	"github.com/krebs3r/paygate-cli/internal/webhooks"
)

type hooksMode int

const (
	hooksList hooksMode = iota
	hooksDetail
)

type (
	// listenTickMsg refreshes the listener state and the received webhooks,
	// also those a paygate listen in another terminal stores.
	listenTickMsg   struct{}
	listenOpenedMsg struct {
		profile string
		open    *listenOpening
	}
	webhookMsg struct {
		ev listen.Event
		ch <-chan listen.Event
	}
	resendDoneMsg struct {
		answer listen.Answer
		err    error
	}
)

func listenTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return listenTickMsg{} })
}

// waitWebhook delivers the next webhook of this app's listener.
func waitWebhook(ch <-chan listen.Event, done <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		select {
		case ev := <-ch:
			return webhookMsg{ev: ev, ch: ch}
		case <-done:
			return nil
		}
	}
}

// listenOpening is a listener that is still coming up, which takes a few
// seconds with a tunnel. Quitting the app meanwhile cancels it.
type listenOpening struct {
	cancel context.CancelFunc
	done   chan struct{}
	sess   *listen.Session // set when done
	err    error
}

// closeListen ends this app's listener when the app quits, also one that
// is still coming up, so no cloudflared is left behind.
func closeListen(ctx *Context) {
	if ctx.Listen != nil {
		ctx.Listen.Close()
		ctx.Listen = nil
	}
	if o := ctx.opening; o != nil {
		ctx.opening = nil
		o.cancel()
		select {
		case <-o.done:
			if o.sess != nil {
				o.sess.Close()
			}
		case <-time.After(5 * time.Second):
		}
	}
}

type webhooksScreen struct {
	mode     hooksMode
	events   []listen.Event
	fileMod  time.Time
	fileSize int64
	sel      int
	offset   int
	width    int
	spin     spinner.Model
	busy     string
	message  string
	msgTone  ui.Tone
	hint     string // how to install cloudflared, after it was missing

	// this app's listener
	profile  string
	incoming chan listen.Event
	done     chan struct{}

	// detail
	cur    listen.Event
	check  webhooks.Check
	scroll int
	form   *huh.Form
	target string
}

func newWebhooksScreen() *webhooksScreen {
	return &webhooksScreen{spin: newSpinner(), width: 80, target: "http://localhost:3000/"}
}

func (s *webhooksScreen) ID() string       { return "webhooks" }
func (s *webhooksScreen) Title() string    { return t("menu.webhooks") }
func (s *webhooksScreen) Shortcut() string { return "w" }
func (s *webhooksScreen) Capturing() bool  { return s.form != nil }

func (s *webhooksScreen) Activate(ctx *Context) tea.Cmd {
	s.refresh(ctx, true)
	return nil
}

func (s *webhooksScreen) Back(*Context) (bool, tea.Cmd) {
	switch {
	case s.form != nil:
		s.form = nil
		return true, nil
	case s.mode == hooksDetail:
		s.mode = hooksList
		return true, nil
	}
	return false, nil
}

// QuitWarning: quitting ends this app's listener and its address.
func (s *webhooksScreen) QuitWarning(ctx *Context) string {
	if ctx.Listen != nil || ctx.opening != nil {
		return t("quit.listening")
	}
	return ""
}

// Receive runs the webhook commands of the palette and opens a webhook
// another screen points to, e.g. one in a payment's timeline.
func (s *webhooksScreen) Receive(ctx *Context, data any) tea.Cmd {
	if e, ok := data.(listen.Event); ok {
		s.openDetail(ctx, e)
		return nil
	}
	switch data {
	case "listen":
		if ctx.Listen == nil && ctx.opening == nil {
			return s.toggle(ctx)
		}
	case "stop":
		if ctx.Listen != nil || ctx.opening != nil {
			return s.toggle(ctx)
		}
	case "clear":
		n, err := listen.Clear()
		if err != nil {
			return toast(err.Error(), ui.ToneDanger)
		}
		s.mode, s.sel = hooksList, 0
		s.refresh(ctx, true)
		return toast(fmt.Sprintf(t("hooks.cleared"), n), ui.ToneSuccess)
	}
	return nil
}

// refresh updates the listener state of the active profile and, when the
// file changed, the received webhooks.
func (s *webhooksScreen) refresh(ctx *Context, force bool) {
	ctx.Listening = nil
	if ctx.Session != nil {
		if st, ok := listen.For(ctx.Session.Name); ok {
			ctx.Listening = st
		}
	}
	mod, size := hooksStamp()
	if !force && mod.Equal(s.fileMod) && size == s.fileSize {
		return
	}
	s.fileMod, s.fileSize = mod, size
	all, err := listen.Load()
	if err != nil {
		s.message, s.msgTone = err.Error(), ui.ToneDanger
		return
	}
	s.events = s.events[:0]
	for _, e := range all {
		if ctx.Session != nil && e.Profile == ctx.Session.Name {
			s.events = append(s.events, e)
		}
	}
}

func (s *webhooksScreen) Hints(ctx *Context) []Hint {
	if s.form != nil {
		return []Hint{{"enter", t("key.send")}, {"esc", t("key.cancel")}}
	}
	if s.mode == hooksDetail {
		return []Hint{{"r", t("key.resend")}, {"y", t("key.copyid")}, {"g", t("key.payment")}, {"↑↓", t("key.scroll")}, {"esc", t("key.back")}}
	}
	toggle := t("key.listen")
	switch {
	case ctx.Listen != nil:
		toggle = t("key.stoplisten")
	case ctx.opening != nil:
		toggle = t("key.cancel")
	}
	hints := []Hint{{"space", toggle}, {"↑↓", t("key.select")}, {"enter", t("key.open")}}
	if ctx.Listening != nil {
		hints = append(hints, Hint{"c", t("key.copyurl")})
	}
	return hints
}

func (s *webhooksScreen) Update(ctx *Context, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case listenTickMsg:
		s.refresh(ctx, false)
		return listenTick()
	case listenOpenedMsg:
		return s.opened(ctx, msg)
	case webhookMsg:
		s.refresh(ctx, true)
		var next tea.Cmd
		if ctx.Listen != nil && msg.ch == s.incoming {
			next = waitWebhook(s.incoming, s.done)
		}
		tone := ui.ToneSuccess
		if msg.ev.Signature == "invalid" {
			tone = ui.ToneDanger
		}
		return tea.Batch(next, toast(fmt.Sprintf(t("hooks.received"), eventSummary(msg.ev)), tone))
	case resendDoneMsg:
		s.busy = ""
		switch {
		case msg.err != nil:
			s.message, s.msgTone = msg.err.Error(), ui.ToneDanger
		case msg.answer.Accepted():
			s.message, s.msgTone = fmt.Sprintf(t("hooks.resend.ok"), msg.answer.Status), ui.ToneSuccess
		default:
			s.message, s.msgTone = fmt.Sprintf(t("hooks.resend.fail"), msg.answer.Status), ui.ToneDanger
		}
		return nil
	case sessionChangedMsg:
		var cmd tea.Cmd
		if ctx.Listen != nil && (ctx.Session == nil || ctx.Session.Name != s.profile) {
			name := s.profile
			s.stop(ctx)
			cmd = toast(fmt.Sprintf(t("hooks.stopped.prof"), name), ui.ToneWarning)
		}
		s.mode, s.sel, s.offset, s.message, s.hint = hooksList, 0, 0, "", ""
		s.refresh(ctx, true)
		return cmd
	case spinner.TickMsg:
		if s.busy == "" {
			return nil
		}
		var cmd tea.Cmd
		s.spin, cmd = s.spin.Update(msg)
		return cmd
	}

	if s.form != nil {
		f, cmd := s.form.Update(msg)
		s.form = f.(*huh.Form)
		switch s.form.State {
		case huh.StateAborted:
			s.form = nil
		case huh.StateCompleted:
			s.form = nil
			return s.resend()
		}
		return cmd
	}
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if s.mode == hooksDetail {
		return s.detailKeys(ctx, km.String())
	}
	switch km.String() {
	case "up", "k":
		s.sel = clamp(s.sel-1, len(s.events))
	case "down", "j":
		s.sel = clamp(s.sel+1, len(s.events))
	case "enter":
		if len(s.events) > 0 {
			s.openDetail(ctx, s.events[clamp(s.sel, len(s.events))])
		}
	case " ", "space":
		return s.toggle(ctx)
	case "c":
		if ctx.Listening != nil {
			if err := clipboard.WriteAll(ctx.Listening.URL); err != nil {
				return toast(err.Error(), ui.ToneDanger)
			}
			return toast(t("copied.url"), ui.ToneSuccess)
		}
	}
	return nil
}

func (s *webhooksScreen) detailKeys(ctx *Context, k string) tea.Cmd {
	switch k {
	case "up", "k":
		s.scroll = max(s.scroll-1, 0)
	case "down", "j":
		s.scroll++ // View stops at the end
	case "r":
		s.message = ""
		s.form = newForm(s.width, huh.NewGroup(
			huh.NewInput().Title(t("hooks.resend.title")).Description(t("hooks.resend.desc")).
				Value(&s.target).Validate(func(v string) error {
				u, err := url.Parse(strings.TrimSpace(v))
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
					return errors.New("http(s)://…")
				}
				return nil
			}),
		))
		return s.form.Init()
	case "y":
		if s.cur.PayID == "" {
			return nil
		}
		if err := clipboard.WriteAll(s.cur.PayID); err != nil {
			return toast(err.Error(), ui.ToneDanger)
		}
		return toast(t("copied.payid"), ui.ToneSuccess)
	case "g":
		for _, id := range []string{s.cur.TransID, s.cur.PayID} {
			if id == "" {
				continue
			}
			if rec, ok := history.Find(s.cur.Profile, id); ok {
				return switchTo("payments", rec)
			}
		}
		return toast(t("hooks.nopayment"), ui.ToneWarning)
	}
	return nil
}

func (s *webhooksScreen) openDetail(ctx *Context, e listen.Event) {
	key := ""
	if ctx.Session != nil && ctx.Session.Name == e.Profile {
		key, _ = config.HMACKey(e.Profile, ctx.Session.Profile)
	}
	s.mode, s.cur, s.message, s.scroll = hooksDetail, e, "", 0
	s.check = webhooks.Verify(e.Header, e.Body, key, e.Time)
}

func (s *webhooksScreen) resend() tea.Cmd {
	to, e := strings.TrimSpace(s.target), s.cur
	s.busy = t("hooks.resending")
	return tea.Batch(s.spin.Tick, func() tea.Msg {
		a, err := listen.Resend(context.Background(), to, e)
		return resendDoneMsg{answer: a, err: err}
	})
}

// toggle starts or stops this app's listener, or cancels the start.
func (s *webhooksScreen) toggle(ctx *Context) tea.Cmd {
	if ctx.Listen != nil {
		s.stop(ctx)
		return toast(t("hooks.stopped"), ui.ToneSuccess)
	}
	if ctx.opening != nil {
		ctx.opening.cancel()
		return nil
	}
	if ctx.Session == nil {
		return nil
	}
	open, err := s.start(ctx)
	if err != nil {
		s.message, s.msgTone = err.Error(), ui.ToneWarning
		return nil
	}
	s.busy = t("hooks.starting")
	if core.LocalOrigin(ctx.Session.Profile) {
		s.busy = t("hooks.starting.loc")
	}
	return tea.Batch(s.spin.Tick, open)
}

// start prepares the listener of the active profile and returns the
// command that opens it, which takes a few seconds with a tunnel.
func (s *webhooksScreen) start(ctx *Context) (tea.Cmd, error) {
	if st, ok := listen.Running(); ok {
		return nil, fmt.Errorf(t("hooks.other"), st.Profile, st.PID)
	}
	name, p := ctx.Session.Name, ctx.Session.Profile
	key, err := config.HMACKey(name, p)
	if err != nil {
		return nil, err
	}
	ch, done := make(chan listen.Event, 32), make(chan struct{})
	s.incoming, s.done, s.profile, s.hint, s.message = ch, done, name, "", ""
	opts := listen.SessionOptions{
		Options: listen.Options{Profile: name, Key: key, OnEvent: func(e listen.Event) {
			select {
			case ch <- e:
			default:
			}
		}},
		Local: core.LocalOrigin(p),
	}
	c, cancel := context.WithCancel(context.Background())
	o := &listenOpening{cancel: cancel, done: make(chan struct{})}
	ctx.opening = o
	return func() tea.Msg {
		o.sess, o.err = listen.Open(c, opts)
		close(o.done)
		return listenOpenedMsg{profile: name, open: o}
	}, nil
}

func (s *webhooksScreen) opened(ctx *Context, msg listenOpenedMsg) tea.Cmd {
	o := msg.open
	if ctx.opening != o {
		return nil // closed by closeListen
	}
	ctx.opening, s.busy = nil, ""
	o.cancel()
	switch {
	case errors.Is(o.err, context.Canceled):
		return nil
	case errors.Is(o.err, listen.ErrNoCloudflared):
		s.hint = listen.InstallHint()
		return nil
	case o.err != nil:
		s.message, s.msgTone = o.err.Error(), ui.ToneDanger
		return nil
	}
	if ctx.Session == nil || ctx.Session.Name != msg.profile {
		o.sess.Close() // the profile changed meanwhile
		return nil
	}
	ctx.Listen = o.sess
	s.refresh(ctx, false)
	return tea.Batch(waitWebhook(s.incoming, s.done), toast(t("hooks.started"), ui.ToneSuccess))
}

func (s *webhooksScreen) stop(ctx *Context) {
	if ctx.Listen == nil {
		return
	}
	ctx.Listen.Close()
	ctx.Listen = nil
	close(s.done)
	s.refresh(ctx, false)
}

// ---- view -------------------------------------------------------------------

func (s *webhooksScreen) View(ctx *Context, w, h int) string {
	s.width = w
	if s.mode == hooksDetail {
		return s.detailView(ctx, w, h)
	}
	// The status goes below the title when both do not fit on one line.
	out := title(t("hooks.title")) + "  " + s.statusLine(ctx)
	if lipgloss.Width(out) >= w {
		out = title(t("hooks.title")) + "\n" + s.statusLine(ctx)
	}
	out += "\n"
	if u := s.url(ctx); u != "" {
		out += ui.Muted.Render(t("hooks.url")+"  ") + ui.Link.Render(truncate(u, w-6)) + "\n"
	}
	if ctx.Listen != nil && ctx.IsProd() {
		out += ui.Warning.Render("! ") + ui.Label.Render(t("hooks.prod")) + "\n"
	}
	out += "\n"
	if s.busy != "" {
		out += spinView(s.spin) + " " + ui.Label.Render(s.busy) + "\n\n"
	}
	if s.hint != "" {
		out += ui.Warning.Render("! ") + ui.Label.Render(wrap(t("hooks.nocf"), min(w-4, 72))) + "\n  " + ui.Accent.Render(s.hint) + "\n\n"
	}
	if s.message != "" {
		out += s.messageLine(w) + "\n\n"
	}
	if len(s.events) == 0 {
		return out + ui.Label.Render(wrap(t("hooks.empty"), min(w, 72)))
	}
	cols := hookColumns(s.events)
	out += ui.Muted.Render("  "+cols.row(t("hooks.col.time"), t("hooks.col.type"), t("hooks.col.sig"), t("hooks.col.status"),
		t("hooks.col.amount"))+t("hooks.col.id")) + "\n"
	room := max(h-lipgloss.Height(out)-1, 3)
	s.sel = clamp(s.sel, len(s.events))
	if s.sel < s.offset {
		s.offset = s.sel
	}
	if s.sel >= s.offset+room {
		s.offset = s.sel - room + 1
	}
	for i := s.offset; i < len(s.events) && i < s.offset+room; i++ {
		out += selectable(ctx, hookRow(s.events[i], cols, w-4), i == s.sel, w) + "\n"
	}
	return out
}

func (s *webhooksScreen) statusLine(ctx *Context) string {
	count := ui.Muted.Render(" · " + fmt.Sprintf(t("hooks.count"), len(s.events)))
	switch {
	case ctx.Listen != nil:
		return ui.Success.Render("● "+t("hooks.on.here")) + ui.Muted.Render(" · "+howLabel(ctx.Listen.How)) + count
	case ctx.Listening != nil:
		return ui.Success.Render("● "+fmt.Sprintf(t("hooks.on.other"), ctx.Listening.PID)) + count
	case ctx.Session != nil && core.LocalOrigin(ctx.Session.Profile):
		return ui.Muted.Render("○ "+t("hooks.off.local")) + count
	}
	return ui.Muted.Render("○ "+t("hooks.off")) + count
}

func (s *webhooksScreen) url(ctx *Context) string {
	if ctx.Listen != nil {
		return ctx.Listen.URL
	}
	if ctx.Listening != nil {
		return ctx.Listening.URL
	}
	return ""
}

func howLabel(h listen.How) string {
	switch h {
	case listen.ViaTunnel:
		return t("hooks.via.tunnel")
	case listen.ViaPublicURL:
		return t("hooks.via.public")
	}
	return t("hooks.via.local")
}

func (s *webhooksScreen) messageLine(w int) string {
	style := ui.Success
	switch s.msgTone {
	case ui.ToneDanger:
		style = ui.Danger
	case ui.ToneWarning:
		style = ui.Warning
	}
	return style.Render(wrap(s.message, min(w, 76)))
}

// hookCols are the widths of the list columns time, type, signature,
// status and amount: the longest value of the received webhooks or the
// heading, plus a gap. The trans or pay ID takes the rest of the line.
type hookCols [5]int

func hookColumns(events []listen.Event) hookCols {
	var c hookCols
	for i, k := range []string{"hooks.col.time", "hooks.col.type", "hooks.col.sig", "hooks.col.status", "hooks.col.amount"} {
		c[i] = lipgloss.Width(t(k))
	}
	for _, e := range events {
		for i, v := range hookCells(e) {
			c[i] = max(c[i], lipgloss.Width(v))
		}
	}
	for i := range c {
		c[i] += 2
	}
	return c
}

// row pads the cells, styled or not, to the column widths.
func (c hookCols) row(cells ...string) string {
	out := ""
	for i, v := range cells {
		out += padCells(v, c[i])
	}
	return out
}

func (c hookCols) width() int { return c[0] + c[1] + c[2] + c[3] + c[4] }

// hookCells are the unstyled values of a webhook's list columns.
func hookCells(e listen.Event) [5]string {
	at, now := e.Time.Local(), time.Now()
	ts := at.Format("15:04:05")
	if at.YearDay() != now.YearDay() || at.Year() != now.Year() {
		ts = at.Format("02.01. 15:04")
	}
	status, amount := "–", ""
	if e.Status != "" {
		status = e.Status
	}
	if e.Amount != nil {
		amount = money.Format(e.Amount.Value, e.Amount.Currency)
	}
	return [5]string{ts, string(e.Kind), signatureWord(e.Signature), status, amount}
}

func hookRow(e listen.Event, c hookCols, w int) string {
	v := hookCells(e)
	id := e.TransID
	if id == "" {
		id = e.PayID
	}
	if e.Repeat > 0 {
		id = "↻ " + id
	}
	statusStyle := ui.Success
	if e.Status != "" && e.Status != "OK" && e.Status != "AUTHORIZED" {
		statusStyle = ui.Danger
	} else if e.Status == "" {
		statusStyle = ui.Muted
	}
	return c.row(v[0], ui.Link.Render(v[1]), signatureText(e.Signature), statusStyle.Render(v[3]), v[4]) +
		ui.Label.Render(truncate(id, max(w-c.width(), 10)))
}

// signatureWord is the signature check of a received webhook in words.
func signatureWord(sig string) string {
	switch sig {
	case "valid", "invalid", "unchecked":
		return t("hooks.sig." + sig)
	}
	return t("hooks.sig.missing")
}

func signatureText(sig string) string {
	style := ui.Muted
	switch sig {
	case "valid":
		style = ui.Success
	case "invalid":
		style = ui.Danger
	case "unchecked":
		style = ui.Warning
	}
	return style.Render(signatureWord(sig))
}

// eventSummary is the short form of a webhook for toasts.
func eventSummary(e listen.Event) string {
	parts := []string{string(e.Kind), signatureWord(e.Signature)}
	if e.Status != "" {
		parts = append(parts, e.Status)
	}
	return strings.Join(parts, " · ")
}

func kindText(k webhooks.Kind) string {
	switch k {
	case webhooks.Enhanced:
		return t("hooks.kind.enhanced")
	case webhooks.Basic:
		return t("hooks.kind.basic")
	}
	return t("hooks.kind.unknown")
}

func (s *webhooksScreen) detailView(ctx *Context, w, h int) string {
	e := s.cur
	head := ui.Muted.Render("← "+t("hooks.title")+" / ") + ui.Label.Render(fmt.Sprintf("#%d · %s", e.ID, e.Time.Local().Format("15:04:05"))) + "\n\n"
	if s.form != nil {
		return head + s.form.View()
	}
	out := " " + ui.WebhookKindBadge(e.Kind) + "  " + ui.Label.Render(kindText(e.Kind)) + "\n\n"
	row := func(label, value string) {
		if value != "" {
			out += "  " + ui.Muted.Render(padCells(label, 12)) + value + "\n"
		}
	}
	row("Pay ID", e.PayID)
	row("Trans ID", e.TransID)
	payment := e.Status
	if e.Amount != nil {
		payment += "  " + money.Format(e.Amount.Value, e.Amount.Currency)
	}
	if e.PaymentMethod != "" {
		payment += "  " + e.PaymentMethod
	}
	row(t("hooks.payment"), strings.TrimSpace(payment))
	received := e.Time.Local().Format("02.01.2006 15:04:05")
	if e.RemoteIP != "" {
		received += ui.Muted.Render(" · " + fmt.Sprintf(t("hooks.from"), e.RemoteIP))
	}
	row(t("hooks.received.at"), received)
	answer := fmt.Sprint(e.Answer)
	if e.Repeat > 0 {
		answer += ui.Warning.Render(" · " + fmt.Sprintf(t("hooks.repeat"), e.Repeat+1, sinceText(e.SinceFirst)))
	}
	row(t("hooks.answer"), answer)
	if f := e.Forward; f != nil {
		if f.Error != "" {
			row(t("hooks.forward"), ui.Danger.Render(truncate(f.URL+" · "+f.Error, max(w-16, 20))))
		} else {
			row(t("hooks.forward"), fmt.Sprintf("%s · %d · %s", truncate(f.URL, max(w-36, 20)), f.Status, paygate.FormatDuration(f.Duration)))
		}
	}
	if s.busy != "" {
		out += "\n " + spinView(s.spin) + " " + ui.Label.Render(s.busy) + "\n"
	}
	if s.message != "" {
		out += "\n " + s.messageLine(w-2) + "\n"
	}

	out += "\n" + section(t("hooks.checks")) + "\n"
	for _, f := range s.check.Findings {
		out += ui.WebhookFinding(f, w-2) + "\n"
	}
	out += "\n" + section(t("hooks.headers")) + "\n"
	names := make([]string, 0, len(e.Header))
	for n := range e.Header {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		out += "  " + ui.Muted.Render(n+": ") + truncate(strings.Join(e.Header[n], ", "), max(w-len(n)-6, 10)) + "\n"
	}
	out += "\n" + section(fmt.Sprintf(t("hooks.body"), len(e.Body))) + "\n"
	body := string(e.Body)
	if json.Valid(e.Body) {
		body = ui.HighlightJSON(e.Body)
	}
	for _, l := range strings.Split(body, "\n") {
		out += "  " + l + "\n"
	}

	// Below the title the detail scrolls.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	room := max(h-2, 3) // the title and a blank line
	if len(lines) <= room {
		return head + strings.Join(lines, "\n")
	}
	room--
	s.scroll = min(s.scroll, len(lines)-room)
	end := s.scroll + room
	view := strings.Join(lines[s.scroll:end], "\n")
	if rest := len(lines) - end; rest > 0 {
		view += "\n" + ui.Muted.Render("  "+fmt.Sprintf(t("hooks.more"), rest))
	}
	return head + view
}

// sinceText renders the gap between deliveries: "63 s", "12 min".
func sinceText(d time.Duration) string {
	if d < 2*time.Minute {
		return fmt.Sprintf("%d s", int(d.Round(time.Second).Seconds()))
	}
	return fmt.Sprintf("%d min", int(d.Minutes()))
}

// hooksStamp tells whether the file with the received webhooks changed.
func hooksStamp() (time.Time, int64) {
	p, err := listen.Path()
	if err != nil {
		return time.Time{}, 0
	}
	info, err := os.Stat(p)
	if err != nil {
		return time.Time{}, 0
	}
	return info.ModTime(), info.Size()
}

// paymentHooks returns the stored webhooks of a payment.
func paymentHooks(rec history.Record) []listen.Event {
	all, err := listen.Load()
	if err != nil {
		return nil
	}
	var out []listen.Event
	for _, e := range all {
		if e.Profile != rec.Profile {
			continue
		}
		if (rec.TransID != "" && e.TransID == rec.TransID) || (rec.PayID != "" && e.PayID == rec.PayID) {
			out = append(out, e)
		}
	}
	return out
}

// hooksCardValue summarises a payment's webhooks for its detail card.
func hooksCardValue(hooks []listen.Event) string {
	if len(hooks) == 0 {
		return ""
	}
	last := hooks[0] // newest first
	return fmt.Sprintf("%d · %s · ", len(hooks), last.Kind) + signatureText(last.Signature)
}

// hookTimelineRow is a webhook in a payment's timeline, its time in the
// layout of the other rows.
func hookTimelineRow(e listen.Event, stamp string) string {
	line := ui.Muted.Render(e.Time.Local().Format(stamp)) + "  " + ui.Link.Render(fmt.Sprintf("%-11s", t("ev.webhook"))) +
		" " + string(e.Kind) + " · " + signatureText(e.Signature)
	if e.Status != "" {
		line += "  " + statusBadgeShort(e.Status, "")
	}
	line += ui.Muted.Render(fmt.Sprintf("  → %d", e.Answer))
	if e.Repeat > 0 {
		line += ui.Warning.Render("  ↻")
	}
	return line
}
