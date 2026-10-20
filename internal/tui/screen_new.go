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
	flow, cardChoice, single, cardNumber, expiry, cvc string
	methods                                           []string
	amount, currency, description, simulate, capture  string
	initialized                                       bool

	// prepared request
	req      *paygate.PaymentRequest
	label    string
	prepared *paygate.Prepared
	showJSON bool

	// waiting
	checkout   string
	waitStart  time.Time
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

func (s *newPaymentScreen) Back(ctx *Context) bool {
	switch s.stage {
	case stageReview:
		s.stage = stageForm
		s.form = nil
		return true
	case stageWaiting:
		s.stopWaiting()
		return true
	}
	return false
}

func (s *newPaymentScreen) Hints(ctx *Context) []Hint {
	if ctx.Session == nil {
		return []Hint{{"enter", t("home.add")}}
	}
	switch s.stage {
	case stageForm:
		return []Hint{{"enter", t("key.next")}, {"shift+tab", t("key.back")}, {"esc", t("key.cancel")}}
	case stageReview:
		return []Hint{{"enter", t("key.send")}, {"e", t("key.edit")}, {"j", t("key.json")}, {"c", t("key.curl")}, {"esc", t("key.edit")}}
	case stageWaiting:
		return []Hint{{"o", t("key.openbrowser")}, {"c", t("key.copyurl")}, {"esc", t("key.stop")}}
	case stageResult:
		hints := []Hint{{"r", t("key.again")}, {"n", t("key.new")}}
		if s.canRelease() {
			hints = append(hints, Hint{"x", t("key.cancelauth")})
		}
		if s.canRefund() {
			hints = append(hints, Hint{"f", t("key.refund")})
		}
		return hints
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
	s.flow, s.cardChoice, s.single = string(core.FlowDirect), "visa-ok", "paypal"
	s.currency, s.description = p.DefaultCurrency(), core.DefaultDescription
	s.amount, s.simulate, s.capture = core.DefaultAmount, "off", "auto"
	if p.IsProd() {
		s.amount, s.simulate, s.capture = core.ProdDefaultAmount, "ok", "manual"
	}
	s.initialized = true
}

func (s *newPaymentScreen) startForm(ctx *Context) tea.Cmd {
	s.defaults(ctx)
	s.stage, s.errText, s.errLines, s.note = stageForm, "", nil, ""

	var cardOpts []huh.Option[string]
	scenarioW := 0
	for _, c := range cards.Cards {
		scenarioW = max(scenarioW, lipgloss.Width(t(c.Scenario)+" · "+t(c.Expect)))
	}
	for _, c := range cards.Cards {
		desc := t(c.Scenario) + " · " + t(c.Expect)
		label := fmt.Sprintf("%-11s %s%s  %s", brandName(c.Brand), desc, strings.Repeat(" ", scenarioW-lipgloss.Width(desc)), formatPAN(c.Number))
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
		).Value(&s.flow)),
		huh.NewGroup(huh.NewMultiSelect[string]().Title(t("new.methods")).Description(t("new.methods.desc")).
			Options(multi...).Value(&s.methods)).
			WithHideFunc(func() bool { return s.flow != string(core.FlowHosted) }),
		huh.NewGroup(huh.NewSelect[string]().Title(t("new.method")).Options(single...).Value(&s.single)).
			WithHideFunc(func() bool { return s.flow != string(core.FlowMethod) }),
		huh.NewGroup(huh.NewSelect[string]().Title(t("new.card")).Description(t("new.card.desc")).
			Options(cardOpts...).Height(10).Value(&s.cardChoice)).
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
	req, label, err := core.BuildPaymentRequest(in, ctx.Session.Profile)
	if err != nil {
		s.errText = err.Error()
		return s.startForm(ctx)
	}
	prepared, err := core.Prepare(ctx.Session.Client, in.Flow, req)
	if err != nil {
		s.errText = err.Error()
		return s.startForm(ctx)
	}
	s.req, s.label, s.prepared = req, label, prepared
	s.stage, s.form, s.showJSON = stageReview, nil, false
	return nil
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

	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch s.stage {
	case stageReview:
		switch km.String() {
		case "enter":
			return s.send(ctx)
		case "e":
			s.stage, s.form = stageForm, nil
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
	s.opKind, s.waitStart = "", time.Time{}
	return tea.Batch(s.spin.Tick, func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		res, err := client.Exec(c, prepared)
		return payResultMsg{seq: seq, res: res, err: err}
	})
}

func (s *newPaymentScreen) onPaid(ctx *Context, msg payResultMsg) tea.Cmd {
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
			return nil
		}
	} else if errors.Is(msg.err, paygate.ErrUnauthorized) {
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
}

func (s *newPaymentScreen) openBrowser() tea.Cmd {
	browser.Stdout, browser.Stderr = io.Discard, io.Discard
	if err := browser.OpenURL(s.checkout); err != nil {
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
	out := s.header(ctx) + "\n\n"
	switch s.stage {
	case stageForm:
		if s.errText != "" {
			out += ui.Danger.Render("! ") + ui.Label.Render(wrap(s.errText, min(w-2, 74))) + "\n\n"
		}
		if s.form == nil {
			return out
		}
		return out + s.form.View()
	case stageReview:
		return out + s.reviewView(ctx, w, h-2)
	case stageSending:
		return out + s.spin.View() + " " + ui.Label.Render(fmt.Sprintf(t("new.sending"), ctx.Session.Host()))
	case stageWaiting:
		return out + s.waitView(w, h-2)
	case stageOperating:
		return out + s.spin.View() + " " + ui.Label.Render("…")
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
	if p.IsProd() {
		border = ui.ColDanger
	}
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

func (s *newPaymentScreen) waitView(w, h int) string {
	heading, hint := t("wait.title"), t("wait.scan")
	if core.Flow(s.flow) == core.FlowDirect {
		heading, hint = t("wait.3ds.title"), t("wait.3ds")
	}
	status := s.lastStatus
	if status == "" {
		status = t("wait.none")
	}
	el := time.Since(s.waitStart).Round(time.Second)
	text := ui.Bold.Render(heading) + "\n" +
		ui.Label.Render(money.Format(s.req.Amount.Value, s.req.Amount.Currency)+" · "+s.req.TransID) + "\n\n" +
		ui.Muted.Render(hint) + "\n\n" +
		s.spin.View() + " " + ui.Label.Render(fmt.Sprintf(t("wait.status"), status, fmt.Sprintf("%d:%02d", int(el.Minutes()), int(el.Seconds())%60)))
	qr := ui.QR(s.checkout)
	block := text
	switch {
	case lipgloss.Height(qr)+4 <= h && lipgloss.Width(qr)+40 <= w:
		block = lipgloss.JoinHorizontal(lipgloss.Center, qr, "   ", text)
	default:
		block = text + "\n\n" + ui.Muted.Render(t("wait.small"))
	}
	return block + "\n\n" + ui.Label.Render("URL ") + ui.Link.Render(s.checkout)
}

func (s *newPaymentScreen) resultView(ctx *Context, w int) string {
	if s.result == nil {
		out := ui.ErrorBox(s.errText, s.errLines, "")
		if s.note != "" {
			out += "\n\n" + ui.Warning.Render("! ") + ui.Label.Render(wrap(s.note, min(w-2, 74)))
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
	if !s.waitStart.IsZero() && s.response == nil {
		opts.Extra = append(opts.Extra, ui.Row{Label: t("res.waited"), Value: ui.Muted.Render(time.Since(s.waitStart).Round(time.Second).String())})
	}
	out := ui.PaymentCard(p, s.response, opts)
	if s.simulate != "off" && s.req.Order != nil {
		out += "\n" + ui.Warning.Render(" ◆ ") + ui.Label.Render(fmt.Sprintf(t("res.sim"), s.req.Order.Description))
	}
	if s.note != "" {
		out += "\n" + ui.Accent.Render(" › ") + ui.Label.Render(wrap(s.note, min(w-4, 74)))
	}
	if ctx.IsProd() && s.simulate == "off" && s.canRelease() {
		out += "\n" + ui.Danger.Render(" ! ") + ui.Label.Render(wrap(t("res.prod.hint"), min(w-4, 74)))
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
