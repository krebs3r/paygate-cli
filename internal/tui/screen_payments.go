package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/history"
	"github.com/krebs3r/paygate-cli/internal/listen"
	"github.com/krebs3r/paygate-cli/internal/money"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

type paysMode int

const (
	paysList paysMode = iota
	paysDetail
)

type (
	detailLoadedMsg struct {
		seq int
		res *paygate.Result
		err error
	}
	lookupDoneMsg struct {
		seq int
		id  string
		res *paygate.Result
		err error
	}
	paysOpDoneMsg struct {
		seq int
		op  paygate.Operation
		res *paygate.Result
		err error
	}
)

type paymentsScreen struct {
	mode    paysMode
	records []history.Record
	sel     int
	offset  int
	filter  string
	width   int
	seq     int
	spin    spinner.Model
	busy    string
	message string
	msgTone ui.Tone

	// detail
	rec      history.Record
	payment  *paygate.Payment
	raw      []byte
	loadErr  error
	showJSON bool
	hooks    []listen.Event // webhooks received for it, newest first
	hookMod  time.Time
	hookSize int64

	// embedded forms: filter, lookup, operation
	form     *huh.Form
	formKind string
	input    string
	confirm  bool
	op       paygate.Operation
}

func newPaymentsScreen() *paymentsScreen { return &paymentsScreen{spin: newSpinner(), width: 80} }

func (s *paymentsScreen) ID() string       { return "payments" }
func (s *paymentsScreen) Title() string    { return t("menu.payments") }
func (s *paymentsScreen) Shortcut() string { return "l" }
func (s *paymentsScreen) Capturing() bool  { return s.form != nil }

func (s *paymentsScreen) Activate(ctx *Context) tea.Cmd {
	s.reload(ctx)
	return nil
}

func (s *paymentsScreen) Receive(ctx *Context, data any) tea.Cmd {
	switch v := data.(type) {
	case string:
		if v == "lookup" {
			return s.openForm("lookup")
		}
	case history.Record:
		return s.openDetail(ctx, v)
	}
	return nil
}

func (s *paymentsScreen) Back(*Context) (bool, tea.Cmd) {
	switch {
	case s.form != nil:
		s.form = nil
		return true, nil
	case s.mode == paysDetail:
		s.mode, s.showJSON = paysList, false
		s.seq++
		return true, nil
	case s.filter != "":
		s.filter, s.sel, s.offset = "", 0, 0
		return true, nil
	}
	return false, nil
}

func (s *paymentsScreen) reload(ctx *Context) {
	all, err := history.Load()
	if err != nil {
		s.message, s.msgTone = err.Error(), ui.ToneDanger
	}
	var mine []history.Record
	for _, r := range all {
		if ctx.Session != nil && r.Profile == ctx.Session.Name {
			mine = append(mine, r)
		}
	}
	s.records = mine
}

func (s *paymentsScreen) visible() []history.Record {
	if s.filter == "" {
		return s.records
	}
	q := strings.ToLower(s.filter)
	var out []history.Record
	for _, r := range s.records {
		hay := strings.ToLower(strings.Join([]string{r.TransID, r.PayID, r.Method, r.Status, r.Simulation,
			money.FormatValue(r.Amount, r.Currency), r.Currency}, " "))
		if strings.Contains(hay, q) {
			out = append(out, r)
		}
	}
	return out
}

func (s *paymentsScreen) Hints(ctx *Context) []Hint {
	if s.form != nil {
		return []Hint{{"enter", t("key.open")}, {"esc", t("key.cancel")}}
	}
	if s.mode == paysDetail {
		var hints []Hint
		if s.can(paygate.OpCapture) {
			hints = append(hints, Hint{"c", t("key.capture")})
		}
		if s.can(paygate.OpRefund) {
			hints = append(hints, Hint{"f", t("key.refund")})
		}
		if s.can(paygate.OpReversal) {
			hints = append(hints, Hint{"x", t("key.cancelauth")})
		}
		return append(hints, Hint{"r", t("key.refresh")}, Hint{"j", "JSON"}, Hint{"y", t("key.copyid")}, Hint{"esc", t("key.back")})
	}
	hints := []Hint{{"↑↓", t("key.select")}, {"enter", t("key.open")}, {"/", t("key.filter")}, {"g", t("key.lookup")}}
	if s.filter != "" {
		hints = append(hints, Hint{"esc", t("key.back")})
	}
	return hints
}

// ---- update -----------------------------------------------------------------

func (s *paymentsScreen) Update(ctx *Context, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		s.spin, cmd = s.spin.Update(msg)
		return cmd
	case sessionChangedMsg:
		s.mode, s.form, s.filter, s.sel, s.offset = paysList, nil, "", 0, 0
		s.reload(ctx)
		return nil
	case payResultMsg, pollResultMsg:
		// A payment was created or finished on the New payment screen.
		s.reload(ctx)
		return nil
	case webhookMsg, listenTickMsg:
		// Also webhooks a paygate listen in another terminal stores.
		if s.mode == paysDetail {
			s.loadHooks(false)
		}
		return nil
	case detailLoadedMsg:
		if msg.seq != s.seq {
			return nil
		}
		s.busy = ""
		s.loadErr = msg.err
		if msg.err == nil {
			s.payment, s.raw = msg.res.Payment, msg.res.Response.Body
			if r, ok := history.Find(s.rec.Profile, s.rec.TransID); ok {
				s.rec = r
			}
		}
		s.reload(ctx)
		return nil
	case lookupDoneMsg:
		if msg.seq != s.seq {
			return nil
		}
		s.busy = ""
		if msg.err != nil {
			s.message, s.msgTone = lookupError(ctx, msg.id, msg.err), ui.ToneDanger
			return nil
		}
		s.reload(ctx)
		s.payment, s.raw, s.loadErr = msg.res.Payment, msg.res.Response.Body, nil
		s.rec = recordFrom(ctx, msg.res.Payment)
		if r, ok := history.Find(ctx.Session.Name, msg.res.Payment.TransID); ok {
			s.rec = r
		}
		s.mode = paysDetail
		return nil
	case paysOpDoneMsg:
		if msg.seq != s.seq {
			return nil
		}
		s.busy = ""
		name := map[paygate.Operation]string{paygate.OpCapture: t("op.done.capture"), paygate.OpRefund: t("res.refunded"), paygate.OpReversal: t("res.cancelled")}[msg.op]
		if msg.err != nil || paygate.OutcomeOf(msg.res.Payment) == paygate.OutcomeFailed {
			reason := ""
			if msg.err != nil {
				reason = msg.err.Error()
			} else {
				reason = msg.res.Payment.ResponseDescription
			}
			s.message, s.msgTone = fmt.Sprintf(t("res.op.failed"), name, reason), ui.ToneDanger
			return s.refresh(ctx)
		}
		s.message, s.msgTone = name, ui.ToneSuccess
		return tea.Batch(toast(name, ui.ToneSuccess), s.refresh(ctx))
	}

	if s.form != nil {
		return s.updateForm(ctx, msg)
	}
	km, ok := msg.(tea.KeyMsg)
	if !ok || s.busy != "" {
		return nil
	}
	if s.mode == paysDetail {
		return s.detailKeys(ctx, km.String())
	}

	rows := s.visible()
	switch km.String() {
	case "up", "k":
		s.sel = clamp(s.sel-1, len(rows))
	case "down", "j":
		s.sel = clamp(s.sel+1, len(rows))
	case "enter":
		if len(rows) > 0 {
			return s.openDetail(ctx, rows[clamp(s.sel, len(rows))])
		}
	case "/":
		return s.openForm("filter")
	case "g":
		return s.openForm("lookup")
	}
	return nil
}

func (s *paymentsScreen) detailKeys(ctx *Context, k string) tea.Cmd {
	switch k {
	case "r":
		return s.refresh(ctx)
	case "j":
		s.showJSON = !s.showJSON
	case "y":
		id := s.rec.PayID
		if s.payment != nil && s.payment.PayID != "" {
			id = s.payment.PayID
		}
		if err := clipboard.WriteAll(id); err != nil {
			return toast(err.Error(), ui.ToneDanger)
		}
		return toast(t("copied.payid"), ui.ToneSuccess)
	case "c":
		if s.can(paygate.OpCapture) {
			return s.startOperation(ctx, paygate.OpCapture)
		}
	case "f":
		if s.can(paygate.OpRefund) {
			return s.startOperation(ctx, paygate.OpRefund)
		}
	case "x":
		if s.can(paygate.OpReversal) {
			return s.startOperation(ctx, paygate.OpReversal)
		}
	}
	return nil
}

func (s *paymentsScreen) openDetail(ctx *Context, r history.Record) tea.Cmd {
	s.mode, s.rec, s.payment, s.raw, s.loadErr, s.showJSON, s.message = paysDetail, r, nil, nil, nil, false, ""
	s.loadHooks(true)
	return s.refresh(ctx)
}

// loadHooks reads the payment's webhooks again when the file changed.
func (s *paymentsScreen) loadHooks(force bool) {
	mod, size := hooksStamp()
	if !force && mod.Equal(s.hookMod) && size == s.hookSize {
		return
	}
	s.hookMod, s.hookSize = mod, size
	s.hooks = paymentHooks(s.rec)
}

func (s *paymentsScreen) refresh(ctx *Context) tea.Cmd {
	if ctx.Session == nil || s.rec.Profile != ctx.Session.Name {
		return nil
	}
	s.seq++
	seq, sess, rec := s.seq, ctx.Session, s.rec
	s.busy = t("pays.loading")
	return tea.Batch(s.spin.Tick, func() tea.Msg {
		id := rec.PayID
		if id == "" {
			id = rec.TransID
		}
		res, err := core.Lookup(context.Background(), sess, id)
		return detailLoadedMsg{seq: seq, res: res, err: err}
	})
}

// ---- forms ------------------------------------------------------------------

func (s *paymentsScreen) openForm(kind string) tea.Cmd {
	s.formKind, s.message = kind, ""
	var field huh.Field
	switch kind {
	case "filter":
		s.input = s.filter
		field = huh.NewInput().Title(t("pays.filter")).Description(t("pays.filter.desc")).Value(&s.input)
	case "lookup":
		s.input = ""
		field = huh.NewInput().Title(t("pays.lookup")).Description(t("pays.lookup.desc")).Value(&s.input).Validate(required)
	}
	s.form = newForm(s.width, huh.NewGroup(field))
	return s.form.Init()
}

func (s *paymentsScreen) startOperation(ctx *Context, op paygate.Operation) tea.Cmd {
	s.op, s.formKind, s.confirm, s.message = op, "op", true, ""
	p := s.currentPayment()
	currency := p.Amount.Currency
	s.input = money.FormatValue(core.RemainingAmount(op, p), currency)
	titleKey, question := "op.capture.t", "op.capture.q"
	switch op {
	case paygate.OpRefund:
		titleKey, question = "op.refund.t", "op.refund.q"
	case paygate.OpReversal:
		titleKey, question = "op.cancel.t", "op.cancel.q"
	}
	desc := ""
	if ctx.IsProd() && s.rec.Simulation == "" {
		desc = t("op.prod")
	}
	amount := huh.NewInput().Title(t(titleKey) + " (" + currency + ")").Value(&s.input).Validate(func(v string) error {
		_, err := money.Parse(v, currency)
		return err
	})
	if op == paygate.OpReversal {
		// Paygate releases the whole authorization; the amount is informative.
		amount = amount.Description(t(titleKey))
	}
	s.form = newForm(s.width, huh.NewGroup(
		amount,
		huh.NewConfirm().Title(t(question)).Description(desc).Affirmative(t("yes")).Negative(t("no")).Value(&s.confirm),
	))
	return s.form.Init()
}

func (s *paymentsScreen) updateForm(ctx *Context, msg tea.Msg) tea.Cmd {
	f, cmd := s.form.Update(msg)
	s.form = f.(*huh.Form)
	switch s.form.State {
	case huh.StateAborted:
		s.form = nil
		return nil
	case huh.StateCompleted:
		s.form = nil
		v := strings.TrimSpace(s.input)
		switch s.formKind {
		case "filter":
			s.filter, s.sel, s.offset = v, 0, 0
		case "lookup":
			return s.lookup(ctx, v)
		case "op":
			if s.confirm {
				return s.runOperation(ctx, v)
			}
		}
		return nil
	}
	return cmd
}

func (s *paymentsScreen) lookup(ctx *Context, id string) tea.Cmd {
	if ctx.Session == nil {
		return nil
	}
	s.seq++
	seq, sess := s.seq, ctx.Session
	s.busy = t("pays.loading")
	return tea.Batch(s.spin.Tick, func() tea.Msg {
		res, err := core.Lookup(context.Background(), sess, id)
		return lookupDoneMsg{seq: seq, id: id, res: res, err: err}
	})
}

func (s *paymentsScreen) runOperation(ctx *Context, amountText string) tea.Cmd {
	p := s.currentPayment()
	value, err := money.Parse(amountText, p.Amount.Currency)
	if err != nil {
		s.message, s.msgTone = err.Error(), ui.ToneDanger
		return nil
	}
	s.seq++
	seq, sess, op := s.seq, ctx.Session, s.op
	payID, transID := p.PayID, p.TransID
	amount := paygate.Amount{Value: value, Currency: p.Amount.Currency}
	s.busy = t("op.running")
	return tea.Batch(s.spin.Tick, func() tea.Msg {
		res, err := core.Operate(context.Background(), sess, op, payID, transID, amount)
		return paysOpDoneMsg{seq: seq, op: op, res: res, err: err}
	})
}

// ---- state helpers ------------------------------------------------------------

// currentPayment is the freshest known state: the API answer, or the stored
// record when Paygate could not be reached.
func (s *paymentsScreen) currentPayment() *paygate.Payment {
	if s.payment != nil {
		p := *s.payment
		if p.TransID == "" {
			p.TransID = s.rec.TransID
		}
		if p.Amount == nil || p.Amount.Currency == "" {
			p.Amount = &paygate.AmountDetails{Value: s.rec.Amount, Currency: s.rec.Currency}
		}
		return &p
	}
	return &paygate.Payment{
		PayID: s.rec.PayID, TransID: s.rec.TransID, Status: s.rec.Status,
		ResponseCode: paygate.Code(s.rec.Code), ResponseDescription: s.rec.Description,
		Amount: &paygate.AmountDetails{Value: s.rec.Amount, Currency: s.rec.Currency,
			CapturedValue: s.rec.Captured, RefundedValue: s.rec.Refunded},
	}
}

// can reports whether an operation makes sense for the current state.
func (s *paymentsScreen) can(op paygate.Operation) bool {
	if s.mode != paysDetail || s.busy != "" {
		return false
	}
	p := s.currentPayment()
	if p.PayID == "" || paygate.OutcomeOf(p) != paygate.OutcomeSuccess {
		return false
	}
	a := p.Amount
	authorized := strings.EqualFold(p.Status, "AUTHORIZED")
	switch op {
	case paygate.OpCapture:
		if a.CapturedKnown {
			return a.CapturedValue < a.Value
		}
		return authorized
	case paygate.OpRefund:
		if a.CapturedKnown {
			return a.CapturedValue > a.RefundedValue
		}
		return !authorized
	case paygate.OpReversal:
		if a.CapturedKnown {
			return a.CapturedValue == 0
		}
		return authorized
	}
	return false
}

func recordFrom(ctx *Context, p *paygate.Payment) history.Record {
	r := history.Record{Profile: ctx.Session.Name, TransID: p.TransID, PayID: p.PayID, Status: p.Status,
		Code: string(p.ResponseCode), Description: p.ResponseDescription, Method: p.Method().Label(), Time: time.Now()}
	if p.Amount != nil {
		r.Amount, r.Currency = p.Amount.Value, p.Amount.Currency
	}
	return r
}

func lookupError(ctx *Context, id string, err error) string {
	if paygate.IsNotFound(err) {
		return fmt.Sprintf(t("pays.notfound"), id, ctx.Session.Host())
	}
	var e *paygate.APIError
	if errors.As(err, &e) && e.Problem != nil {
		return err.Error() + " " + strings.Join(e.Problem.FieldErrors(), "; ")
	}
	return err.Error()
}

// ---- view -------------------------------------------------------------------

func (s *paymentsScreen) View(ctx *Context, w, h int) string {
	s.width = w
	if s.mode == paysDetail {
		return s.detailView(ctx, w, h)
	}
	out := title(t("pays.title")) + "  " + ui.Muted.Render(fmt.Sprintf(t("pays.count"), len(s.records)))
	if s.filter != "" {
		out += "  " + ui.Warning.Render(fmt.Sprintf(t("pays.filtered"), s.filter))
	}
	out += "\n\n"
	if s.form != nil {
		return out + s.form.View()
	}
	if s.busy != "" {
		out += spinView(s.spin) + " " + ui.Label.Render(s.busy) + "\n\n"
	}
	if s.message != "" {
		out += s.messageLine(w) + "\n\n"
	}
	rows := s.visible()
	if len(rows) == 0 {
		text := t("pays.empty")
		if s.filter != "" {
			text = fmt.Sprintf(t("pays.nomatch"), s.filter)
		}
		return out + ui.Label.Render(wrap(text, min(w, 72)))
	}
	out += ui.Muted.Render(fmt.Sprintf("  %-12s %-14s %-14s   %s", t("pays.col.time"), t("pays.col.status"), t("pays.col.amount"), t("pays.col.method"))) + "\n"

	room := h - lipgloss.Height(out) - 1
	if room < 3 {
		room = 3
	}
	s.sel = clamp(s.sel, len(rows))
	if s.sel < s.offset {
		s.offset = s.sel
	}
	if s.sel >= s.offset+room {
		s.offset = s.sel - room + 1
	}
	for i := s.offset; i < len(rows) && i < s.offset+room; i++ {
		out += selectable(ctx, listRow(rows[i], w-4), i == s.sel, w) + "\n"
	}
	return out
}

func listRow(r history.Record, w int) string {
	when := r.Time.Local()
	ts := when.Format("15:04")
	if when.YearDay() != time.Now().YearDay() || when.Year() != time.Now().Year() {
		ts = when.Format("02.01. 15:04")
	}
	sim := " "
	if r.Simulation != "" {
		sim = ui.Warning.Render("◆")
	}
	amount := money.Format(r.Amount, r.Currency)
	method := r.Method
	if method == "" {
		method = core.MethodName(strings.ToUpper(r.Flow))
	}
	rest := truncate(method+"  "+r.TransID, max(w-46, 10))
	return fmt.Sprintf("%-12s ", ts) + padCells(statusBadgeShort(r.Status, r.Code), 14) + " " +
		padCells(ui.Bold.Render(amount), 14) + " " + sim + " " + ui.Label.Render(rest)
}

func statusBadgeShort(status, code string) string {
	p := &paygate.Payment{Status: status, ResponseCode: paygate.Code(code)}
	if status == "" {
		status = "…"
	}
	if len(status) > 10 {
		status = status[:9] + "…"
	}
	switch paygate.OutcomeOf(p) {
	case paygate.OutcomeSuccess:
		return ui.Success.Render("✓ " + status)
	case paygate.OutcomeFailed:
		return ui.Danger.Render("✗ " + status)
	}
	return ui.Warning.Render("… " + status)
}

func padCells(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func (s *paymentsScreen) messageLine(w int) string {
	style := ui.Success
	if s.msgTone == ui.ToneDanger {
		style = ui.Danger
	}
	return style.Render(wrap(s.message, min(w, 76)))
}

func (s *paymentsScreen) detailView(ctx *Context, w, h int) string {
	out := ui.Muted.Render("← "+t("pays.title")+" / ") + ui.Label.Render(s.rec.TransID) + "\n\n"
	if s.form != nil {
		return out + s.form.View()
	}
	p := s.currentPayment()
	opts := ui.PaymentCardOpts{}
	if p.Amount != nil && p.Amount.Currency != "" {
		opts.Amount = money.Format(p.Amount.Value, p.Amount.Currency)
		if p.Amount.CapturedKnown || s.payment == nil {
			opts.Extra = append(opts.Extra,
				ui.Row{Label: t("pays.captured"), Value: money.Format(p.Amount.CapturedValue, p.Amount.Currency)},
				ui.Row{Label: t("pays.refunded"), Value: money.Format(p.Amount.RefundedValue, p.Amount.Currency)})
		}
	}
	if s.rec.Method != "" {
		opts.Extra = append(opts.Extra, ui.Row{Label: t("card.method"), Value: s.rec.Method})
	}
	if s.rec.Simulation != "" {
		opts.Extra = append(opts.Extra, ui.Row{Label: t("pays.sim"), Value: ui.Warning.Render(s.rec.Simulation)})
	}
	if len(s.hooks) > 0 {
		opts.Extra = append(opts.Extra, ui.Row{Label: t("pays.webhooks"), Value: hooksCardValue(s.hooks)})
	}
	out += ui.PaymentCard(p, nil, opts) + "\n"
	if s.busy != "" {
		out += " " + spinView(s.spin) + " " + ui.Label.Render(s.busy) + "\n"
	}
	if s.loadErr != nil {
		out += ui.Warning.Render(" ! ") + ui.Label.Render(t("pays.offline")+" ") + ui.Muted.Render(wrap(s.loadErr.Error(), min(w-4, 70))) + "\n"
	}
	if ctx.Session == nil || s.rec.Profile != ctx.Session.Name {
		out += ui.Muted.Render(" "+fmt.Sprintf(t("pays.other"), s.rec.Profile)) + "\n"
	}
	if s.message != "" {
		out += " " + s.messageLine(w-2) + "\n"
	}
	if s.showJSON && len(s.raw) > 0 {
		lines := strings.Split(ui.HighlightJSON(s.raw), "\n")
		limit := max(h-lipgloss.Height(out)-2, 4)
		if len(lines) > limit {
			lines = append(lines[:limit], ui.Muted.Render("…"))
		}
		return out + "\n" + strings.Join(lines, "\n")
	}
	if rows := s.timeline(); len(rows) > 0 {
		out += "\n" + section(t("pays.timeline")) + "\n"
		for _, r := range rows {
			out += " " + r + "\n"
		}
	}
	return out
}

// timeline merges the payment's API events and its webhooks by time and
// keeps the last eight.
func (s *paymentsScreen) timeline() []string {
	type entry struct {
		at   time.Time
		line string
	}
	var all []entry
	for _, ev := range s.rec.Events {
		all = append(all, entry{ev.Time, timelineRow(ev, s.rec.Currency)})
	}
	for _, e := range s.hooks {
		all = append(all, entry{e.Time, hookTimelineRow(e)})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })
	if len(all) > 8 {
		all = all[len(all)-8:]
	}
	out := make([]string, len(all))
	for i, e := range all {
		out[i] = e.line
	}
	return out
}

func timelineRow(ev history.Event, currency string) string {
	kind := t("ev." + ev.Kind)
	line := ui.Muted.Render(ev.Time.Local().Format("02.01. 15:04:05")) + "  " + fmt.Sprintf("%-11s", kind)
	if ev.Amount != 0 {
		line += " " + money.Format(ev.Amount, currency)
	}
	if ev.Status != "" {
		line += "  " + statusBadgeShort(ev.Status, ev.Code)
	}
	if ev.HTTP != 0 {
		line += ui.Muted.Render(fmt.Sprintf("  %d · %s", ev.HTTP, paygate.FormatDuration(time.Duration(ev.Duration)*time.Millisecond)))
	}
	return line
}
