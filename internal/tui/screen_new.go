package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/pkg/browser"

	"github.com/krebs3r/paygate-cli/internal/cards"
	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/export"
	"github.com/krebs3r/paygate-cli/internal/ids"
	"github.com/krebs3r/paygate-cli/internal/listen"
	"github.com/krebs3r/paygate-cli/internal/money"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

type payStage int

const (
	stageForm payStage = iota
	stageReview
	stageSending
	stageWaiting
	stageResult
	stageConfirmOp
	stageOperating
)

const manualCardChoice = "__manual__"

// Polling timings; variables so tests can speed them up.
var (
	firstPoll    = 500 * time.Millisecond
	pollInterval = 2 * time.Second
	waitTimeout  = 15 * time.Minute
)

// Messages of the new payment screen. seq ties results to the current run,
// so answers to an abandoned payment are ignored.
type (
	payResultMsg struct {
		seq int
		res *paygate.Result
		err error
	}
	pollResultMsg struct {
		seq int
		res *paygate.Result
		err error
	}
	opResultMsg struct {
		seq  int
		kind string
		res  *paygate.Result
		err  error
	}
)

type newPaymentScreen struct {
	stage payStage
	form  *huh.Form
	spin  spinner.Model
	width int
	seq   int

	// form values
	flow, cardChoice, single, cardNumber, expiry, cvc, threeDS string
	methods                                                    []string
	amount, currency, description, simulate, capture           string
	initialized                                                bool

	// prepared request
	req      *paygate.PaymentRequest
	label    string
	prepared *paygate.Prepared
	showJSON bool

	// waiting
	checkout   string
	urlArea    area // where the checkout URL is drawn, relative to the view
	waitStart  time.Time
	waited     time.Duration // until the final status or until waiting stopped
	lastStatus string

	// result
	result    *paygate.Payment
	response  *paygate.Response
	errText   string
	errLines  []string
	note      string
	opKind    string // operation already done on this result
	pendingOp string
	confirmOp bool
	details   bool // show the detail card instead of the receipt
	zoom      bool // QR code full screen while waiting
	paidAt    time.Time
}

func newNewPaymentScreen() *newPaymentScreen {
	return &newPaymentScreen{spin: newSpinner(), width: 70}
}

func (s *newPaymentScreen) ID() string       { return "new" }
func (s *newPaymentScreen) Title() string    { return t("menu.new") }
func (s *newPaymentScreen) Shortcut() string { return "n" }
func (s *newPaymentScreen) Capturing() bool {
	return s.stage == stageForm && s.form != nil || s.stage == stageConfirmOp
}

func (s *newPaymentScreen) Activate(ctx *Context) tea.Cmd {
	if ctx.Session == nil {
		return nil
	}
	if s.stage == stageForm && s.form == nil {
		return s.startForm(ctx)
	}
	return nil
}

// QuitWarning: quitting stops waiting for the payment, which goes on at
// Paygate.
func (s *newPaymentScreen) QuitWarning(*Context) string {
	if s.stage == stageWaiting {
		return t("quit.waiting")
	}
	return ""
}

func (s *newPaymentScreen) Back(ctx *Context) (bool, tea.Cmd) {
	switch s.stage {
	case stageReview:
		return true, s.startForm(ctx)
	case stageWaiting:
		if s.zoom {
			s.zoom = false
			return true, nil
		}
		s.stopWaiting()
		return true, nil
	case stageResult:
		// A successful payment is done: esc leaves to the menu and the
		// receipt stays. After a failure it goes back to the review with a
		// fresh transaction ID, ready to fix and send again.
		if s.succeeded() {
			return false, nil
		}
		return true, s.review(ctx)
	}
	return false, nil
}

func (s *newPaymentScreen) Hints(ctx *Context) []Hint {
	if ctx.Session == nil {
		return []Hint{{"enter", t("home.add")}}
	}
	switch s.stage {
	case stageForm:
		return []Hint{{"enter", t("key.next")}, {"shift+tab", t("key.back")}, {"esc", t("key.cancel")}}
	case stageReview:
		return []Hint{{"enter", t("key.send")}, {"j", t("key.json")}, {"c", t("key.curl")}, {"esc", t("key.edit")}}
	case stageWaiting:
		return []Hint{{"z", t("key.qrzoom")}, {"o", t("key.openbrowser")}, {"c", t("key.copyurl")}, {"esc", t("key.stop")}}
	case stageResult:
		hints := []Hint{{"r", t("key.again")}, {"n", t("key.new")}}
		if s.result != nil {
			if s.details {
				hints = append(hints, Hint{"d", t("key.receipt")})
			} else {
				hints = append(hints, Hint{"d", t("key.details")})
			}
		}
		if s.canRelease() {
			hints = append(hints, Hint{"x", t("key.cancelauth")})
		}
		if s.canRefund() {
			hints = append(hints, Hint{"f", t("key.refund")})
		}
		if s.succeeded() {
			return append(hints, Hint{"esc", t("key.menu")})
		}
		return append(hints, Hint{"esc", t("key.back")})
	case stageConfirmOp:
		return []Hint{{"← →", t("key.select")}, {"enter", t("key.open")}, {"esc", t("key.cancel")}}
	}
	return nil
}

// ---- form -------------------------------------------------------------------

func (s *newPaymentScreen) defaults(ctx *Context) {
	if s.initialized {
		return
	}
	p := ctx.Session.Profile
	s.flow, s.cardChoice, s.single, s.threeDS = string(core.FlowDirect), "visa-ok", "paypal", "skip"
	s.currency, s.description = p.DefaultCurrency(), core.DefaultDescription
	s.amount, s.simulate, s.capture = core.DefaultAmount, "off", "auto"
	if p.IsProd() {
		s.amount, s.simulate, s.capture = core.ProdDefaultAmount, "ok", "manual"
	}
	s.initialized = true
}

func (s *newPaymentScreen) startForm(ctx *Context) tea.Cmd {
	s.defaults(ctx)
	if ctx.Width > 0 {
		s.width = ctx.Width
	}
	s.stage, s.errText, s.errLines, s.note = stageForm, "", nil, ""

	// Only cards without 3-D Secure: a direct payment cannot run the 3DS
	// browser steps in a terminal. 3DS cards work on the payment page.
	var cardOpts []huh.Option[string]
	for _, c := range cards.Cards {
		if c.ThreeDS {
			continue
		}
		label := fmt.Sprintf("%-10s  %-19s  %s", brandName(c.Brand), formatPAN(c.Number), t(c.Expect))
		cardOpts = append(cardOpts, huh.NewOption(label, c.Alias))
	}
	cardOpts = append(cardOpts, huh.NewOption(t("new.card.manual"), manualCardChoice))

	var multi, single []huh.Option[string]
	for _, a := range []string{"card", "paypal", "ideal", "sepa", "klarna", "applepay", "googlepay", "bancontact", "eps", "twint"} {
		pm, _ := core.ResolveMethod(a)
		multi = append(multi, huh.NewOption(pm.Name, pm.Alias))
	}
	for _, a := range core.HostedSingleMethods {
		pm, _ := core.ResolveMethod(a)
		single = append(single, huh.NewOption(pm.Name, pm.Alias))
	}
	currencies := huh.NewOptions(money.Common...)
	if !containsFold(money.Common, s.currency) {
		currencies = append([]huh.Option[string]{huh.NewOption(s.currency, s.currency)}, currencies...)
	}

	s.form = newForm(s.width,
		huh.NewGroup(huh.NewSelect[string]().Title(t("new.flow")).Options(
			huh.NewOption(t("flow.direct"), string(core.FlowDirect)),
			huh.NewOption(t("flow.hosted"), string(core.FlowHosted)),
			huh.NewOption(t("flow.method"), string(core.FlowMethod)),
		).Value(&s.flow), ui.LiveNote(func() string { return s.endpoint(ctx) })),
		huh.NewGroup(huh.NewMultiSelect[string]().Title(t("new.methods")).Description(t("new.methods.desc")).
			Options(multi...).Value(&s.methods)).
			WithHideFunc(func() bool { return s.flow != string(core.FlowHosted) }),
		huh.NewGroup(huh.NewSelect[string]().Title(t("new.method")).Options(single...).Value(&s.single)).
			WithHideFunc(func() bool { return s.flow != string(core.FlowMethod) }),
		huh.NewGroup(huh.NewSelect[string]().Title(t("new.card")).Description(t("new.card.desc")).
			Options(cardOpts...).Value(&s.cardChoice)).
			WithHideFunc(func() bool { return s.flow != string(core.FlowDirect) }),
		huh.NewGroup(
			huh.NewInput().Title(t("new.card.number")).Value(&s.cardNumber).Validate(func(v string) error {
				if len(strings.ReplaceAll(v, " ", "")) < 12 {
					return errors.New(t("new.card.short"))
				}
				return nil
			}),
			huh.NewInput().Title(t("new.card.expiry")).Placeholder(cards.DefaultExpiry()).Value(&s.expiry),
			huh.NewInput().Title("CVC").Placeholder("123").Value(&s.cvc),
			huh.NewSelect[string]().Title("3-D Secure").Options(
				huh.NewOption(t("3ds.skip"), "skip"),
				huh.NewOption(t("3ds.request"), "request"),
			).Inline(true).Value(&s.threeDS),
		).WithHideFunc(func() bool { return s.flow != string(core.FlowDirect) || s.cardChoice != manualCardChoice }),
		huh.NewGroup(
			huh.NewInput().Title(t("new.amount")).Value(&s.amount).Validate(func(v string) error {
				_, err := money.Parse(v, s.currency)
				return err
			}),
			huh.NewSelect[string]().Title(t("new.currency")).Options(currencies...).Inline(true).Value(&s.currency),
			huh.NewInput().Title(t("new.desc")).Value(&s.description),
			huh.NewSelect[string]().Title(t("new.sim")).Description(t("new.sim.desc")).Options(
				huh.NewOption(t("sim.off"), "off"),
				huh.NewOption(t("sim.ok"), "ok"),
				huh.NewOption(t("sim.decline"), "decline"),
			).Value(&s.simulate),
			huh.NewSelect[string]().Title(t("new.capture")).Options(
				huh.NewOption(t("cap.auto"), "auto"),
				huh.NewOption(t("cap.manual"), "manual"),
			).Inline(true).Value(&s.capture),
		),
	)
	return s.form.Init()
}

func (s *newPaymentScreen) input() core.PaymentInput {
	in := core.PaymentInput{
		Flow: core.Flow(s.flow), Amount: s.amount, Currency: s.currency, Description: s.description,
		Capture: s.capture,
	}
	if s.simulate != "off" {
		in.Simulate = s.simulate
	}
	switch in.Flow {
	case core.FlowHosted:
		in.Methods = s.methods
	case core.FlowMethod:
		in.Method = s.single
	case core.FlowDirect:
		if s.cardChoice == manualCardChoice {
			in.CardNumber, in.Expiry, in.CVC = s.cardNumber, s.expiry, s.cvc
			in.Skip3DS = s.threeDS == "skip"
		} else {
			in.Card = s.cardChoice
		}
	}
	return in
}

// review builds the request from the form values and shows the summary.
func (s *newPaymentScreen) review(ctx *Context) tea.Cmd {
	in := s.input()
	in.TransID = ids.NewTransID()
	// Webhooks go to a paygate listen running in another terminal.
	if st, ok := listen.For(ctx.Session.Name); ok && in.Flow != core.FlowDirect {
		in.WebhookURL = st.URL
	}
	req, label, err := core.BuildPaymentRequest(in, ctx.Session.Profile)
	if err != nil {
		return s.formWithError(ctx, err)
	}
	prepared, err := core.Prepare(ctx.Session.Client, in.Flow, req)
	if err != nil {
		return s.formWithError(ctx, err)
	}
	s.req, s.label, s.prepared = req, label, prepared
	s.stage, s.form, s.showJSON = stageReview, nil, false
	return nil
}

// formWithError reopens the form with the error above it.
func (s *newPaymentScreen) formWithError(ctx *Context, err error) tea.Cmd {
	cmd := s.startForm(ctx)
	s.errText = err.Error()
	return cmd
}

// ---- update -----------------------------------------------------------------

func (s *newPaymentScreen) Update(ctx *Context, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		s.spin, cmd = s.spin.Update(msg)
		return cmd
	case sessionChangedMsg:
		s.initialized, s.stage, s.form = false, stageForm, nil
		return nil
	case payResultMsg:
		if msg.seq == s.seq {
			return s.onPaid(ctx, msg)
		}
		return nil
	case pollResultMsg:
		if msg.seq == s.seq && s.stage == stageWaiting {
			return s.onPoll(ctx, msg)
		}
		return nil
	case opResultMsg:
		if msg.seq == s.seq {
			return s.onOperation(ctx, msg)
		}
		return nil
	}

	if ctx.Session == nil {
		if km, ok := msg.(tea.KeyMsg); ok && km.String() == "enter" {
			return switchTo("profiles", "add")
		}
		return nil
	}

	switch s.stage {
	case stageForm:
		if s.form == nil {
			return s.startForm(ctx)
		}
		f, cmd := s.form.Update(msg)
		s.form = f.(*huh.Form)
		switch s.form.State {
		case huh.StateAborted:
			s.form = nil
			return backToMenu
		case huh.StateCompleted:
			return s.review(ctx)
		}
		return cmd
	case stageConfirmOp:
		f, cmd := s.form.Update(msg)
		s.form = f.(*huh.Form)
		switch s.form.State {
		case huh.StateAborted:
			s.stage, s.form = stageResult, nil
		case huh.StateCompleted:
			s.stage, s.form = stageResult, nil
			if s.confirmOp {
				return s.runOperation(ctx)
			}
		}
		return cmd
	}

	// A click on the checkout URL opens it, like a link.
	if mm, ok := msg.(tea.MouseMsg); ok {
		if s.stage == stageWaiting && !s.zoom && mm.Action == tea.MouseActionPress && mm.Button == tea.MouseButtonLeft &&
			s.urlArea.contains(mm.X-ctx.ContentX, mm.Y-ctx.ContentY) {
			return s.openBrowser()
		}
		return nil
	}
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch s.stage {
	case stageReview:
		switch km.String() {
		case "enter":
			return s.send(ctx)
		case "e", "shift+tab":
			return s.startForm(ctx)
		case "j":
			s.showJSON = !s.showJSON
		case "c":
			if err := clipboard.WriteAll(export.Curl(s.prepared, ctx.Session.Client, false)); err != nil {
				return toast(err.Error(), ui.ToneDanger)
			}
			return toast(t("copied.curl"), ui.ToneSuccess)
		}
	case stageWaiting:
		switch km.String() {
		case "z":
			s.zoom = !s.zoom
		case "o":
			return s.openBrowser()
		case "c":
			if err := clipboard.WriteAll(s.checkout); err != nil {
				return toast(err.Error(), ui.ToneDanger)
			}
			return toast(t("copied.url"), ui.ToneSuccess)
		}
	case stageResult:
		switch km.String() {
		case "r":
			if s.req != nil {
				if cmd := s.review(ctx); cmd != nil {
					return cmd
				}
				return s.send(ctx)
			}
		case "n", "e":
			return s.startForm(ctx)
		case "shift+tab":
			return s.review(ctx)
		case "d":
			s.details = !s.details
		case "x":
			if s.canRelease() {
				return s.confirmOperation("cancel", t("res.cancel.q"))
			}
		case "f":
			if s.canRefund() {
				return s.confirmOperation("refund", t("res.refund.q"))
			}
		}
	}
	return nil
}

// backToMenu moves the keyboard focus to the sidebar.
func backToMenu() tea.Msg { return focusMenuMsg{} }

// ---- sending and waiting ---------------------------------------------------------

func (s *newPaymentScreen) send(ctx *Context) tea.Cmd {
	s.seq++
	seq, client, prepared := s.seq, ctx.Session.Client, s.prepared
	s.stage, s.result, s.response, s.errText, s.errLines, s.note = stageSending, nil, nil, "", nil, ""
	s.opKind, s.waitStart, s.waited, s.details, s.zoom = "", time.Time{}, 0, false, false
	return tea.Batch(s.spin.Tick, func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		res, err := client.Exec(c, prepared)
		return payResultMsg{seq: seq, res: res, err: err}
	})
}

func (s *newPaymentScreen) onPaid(ctx *Context, msg payResultMsg) tea.Cmd {
	s.paidAt = time.Now()
	core.RecordPayment(ctx.Session.Name, ctx.Session.Profile, core.Flow(s.flow), s.req, s.label, msg.res, msg.err)
	if msg.err != nil {
		s.showError(msg.err)
		return nil
	}
	s.result, s.response = msg.res.Payment, msg.res.Response
	if s.result.TransID == "" {
		s.result.TransID = s.req.TransID
	}
	if url := s.result.RedirectURL(); url != "" {
		s.checkout, s.waitStart, s.lastStatus = url, time.Now(), ""
		s.stage = stageWaiting
		return tea.Batch(s.spin.Tick, s.poll(ctx, firstPoll))
	}
	s.stage = stageResult
	if paygate.ClassifyCode(s.result.ResponseCode) == paygate.Code3DS {
		s.note = t("res.3ds")
	}
	return nil
}

func (s *newPaymentScreen) poll(ctx *Context, delay time.Duration) tea.Cmd {
	seq, client, transID := s.seq, ctx.Session.Client, s.req.TransID
	return tea.Tick(delay, func(time.Time) tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		res, err := client.GetByTransID(c, transID)
		return pollResultMsg{seq: seq, res: res, err: err}
	})
}

func (s *newPaymentScreen) onPoll(ctx *Context, msg pollResultMsg) tea.Cmd {
	if msg.err == nil {
		s.lastStatus = msg.res.Payment.Status
		if paygate.OutcomeOf(msg.res.Payment) != paygate.OutcomePending {
			core.RecordStatus(ctx.Session.Name, msg.res.Payment)
			s.result, s.response, s.stage = msg.res.Payment, nil, stageResult
			s.waited = time.Since(s.waitStart)
			return nil
		}
	} else if errors.Is(msg.err, paygate.ErrUnauthorized) {
		s.waited = time.Since(s.waitStart)
		s.showError(msg.err)
		return nil
	}
	if time.Since(s.waitStart) > waitTimeout {
		s.stopWaiting()
		return nil
	}
	return s.poll(ctx, pollInterval)
}

func (s *newPaymentScreen) stopWaiting() {
	s.seq++ // ignore polls still in flight
	s.stage, s.note = stageResult, t("res.stopped")
	s.waited = time.Since(s.waitStart)
}

// openURL opens a link in the default browser; a variable for the tests.
var openURL = func(url string) error {
	browser.Stdout, browser.Stderr = io.Discard, io.Discard
	return browser.OpenURL(url)
}

// endpoint is the method and URL the payment goes to in the chosen flow.
func (s *newPaymentScreen) endpoint(ctx *Context) string {
	if ctx.Session == nil {
		return ""
	}
	p, err := core.Prepare(ctx.Session.Client, core.Flow(s.flow), &paygate.PaymentRequest{})
	if err != nil {
		return ""
	}
	return p.Method + " " + p.URL
}

func (s *newPaymentScreen) openBrowser() tea.Cmd { return openLink(s.checkout) }

// openLink opens a URL in the browser and says so.
func openLink(url string) tea.Cmd {
	if err := openURL(url); err != nil {
		return toast(err.Error(), ui.ToneDanger)
	}
	return toast(t("opened"), ui.ToneSuccess)
}

func (s *newPaymentScreen) showError(err error) {
	s.stage = stageResult
	if e, ok := paygate.AsAPIError(err); ok && e.Payment != nil && (e.Payment.Status != "" || e.Payment.ResponseCode != "") {
		if e.Payment.Status == "" {
			e.Payment.Status = "FAILED"
		}
		if e.Payment.TransID == "" && s.req != nil {
			e.Payment.TransID = s.req.TransID
		}
		s.result, s.response = e.Payment, e.Response
		return
	}
	s.result, s.response = nil, nil
	s.errText, s.errLines = err.Error(), nil
	if e, ok := paygate.AsAPIError(err); ok && e.Problem != nil {
		s.errLines = e.Problem.FieldErrors()
	}
}

// ---- follow-up operations ------------------------------------------------------

func (s *newPaymentScreen) succeeded() bool {
	return s.result != nil && s.result.PayID != "" && paygate.OutcomeOf(s.result) == paygate.OutcomeSuccess
}

func (s *newPaymentScreen) canRelease() bool {
	return s.succeeded() && s.capture == "manual" && s.opKind != "cancel"
}

func (s *newPaymentScreen) canRefund() bool {
	return s.succeeded() && s.capture == "auto" && s.opKind != "refund"
}

func (s *newPaymentScreen) confirmOperation(kind, question string) tea.Cmd {
	s.pendingOp, s.confirmOp, s.stage = kind, true, stageConfirmOp
	amount := money.Format(s.req.Amount.Value, s.req.Amount.Currency)
	s.form = newForm(s.width, huh.NewGroup(huh.NewConfirm().
		Title(fmt.Sprintf(question, amount)).
		Affirmative(t("yes")).Negative(t("no")).
		Value(&s.confirmOp)))
	return s.form.Init()
}

func (s *newPaymentScreen) runOperation(ctx *Context) tea.Cmd {
	kind := s.pendingOp
	op := paygate.OpReversal
	if kind == "refund" {
		op = paygate.OpRefund
	}
	seq, client, payID := s.seq, ctx.Session.Client, s.result.PayID
	body := &paygate.OperationRequest{TransID: s.req.TransID, Amount: s.req.Amount}
	s.stage = stageOperating
	return tea.Batch(s.spin.Tick, func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		res, err := client.Operate(c, op, payID, body, ids.NewIdempotencyKey())
		return opResultMsg{seq: seq, kind: kind, res: res, err: err}
	})
}

func (s *newPaymentScreen) onOperation(ctx *Context, msg opResultMsg) tea.Cmd {
	core.RecordOperation(ctx.Session.Name, s.req.TransID, msg.kind, s.req.Amount.Value, msg.res, msg.err)
	s.stage = stageResult
	name := t("res.cancelled")
	if msg.kind == "refund" {
		name = t("res.refunded")
	}
	if msg.err != nil || paygate.OutcomeOf(msg.res.Payment) == paygate.OutcomeFailed {
		reason := ""
		if msg.err != nil {
			reason = msg.err.Error()
		} else {
			reason = msg.res.Payment.ResponseDescription
		}
		return toast(fmt.Sprintf(t("res.op.failed"), name, reason), ui.ToneDanger)
	}
	s.opKind = msg.kind
	s.note = name
	return toast(name, ui.ToneSuccess)
}

// ---- view -------------------------------------------------------------------

func (s *newPaymentScreen) View(ctx *Context, w, h int) string {
	s.width = w
	if ctx.Session == nil {
		return title(t("new.title")) + "\n\n" + ui.Label.Render(t("new.noprofile"))
	}
	out := s.header(ctx) + strings.Repeat("\n", viewTop)
	switch s.stage {
	case stageForm:
		if s.errText != "" {
			out += hang(ui.Danger.Render("! "), ui.Label.Render(wrap(s.errText, min(w-2, 74)))) + "\n\n"
		}
		if s.form == nil {
			return out
		}
		return out + s.form.View()
	case stageReview:
		return out + s.reviewView(ctx, w, h-2)
	case stageSending:
		return out + spinView(s.spin) + " " + ui.Label.Render(fmt.Sprintf(t("new.sending"), ctx.Session.Host()))
	case stageWaiting:
		return out + s.waitView(ctx, w, h-2)
	case stageOperating:
		return out + spinView(s.spin) + " " + ui.Label.Render("…")
	case stageConfirmOp:
		return out + s.resultView(ctx, w) + "\n\n" + s.form.View()
	}
	return out + s.resultView(ctx, w)
}

func (s *newPaymentScreen) header(ctx *Context) string {
	steps := []struct {
		key   string
		stage payStage
	}{{"new.step.form", stageForm}, {"new.step.review", stageReview}, {"new.step.result", stageResult}}
	cur := s.stage
	if cur == stageSending || cur == stageWaiting || cur == stageOperating || cur == stageConfirmOp {
		cur = stageResult
	}
	var parts []string
	for _, st := range steps {
		switch {
		case st.stage < cur:
			parts = append(parts, ui.Success.Render("✓ "+t(st.key)))
		case st.stage == cur:
			parts = append(parts, ui.Link.Render("● "+t(st.key)))
		default:
			parts = append(parts, ui.Muted.Render("○ "+t(st.key)))
		}
	}
	return title(t("new.title")) + "   " + strings.Join(parts, ui.Muted.Render(" ─ "))
}

func (s *newPaymentScreen) reviewView(ctx *Context, w, h int) string {
	p := ctx.Session.Profile
	req := s.req
	rows := [][2]string{{t("rev.flow"), flowLabel(s.flow)}}
	switch core.Flow(s.flow) {
	case core.FlowHosted:
		methods := t("rev.methods.all")
		if len(req.AllowedPaymentMethods) > 0 {
			var names []string
			for _, m := range req.AllowedPaymentMethods {
				names = append(names, core.MethodName(m))
			}
			methods = strings.Join(names, ", ")
		}
		rows = append(rows, [2]string{t("rev.methods"), methods})
	default:
		rows = append(rows, [2]string{t("rev.method"), s.label})
	}
	if pm := req.PaymentMethods; pm != nil && pm.Card != nil {
		threeDS := ui.Warning.Render(t("rev.3ds.on"))
		if pm.Card.ThreeDsPolicy != nil {
			threeDS = t("rev.3ds.skip")
		}
		rows = append(rows, [2]string{"3-D Secure", threeDS})
	}
	sim := ui.Muted.Render(t("rev.sim.off"))
	if s.simulate != "off" {
		sim = ui.Warning.Render(req.Order.Description)
	}
	capture := t("cap.auto")
	if s.capture == "manual" {
		capture = t("cap.manual")
	}
	rows = append(rows,
		[2]string{t("rev.amount"), ui.Bold.Render(money.Format(req.Amount.Value, req.Amount.Currency))},
		[2]string{t("rev.capture"), capture},
		[2]string{t("rev.sim"), sim},
		[2]string{t("rev.desc"), req.Order.Description},
		[2]string{t("rev.transid"), ui.Muted.Render(req.TransID)},
		[2]string{t("rev.host"), ctx.Session.Host() + "  " + ui.EnvBadge(p.IsProd())},
	)
	border := ui.ColBorder
	out := box(kv(rows), min(w, 72), border) + "\n"
	if p.IsProd() {
		if s.simulate != "off" {
			out += ui.Warning.Render("! ") + ui.Label.Render(fmt.Sprintf(t("rev.prod.sim"), req.Order.Description)) + "\n"
		} else {
			out += ui.Danger.Render("! "+t("rev.prod.real")) + "\n"
			if s.capture == "manual" {
				out += ui.Label.Render("  "+t("rev.prod.manual")) + "\n"
			}
		}
	}
	if s.showJSON {
		lines := strings.Split(ui.HighlightJSON(maskCard(s.prepared.Body)), "\n")
		limit := h - lipgloss.Height(out) - 3
		if limit < 4 {
			limit = 4
		}
		if len(lines) > limit {
			lines = append(lines[:limit], ui.Muted.Render(t("rev.json.more")))
		}
		out += "\n" + ui.Muted.Render(s.prepared.Method+" "+s.prepared.URL) + "\n" + strings.Join(lines, "\n")
	}
	return out
}

// waitText is the status column shown next to (or instead of) the QR code.
// cardHint lists test cards for the payment page. Only on the test system:
// production declines test cards.
func (s *newPaymentScreen) cardHint(ctx *Context) string {
	offersCard := core.Flow(s.flow) == core.FlowHosted && (len(s.methods) == 0 || containsFold(s.methods, "card")) ||
		core.Flow(s.flow) == core.FlowMethod && s.single == "card"
	if !offersCard || ctx.IsProd() {
		return ""
	}
	visa, _ := cards.FindCard("visa-ok")
	challenge, _ := cards.FindCard("visa-3ds-challenge")
	return ui.Label.Render(t("wait.cards")) + "\n" +
		ui.Text.Render(formatPAN(visa.Number)) + "\n" +
		ui.Text.Render(formatPAN(challenge.Number)) + ui.Muted.Render("  3DS · OTP 1234")
}

func (s *newPaymentScreen) waitText(ctx *Context) string {
	heading, hint := t("wait.title"), t("wait.scan")
	if core.Flow(s.flow) == core.FlowDirect {
		heading, hint = t("wait.3ds.title"), t("wait.3ds")
	}
	status := s.lastStatus
	if status == "" {
		status = t("wait.none")
	}
	el := time.Since(s.waitStart).Round(time.Second)
	out := ui.Bold.Render(heading) + "\n" +
		ui.Label.Render(money.Format(s.req.Amount.Value, s.req.Amount.Currency)+" · "+s.req.TransID) + "\n\n" +
		ui.Muted.Render(hint) + "\n\n" +
		spinView(s.spin) + " " + ui.Label.Render(fmt.Sprintf(t("wait.status"), status, fmt.Sprintf("%d:%02d", int(el.Minutes()), int(el.Seconds())%60)))
	if c := s.cardHint(ctx); c != "" {
		out += "\n\n" + c
	}
	return out
}

func (s *newPaymentScreen) waitView(ctx *Context, w, h int) string {
	qr := ui.QR(s.checkout)
	qrW, qrH := lipgloss.Width(qr), lipgloss.Height(qr)
	textW := w - qrW - 3
	// The URL sits in the column next to the code, so the code only needs
	// its own height. The column is wrapped to its own width: a line wider
	// than the content area would be wrapped by the frame and push text
	// between the rows of the code, which breaks it.
	if qrH <= h && textW >= 24 {
		url := ui.Link.Render(hardWrap(s.checkout, textW))
		text := lipgloss.NewStyle().Width(textW).Render(s.waitText(ctx) + "\n\n" + s.urlLabel() + "\n" + url)
		block := lipgloss.JoinHorizontal(lipgloss.Top, qr, "   ", text)
		if lipgloss.Width(block) <= w && lipgloss.Height(block) <= h {
			s.urlArea = area{x: qrW + 3, y: viewTop + lipgloss.Height(text) - lipgloss.Height(url), w: textW, h: lipgloss.Height(url)}
			return block
		}
	}
	url := ui.Link.Render(hardWrap(s.checkout, w))
	out := s.waitText(ctx) + "\n\n" + hang(ui.Warning.Render("! "), ui.Label.Render(wrap(t("wait.small"), min(w-2, 76)))) +
		"\n\n" + s.urlLabel() + "\n" + url
	s.urlArea = area{x: 0, y: viewTop + lipgloss.Height(out) - lipgloss.Height(url), w: w, h: lipgloss.Height(url)}
	return out
}

// urlLabel heads the checkout URL and says how to use it: the terminal
// cannot select or open it while the app owns the mouse.
func (s *newPaymentScreen) urlLabel() string {
	return ui.Label.Render("URL") + "  " + ui.Muted.Render(t("wait.urlkeys"))
}

// viewTop is the number of lines above the stage content: the header with
// the steps and a blank line.
const viewTop = 2

// area is a rectangle of cells.
type area struct{ x, y, w, h int }

func (a area) contains(x, y int) bool {
	return x >= a.x && x < a.x+a.w && y >= a.y && y < a.y+a.h
}

// FullScreen shows the QR code alone on the whole terminal (key z).
func (s *newPaymentScreen) FullScreen(ctx *Context, w, h int) (string, bool) {
	if !s.zoom || s.stage != stageWaiting {
		return "", false
	}
	qr := ui.QR(s.checkout)
	qrH := lipgloss.Height(qr)
	keys := ui.Muted.Render("esc " + t("key.back") + "  ·  o " + t("key.openbrowser") + "  ·  c " + t("key.copyurl"))
	var block string
	switch {
	case h >= qrH+4:
		el := time.Since(s.waitStart).Round(time.Second)
		info := ui.Label.Render(money.Format(s.req.Amount.Value, s.req.Amount.Currency)+" · "+s.req.TransID) + "  " +
			spinView(s.spin) + " " + ui.Muted.Render(fmt.Sprintf("%d:%02d", int(el.Minutes()), int(el.Seconds())%60))
		block = qr + "\n\n" + info + "\n" + keys
	case h >= qrH:
		block = qr
	default:
		block = ui.Warning.Render(fmt.Sprintf(t("wait.zoom.small"), qrH)) + "\n\n" + keys
	}
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, lipgloss.NewStyle().Align(lipgloss.Center).Render(block)), true
}

// hardWrap breaks a long token-like string (URL) into lines of width w.
func hardWrap(s string, w int) string {
	if w < 10 {
		w = 10
	}
	r := []rune(s)
	var parts []string
	for len(r) > w {
		parts = append(parts, string(r[:w]))
		r = r[w:]
	}
	return strings.Join(append(parts, string(r)), "\n")
}

func (s *newPaymentScreen) resultView(ctx *Context, w int) string {
	if s.result == nil {
		out := ui.ErrorBox(s.errText, s.errLines, "")
		if s.note != "" {
			out += "\n\n" + hang(ui.Warning.Render("! "), ui.Label.Render(wrap(s.note, min(w-2, 74))))
		}
		return out
	}
	p := s.result
	opts := ui.PaymentCardOpts{Amount: money.Format(s.req.Amount.Value, s.req.Amount.Currency)}
	if m := p.Method(); m.Type != "" && m.Type != "CARD" && paygate.OutcomeOf(p) == paygate.OutcomeSuccess {
		opts.Title = fmt.Sprintf(t("res.paidwith"), core.MethodName(m.Type))
	}
	if s.label != "" {
		opts.Extra = append(opts.Extra, ui.Row{Label: t("card.method"), Value: s.label})
	}
	if s.waited > 0 && s.response == nil {
		where := t("res.waited.page")
		if core.Flow(s.flow) == core.FlowDirect {
			where = t("res.waited.3ds")
		}
		opts.Extra = append(opts.Extra, ui.Row{Label: t("res.waited"), Value: ui.Muted.Render(fmt.Sprintf(where, s.waited.Round(time.Second)))})
	}
	var out string
	if s.details {
		out = ui.PaymentCard(p, s.response, opts)
	} else {
		sim := ""
		if s.simulate != "off" && s.req.Order != nil {
			sim = s.req.Order.Description
		}
		out = indentLines(ui.Receipt(p, ui.ReceiptOpts{
			Amount: opts.Amount, Method: s.label, Simulation: sim, Prod: ctx.IsProd(), Time: s.paidAt,
		}), " ")
	}
	if s.details && s.simulate != "off" && s.req.Order != nil {
		out += "\n" + ui.Warning.Render(" ◆ ") + ui.Label.Render(fmt.Sprintf(t("res.sim"), s.req.Order.Description))
	}
	if s.note != "" {
		out += "\n" + hang(ui.Accent.Render(" › "), ui.Label.Render(wrap(s.note, min(w-4, 74))))
	}
	if ctx.IsProd() && s.simulate == "off" && s.canRelease() {
		out += "\n" + hang(ui.Danger.Render(" ! "), ui.Label.Render(wrap(t("res.prod.hint"), min(w-4, 74))))
	}
	return out
}

// ---- helpers ------------------------------------------------------------------

var (
	cardNumberRe = regexp.MustCompile(`("number"\s*:\s*")\d{8,15}(\d{4})"`)
	cvcRe        = regexp.MustCompile(`("securityCode"\s*:\s*")[^"]*"`)
)

// maskCard hides card number and CVC in a request body shown on screen.
func maskCard(body []byte) []byte {
	b := cardNumberRe.ReplaceAll(body, []byte(`${1}•••• ${2}"`))
	return cvcRe.ReplaceAll(b, []byte(`${1}•••"`))
}

// hang puts a marker before a wrapped text, so the following lines stay
// indented below the text.
func hang(marker, text string) string {
	return lipgloss.JoinHorizontal(lipgloss.Top, marker, text)
}

func indentLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

func flowLabel(f string) string {
	switch core.Flow(f) {
	case core.FlowHosted:
		return t("flow.hosted.short")
	case core.FlowMethod:
		return t("flow.method.short")
	}
	return t("flow.direct.short")
}

func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}
