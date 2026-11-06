package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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
	stageQuoting // asking Paygate for an eDCC offer
	stageDCC     // the customer chooses the currency
	stageCheck   // the eDCC check asks one card after the other
)

const manualCardChoice = "__manual__"

// dccCheckFlow is the eDCC check on the eDCC screen: BIN check and quote
// for test cards, without paying.
const dccCheckFlow = "check"

// runSeq numbers the runs of both payment screens, New payment and eDCC.
// The app hands answers to every screen, so a run number must not repeat
// across them: else one screen would take the other's payment as its own.
// Updates run one after the other, so it needs no lock.
var runSeq int

func nextRun() int {
	runSeq++
	return runSeq
}

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
	dccQuoteMsg struct {
		seq   int
		offer *core.DCCOffer
		err   error
	}
	// dccDetailsMsg brings the details of a payment that accepted an eDCC
	// offer: only they carry the charged amount and the rate.
	dccDetailsMsg struct {
		seq int
		res *paygate.Result
		err error
	}
	// dccProbeMsg brings the eDCC check of one card.
	dccProbeMsg struct {
		seq   int
		probe *core.DCCProbe
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
	delivery, email, phone, name, template, expires            string // payment link
	dccPage                                                    string // eDCC screen: the offer on the payment page (on, off, "")
	initialized                                                bool

	// dcc makes this the eDCC screen: card payments with Paygate's offer
	// to pay in the card's currency, here or on the payment page.
	dcc bool
	// eDCC offer of a direct payment and the customer's answer
	offer     *core.DCCOffer
	quoted    paygate.Amount
	dccChoice string

	// eDCC check: what to check (all, one, own), the BIN or the digits typed
	// in, the amount the quotes ask for, the cards and their probes
	checkWhat, checkBIN, checkOwn, checkAmount string
	checkNumbers                               []string
	probes                                     []*core.DCCProbe
	checkRunning                               bool
	checkScroll                                int
	checkFollow                                bool   // the table shows its end until the user scrolls
	checkEnd                                   string // why the check ended early

	// prepared request
	req        *paygate.PaymentRequest
	label      string
	prepared   *paygate.Prepared
	showJSON   bool
	jsonScroll int // first visible line of the request JSON

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

// newDCCScreen is the eDCC screen: the new payment screen for card
// payments with an eDCC offer.
func newDCCScreen() *newPaymentScreen {
	return &newPaymentScreen{spin: newSpinner(), width: 70, dcc: true}
}

func (s *newPaymentScreen) ID() string {
	if s.dcc {
		return "dcc"
	}
	return "new"
}

func (s *newPaymentScreen) Title() string {
	if s.dcc {
		return t("menu.dcc")
	}
	return t("menu.new")
}

func (s *newPaymentScreen) Shortcut() string {
	if s.dcc {
		return "u"
	}
	return "n"
}
func (s *newPaymentScreen) Capturing() bool {
	return s.stage == stageForm && s.form != nil || s.stage == stageConfirmOp || s.stage == stageDCC
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

// Receive starts the form in the given flow, e.g. a payment link from the
// command palette. A payment that is being sent or awaited is left alone.
func (s *newPaymentScreen) Receive(ctx *Context, data any) tea.Cmd {
	flow, ok := data.(core.Flow)
	if !ok || ctx.Session == nil {
		return nil
	}
	switch s.stage {
	case stageSending, stageWaiting, stageOperating:
		return nil
	}
	s.defaults(ctx)
	s.flow = string(flow)
	return s.startForm(ctx)
}

func (s *newPaymentScreen) Back(ctx *Context) (bool, tea.Cmd) {
	switch s.stage {
	case stageReview, stageDCC:
		return true, s.startForm(ctx)
	case stageQuoting, stageCheck:
		s.seq = nextRun() // answers on their way are dropped
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
		return true, s.again(ctx)
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
		hints := []Hint{{"enter", t("key.send")}, {"j", t("key.json")}}
		if s.showJSON {
			hints = append(hints, Hint{"↑↓", t("key.scroll")})
		}
		return append(hints, Hint{"c", t("key.curl")}, Hint{"esc", t("key.edit")})
	case stageWaiting:
		return []Hint{{"z", t("key.qrzoom")}, {"o", t("key.openbrowser")}, {"c", t(s.copyKey())}, {"esc", t("key.stop")}}
	case stageResult:
		hints := []Hint{{"r", t("key.again")}, {s.Shortcut(), t("key.new")}}
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
	case stageQuoting:
		return []Hint{{"esc", t("key.cancel")}}
	case stageCheck:
		hints := []Hint{{"↑↓", t("key.scroll")}}
		if s.checkRunning {
			hints = append(hints, Hint{"x", t("key.stop")})
		} else {
			hints = append(hints, Hint{"r", t("key.again")})
		}
		return append(hints, Hint{"i", "Inspector"}, Hint{"esc", t("key.back")})
	case stageDCC:
		return []Hint{{"↑↓", t("key.select")}, {"enter", t("key.next")}, {"esc", t("key.back")}}
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
	s.delivery, s.expires = core.SendLink, ""
	if p.IsProd() {
		s.amount, s.simulate, s.capture = core.ProdDefaultAmount, "ok", "manual"
	}
	if s.dcc {
		// The eDCC screen starts with the check. Its quotes move no money,
		// so they ask for the usual amount also on production.
		s.flow, s.cardChoice, s.single, s.dccPage = dccCheckFlow, "dcc-usd", "card", "on"
		s.checkWhat, s.checkBIN, s.checkAmount = "all", cards.DCCTestCards[3].BIN, core.DefaultAmount
	}
	s.initialized = true
}

func (s *newPaymentScreen) startForm(ctx *Context) tea.Cmd {
	s.defaults(ctx)
	if ctx.Width > 0 {
		s.width = ctx.Width
	}
	s.stage, s.errText, s.errLines, s.note = stageForm, "", nil, ""
	if s.dcc {
		return s.startDCCForm(ctx)
	}

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

	s.form = newForm(s.width,
		huh.NewGroup(huh.NewSelect[string]().Title(t("new.flow")).Options(
			huh.NewOption(t("flow.direct"), string(core.FlowDirect)),
			huh.NewOption(t("flow.hosted"), string(core.FlowHosted)),
			huh.NewOption(t("flow.method"), string(core.FlowMethod)),
			huh.NewOption(t("flow.link"), string(core.FlowLink)),
		).Value(&s.flow), ui.LiveNote(func() string { return s.endpoint(ctx) })),
		huh.NewGroup(huh.NewMultiSelect[string]().Title(t("new.methods")).Description(t("new.methods.desc")).
			Options(multi...).Value(&s.methods)).
			WithHideFunc(func() bool { return s.flow != string(core.FlowHosted) && s.flow != string(core.FlowLink) }),
		huh.NewGroup(
			huh.NewSelect[string]().Title(t("link.send")).Description(t("link.send.desc")).Options(
				huh.NewOption(t("link.send.link"), core.SendLink),
				huh.NewOption(t("link.send.email"), core.SendEmail),
				huh.NewOption(t("link.send.sms"), core.SendSMS),
			).Value(&s.delivery),
			huh.NewSelect[string]().Title(t("link.valid")).Options(
				huh.NewOption(t("link.valid.none"), ""),
				huh.NewOption(t("link.valid.1h"), "1h"),
				huh.NewOption(t("link.valid.24h"), "24h"),
				huh.NewOption(t("link.valid.7d"), "7d"),
			).Inline(true).Value(&s.expires),
		).WithHideFunc(func() bool { return s.flow != string(core.FlowLink) }),
		huh.NewGroup(
			huh.NewInput().Title(t("link.email")).Placeholder("customer@example.com").Value(&s.email).Validate(validEmail),
			huh.NewInput().Title(t("link.name")).Placeholder("Max Muster").Value(&s.name),
			huh.NewInput().Title(t("link.tpl.email")).Description(t("link.tpl.edesc")).Value(&s.template).Validate(validTemplate(false)),
		).WithHideFunc(func() bool { return s.flow != string(core.FlowLink) || s.delivery != core.SendEmail }),
		huh.NewGroup(
			huh.NewInput().Title(t("link.phone")).Description(t("link.phone.desc")).Placeholder("+49 170 1234567").
				Value(&s.phone).Validate(validPhone),
			huh.NewInput().Title(t("link.name")).Placeholder("Max Muster").Value(&s.name),
			huh.NewInput().Title(t("link.tpl.sms")).Description(t("link.tpl.sdesc")).Value(&s.template).Validate(validTemplate(true)),
		).WithHideFunc(func() bool { return s.flow != string(core.FlowLink) || s.delivery != core.SendSMS }),
		huh.NewGroup(huh.NewSelect[string]().Title(t("new.method")).Options(single...).Value(&s.single)).
			WithHideFunc(func() bool { return s.flow != string(core.FlowMethod) }),
		huh.NewGroup(huh.NewSelect[string]().Title(t("new.card")).Description(t("new.card.desc")).
			Options(cardOpts...).Value(&s.cardChoice)).
			WithHideFunc(func() bool { return s.flow != string(core.FlowDirect) }),
		s.manualCardGroup(),
		s.amountGroup(),
	)
	return s.form.Init()
}

// startDCCForm opens the form of the eDCC screen: a card payment either
// with the offer here in the terminal or on the payment page, where Paygate
// makes it to the customer.
func (s *newPaymentScreen) startDCCForm(ctx *Context) tea.Cmd {
	intro := t("dcc.intro")
	if ctx.Session.Profile.IsProd() {
		// The test cards pay only simulated there; the check moves no money.
		intro += " " + t("dcc.intro.prod")
	}
	intro += "\n\n" + t("dcc.preview")
	var cardOpts []huh.Option[string]
	for _, c := range cards.DCCCards {
		label := fmt.Sprintf("%-10s  %-19s  %s", brandName(c.Brand), formatPAN(c.Number), fmt.Sprintf(t("new.card.dcc"), c.Currency))
		cardOpts = append(cardOpts, huh.NewOption(label, c.Alias))
	}
	cardOpts = append(cardOpts, huh.NewOption(t("new.card.manual"), manualCardChoice))
	var binOpts []huh.Option[string]
	for _, c := range cards.DCCTestCards {
		brand := brandName(c.Brand())
		if brand == "" {
			brand = "–"
		}
		binOpts = append(binOpts, huh.NewOption(fmt.Sprintf("%s  %-10s  %s", c.BIN, brand, c.Currency), c.BIN))
	}
	check := func() bool { return s.flow == dccCheckFlow }
	what := []huh.Field{huh.NewSelect[string]().Title(t("dcc.flow")).Options(
		huh.NewOption(t("dcc.flow.check"), dccCheckFlow),
		huh.NewOption(t("dcc.flow.direct"), string(core.FlowDirect)),
		huh.NewOption(t("dcc.flow.page"), string(core.FlowMethod)),
	).Value(&s.flow)}
	// In a low window the intro gives way, so the choice stays in view:
	// below it, the title, three options and the key help need 9 lines,
	// with the blank line the note brings.
	if lipgloss.Height(wrap(intro, min(ctx.Width, 76)-4))+9 <= ctx.Height-2 {
		what = append([]huh.Field{huh.NewNote().Description(intro)}, what...)
	}
	s.form = newForm(s.width,
		huh.NewGroup(what...),
		huh.NewGroup(huh.NewSelect[string]().Title(t("dcc.check.what")).Description(t("dcc.check.intro")).Options(
			huh.NewOption(fmt.Sprintf(t("dcc.check.all"), len(cards.DCCTestCards)), "all"),
			huh.NewOption(t("dcc.check.one"), "one"),
			huh.NewOption(t("dcc.check.own"), "own"),
		).Value(&s.checkWhat)).
			WithHideFunc(func() bool { return !check() }),
		// Value before Options: huh opens the list at the chosen card. The
		// 28 cards scroll in a low window.
		huh.NewGroup(huh.NewSelect[string]().Title(t("dcc.check.bin")).Description(t("new.dcccard.desc")).
			Value(&s.checkBIN).Options(binOpts...).Height(listHeight(len(binOpts), ctx.Height))).
			WithHideFunc(func() bool { return !check() || s.checkWhat != "one" }),
		// The digits and the amount are checked when the check starts: a
		// validator here would keep shift+tab from going back.
		huh.NewGroup(huh.NewInput().Title(t("dcc.check.ownbin")).Description(t("dcc.check.owndesc")).Value(&s.checkOwn)).
			WithHideFunc(func() bool { return !check() || s.checkWhat != "own" }),
		huh.NewGroup(
			huh.NewInput().Title(t("new.amount")).Description(t("dcc.check.amount")).Value(&s.checkAmount),
			huh.NewSelect[string]().Title(t("new.currency")).Value(&s.currency).Options(currencyOptions(s.currency)...).Inline(true),
		).WithHideFunc(func() bool { return !check() }),
		huh.NewGroup(huh.NewSelect[string]().Title(t("new.dcccard")).Description(t("new.dcccard.desc")).
			Options(cardOpts...).Value(&s.cardChoice)).
			WithHideFunc(func() bool { return s.flow != string(core.FlowDirect) }),
		s.manualCardGroup(),
		// Value before Options: huh scrolls the list to the value it holds
		// when the options come.
		huh.NewGroup(huh.NewSelect[string]().Title(t("new.dccpage")).Description(t("new.dccpage.desc")).Value(&s.dccPage).Options(
			huh.NewOption(t("new.dccpage.on"), "on"),
			huh.NewOption(t("new.dccpage.default"), ""),
			huh.NewOption(t("new.dccpage.off"), "off"),
		)).
			WithHideFunc(func() bool { return s.flow != string(core.FlowMethod) }),
		s.amountGroup().WithHideFunc(check),
	)
	return s.form.Init()
}

// startCheck runs the eDCC check: BIN check and quote for one card after
// the other, without paying.
func (s *newPaymentScreen) startCheck(ctx *Context) tea.Cmd {
	if _, err := money.Parse(s.checkAmount, s.currency); err != nil {
		return s.formWithError(ctx, err)
	}
	switch s.checkWhat {
	case "one":
		s.checkNumbers = []string{s.checkBIN}
	case "own":
		own := strings.NewReplacer(" ", "", "-", "").Replace(s.checkOwn)
		if _, err := cards.BIN(own); err != nil {
			return s.formWithError(ctx, err)
		}
		s.checkNumbers = []string{own}
	default:
		s.checkNumbers = s.checkNumbers[:0]
		for _, c := range cards.DCCTestCards {
			s.checkNumbers = append(s.checkNumbers, c.Number)
		}
	}
	s.seq = nextRun()
	s.stage, s.form, s.probes, s.checkRunning, s.checkScroll, s.checkEnd = stageCheck, nil, nil, true, 0, ""
	s.checkFollow = true
	return tea.Batch(s.spin.Tick, s.probe(ctx))
}

// probe checks the next card of the eDCC check.
func (s *newPaymentScreen) probe(ctx *Context) tea.Cmd {
	seq, client, number := s.seq, ctx.Session.Client, s.checkNumbers[len(s.probes)]
	amount, err := core.DCCAmount(core.PaymentInput{Amount: s.checkAmount, Currency: s.currency}, ctx.Session.Profile)
	if err != nil {
		return func() tea.Msg { return dccProbeMsg{seq: seq, probe: &core.DCCProbe{Err: err}} }
	}
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return dccProbeMsg{seq: seq, probe: core.ProbeDCC(c, client, number, amount)}
	}
}

func (s *newPaymentScreen) onProbe(ctx *Context, msg dccProbeMsg) tea.Cmd {
	s.probes = append(s.probes, msg.probe)
	// A refused key or no answer at all would fail every card the same
	// way: the check stops at the first.
	if msg.probe.Fatal() {
		s.checkRunning, s.checkEnd = false, fmt.Sprintf(t("dcc.check.fatal"), msg.probe.Err)
		return nil
	}
	if len(s.probes) < len(s.checkNumbers) {
		return s.probe(ctx)
	}
	s.checkRunning = false
	return nil
}

// manualCardGroup asks for the card details typed in.
func (s *newPaymentScreen) manualCardGroup() *huh.Group {
	return huh.NewGroup(
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
	).WithHideFunc(func() bool { return s.flow != string(core.FlowDirect) || s.cardChoice != manualCardChoice })
}

// currencyOptions are the common currencies, with the current one first
// when it is none of them, e.g. a profile currency like HUF.
func currencyOptions(current string) []huh.Option[string] {
	currencies := huh.NewOptions(money.Common...)
	if !containsFold(money.Common, current) {
		currencies = append([]huh.Option[string]{huh.NewOption(current, current)}, currencies...)
	}
	return currencies
}

// listHeight lets a long select scroll in a low window: title,
// description and as many options as fit below the steps and above the key
// help. 0 lets huh size it to all options.
func listHeight(options, height int) int {
	if height <= 0 || options+4 <= height-8 {
		return 0
	}
	return max(height-8, 6)
}

// amountGroup asks for amount, currency, description, simulation and
// capture, the last step of every payment.
func (s *newPaymentScreen) amountGroup() *huh.Group {
	currencies := currencyOptions(s.currency)
	return huh.NewGroup(
		huh.NewInput().Title(t("new.amount")).Value(&s.amount).Validate(func(v string) error {
			_, err := money.Parse(v, s.currency)
			return err
		}),
		huh.NewSelect[string]().Title(t("new.currency")).Options(currencies...).Inline(true).Value(&s.currency),
		huh.NewInput().Title(t("new.desc")).Value(&s.description),
		huh.NewSelect[string]().Title(t("new.sim")).DescriptionFunc(func() string {
			if s.flow == string(core.FlowLink) {
				return t("new.sim.link")
			}
			return t("new.sim.desc")
		}, &s.flow).Options(
			huh.NewOption(t("sim.off"), "off"),
			huh.NewOption(t("sim.ok"), "ok"),
			huh.NewOption(t("sim.decline"), "decline"),
		).Value(&s.simulate),
		huh.NewSelect[string]().Title(t("new.capture")).Options(
			huh.NewOption(t("cap.auto"), "auto"),
			huh.NewOption(t("cap.manual"), "manual"),
		).Inline(true).Value(&s.capture),
	)
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
		if s.dcc {
			in.DCCEnabled = s.dccPage
		}
	case core.FlowLink:
		in.Methods, in.Send, in.Expires = s.methods, s.delivery, s.expires
		switch s.delivery {
		case core.SendEmail:
			in.Email, in.Name, in.Template = s.email, s.name, s.template
		case core.SendSMS:
			in.Phone, in.Name, in.Template = s.phone, s.name, s.template
		}
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

// afterForm asks Paygate for an eDCC offer first when the form wants one,
// then shows the review.
func (s *newPaymentScreen) afterForm(ctx *Context) tea.Cmd {
	if s.dcc && s.flow == dccCheckFlow {
		return s.startCheck(ctx)
	}
	s.offer, s.dccChoice = nil, ""
	if s.asksDCC() {
		// Before the quote: nobody should answer an offer for a payment
		// that would be refused.
		if err := core.CheckProdTestCard(s.input(), ctx.Session.Profile); err != nil {
			return s.formWithError(ctx, err)
		}
		return s.quote(ctx)
	}
	return s.review(ctx)
}

// again prepares the payment once more: with a new offer when it asks for
// one, as a quote pays once.
func (s *newPaymentScreen) again(ctx *Context) tea.Cmd {
	if s.asksDCC() {
		return s.afterForm(ctx)
	}
	return s.review(ctx)
}

// asksDCC reports a direct payment of the eDCC screen: Paygate's offer
// comes first.
func (s *newPaymentScreen) asksDCC() bool {
	return s.dcc && core.Flow(s.flow) == core.FlowDirect
}

// quote asks Paygate whether the card can pay in its own currency.
func (s *newPaymentScreen) quote(ctx *Context) tea.Cmd {
	in := s.input()
	number, err := core.CardNumber(in)
	if err != nil {
		return s.formWithError(ctx, err)
	}
	amount, err := core.DCCAmount(in, ctx.Session.Profile)
	if err != nil {
		return s.formWithError(ctx, err)
	}
	s.seq = nextRun()
	seq, client := s.seq, ctx.Session.Client
	s.stage, s.form, s.errText, s.quoted = stageQuoting, nil, "", amount
	return tea.Batch(s.spin.Tick, func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		offer, err := core.QuoteDCC(c, client, number, amount)
		return dccQuoteMsg{seq: seq, offer: offer, err: err}
	})
}

// onQuote shows the offer for the customer to choose, or goes on to the
// review in the shop's currency when there is none.
func (s *newPaymentScreen) onQuote(ctx *Context, msg dccQuoteMsg) tea.Cmd {
	if msg.err != nil {
		return s.formWithError(ctx, fmt.Errorf(t("dcc.failed"), msg.err))
	}
	s.offer = msg.offer
	if !s.offer.Available() {
		return s.review(ctx)
	}
	s.stage, s.dccChoice = stageDCC, core.DCCDecline // the shop's currency first, as the eDCC rules ask
	s.form = newForm(s.width, huh.NewGroup(huh.NewSelect[string]().Title(t("dcc.ask")).Options(
		huh.NewOption(money.Format(s.quoted.Value, s.quoted.Currency)+"   "+t("dcc.inshop"), core.DCCDecline),
		huh.NewOption(ui.DCCAmount(s.offer.Info().Amounts.Cardholder)+"   "+t("dcc.incard"), core.DCCAccept),
	).Value(&s.dccChoice)))
	return s.form.Init()
}

// review builds the request from the form values and shows the summary.
func (s *newPaymentScreen) review(ctx *Context) tea.Cmd {
	in := s.input()
	in.TransID = ids.NewTransID()
	if s.asksDCC() && s.offer.Available() {
		in.DCC, in.DCCRequestID = s.dccChoice, s.offer.Info().DCCRequestID
	}
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
	s.stage, s.form, s.showJSON, s.jsonScroll = stageReview, nil, false, 0
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
		// Answers to the old profile's payment, quote or details are
		// dropped: they belong to its history, not to the new profile.
		s.initialized, s.stage, s.form = false, stageForm, nil
		s.seq, s.req, s.result, s.offer = nextRun(), nil, nil, nil
		return nil
	case payResultMsg:
		if msg.seq == s.seq && s.stage == stageSending {
			return s.onPaid(ctx, msg)
		}
		return nil
	case pollResultMsg:
		if msg.seq == s.seq && s.stage == stageWaiting {
			return s.onPoll(ctx, msg)
		}
		return nil
	case opResultMsg:
		if msg.seq == s.seq && s.stage == stageOperating {
			return s.onOperation(ctx, msg)
		}
		return nil
	case dccQuoteMsg:
		if msg.seq == s.seq && s.stage == stageQuoting {
			return s.onQuote(ctx, msg)
		}
		return nil
	case dccProbeMsg:
		if msg.seq == s.seq && s.stage == stageCheck {
			return s.onProbe(ctx, msg)
		}
		return nil
	case dccDetailsMsg:
		// Also while a refund or cancel is asked for or runs: neither
		// replaces the result.
		if msg.seq == s.seq && (s.stage == stageResult || s.stage == stageConfirmOp || s.stage == stageOperating) {
			s.onDCCDetails(msg)
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
			return s.afterForm(ctx)
		}
		return cmd
	case stageDCC:
		f, cmd := s.form.Update(msg)
		s.form = f.(*huh.Form)
		switch s.form.State {
		case huh.StateAborted:
			return s.startForm(ctx)
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
	case stageCheck:
		switch km.String() {
		case "up", "k":
			s.checkScroll, s.checkFollow = max(0, s.checkScroll-1), false
		case "down", "j":
			s.checkScroll, s.checkFollow = s.checkScroll+1, false // the view stops at the end
		case "x":
			if s.checkRunning {
				s.seq, s.checkRunning = nextRun(), false // the card on its way is dropped
				s.checkEnd = fmt.Sprintf(t("dcc.check.stopped"), len(s.probes), len(s.checkNumbers))
			}
		case "r":
			if !s.checkRunning {
				return s.startCheck(ctx)
			}
		}
	case stageReview:
		switch km.String() {
		case "enter":
			return s.send(ctx)
		case "e", "shift+tab":
			return s.startForm(ctx)
		case "j":
			s.showJSON, s.jsonScroll = !s.showJSON, 0
		case "down":
			s.jsonScroll++ // the view stops at the end of the JSON
		case "up":
			s.jsonScroll = max(0, s.jsonScroll-1)
		case "pgdown", " ", "ctrl+d":
			s.jsonScroll += 10
		case "pgup", "ctrl+u":
			s.jsonScroll = max(0, s.jsonScroll-10)
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
			copied := "copied.url"
			if core.Flow(s.flow) == core.FlowLink {
				copied = "copied.link"
			}
			return toast(t(copied), ui.ToneSuccess)
		}
	case stageResult:
		switch km.String() {
		case "r":
			if s.req != nil && s.asksDCC() {
				return s.afterForm(ctx) // a new offer for the customer to choose
			}
			if s.req != nil {
				if cmd := s.review(ctx); cmd != nil {
					return cmd
				}
				return s.send(ctx)
			}
		case s.Shortcut(), "e": // n, or u on the eDCC screen
			return s.startForm(ctx)
		case "shift+tab":
			return s.again(ctx)
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
	s.seq = nextRun()
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
	if core.DCCAccepted(s.req) && paygate.OutcomeOf(s.result) == paygate.OutcomeSuccess {
		seq, client, transID := s.seq, ctx.Session.Client, s.req.TransID
		return func() tea.Msg {
			c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			res, err := client.GetByTransID(c, transID)
			return dccDetailsMsg{seq: seq, res: res, err: err}
		}
	}
	return nil
}

// onDCCDetails shows the eDCC outcome from the payment details.
func (s *newPaymentScreen) onDCCDetails(msg dccDetailsMsg) {
	if msg.err != nil || msg.res == nil || msg.res.Payment == nil {
		s.note = fmt.Sprintf(t("dcc.nodetails"), msg.err)
		return
	}
	p := msg.res.Payment
	if p.TransID == "" {
		p.TransID = s.req.TransID
	}
	s.result = p
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
		if core.Flow(s.flow) == core.FlowLink && msg.res.Payment.Method().Type == "" {
			s.lastStatus = "" // a link's own record says OK before any attempt
		}
		if core.WaitOutcome(core.Flow(s.flow), msg.res.Payment) != paygate.OutcomePending {
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
	s.seq = nextRun() // ignore polls still in flight
	s.stage, s.note = stageResult, t("res.stopped")
	if core.Flow(s.flow) == core.FlowLink {
		s.note = t("res.stopped.link")
	}
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
		return title(s.Title()) + "\n\n" + ui.Label.Render(t("new.noprofile"))
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
		sending := "new.sending"
		if core.Flow(s.flow) == core.FlowLink {
			sending = "new.sending.link"
		}
		return out + spinView(s.spin) + " " + ui.Label.Render(fmt.Sprintf(t(sending), ctx.Session.Host()))
	case stageWaiting:
		return out + s.waitView(ctx, w, h-2)
	case stageOperating:
		return out + spinView(s.spin) + " " + ui.Label.Render("…")
	case stageConfirmOp:
		return out + s.resultView(ctx, w) + "\n\n" + s.form.View()
	case stageQuoting:
		return out + spinView(s.spin) + " " + ui.Label.Render(fmt.Sprintf(t("dcc.quoting"), ctx.Session.Host()))
	case stageDCC:
		return out + s.offerView(w)
	case stageCheck:
		return out + s.checkView(ctx, w, h-2)
	}
	return out + s.resultView(ctx, w)
}

// checkView shows the eDCC check: per card the BIN check and the offer as
// Paygate answered, and the verdict once all are asked.
func (s *newPaymentScreen) checkView(ctx *Context, w, h int) string {
	intro, note := ui.Label.Render(wrap(t("dcc.check.intro"), min(w, 76)))+"\n", ""
	if ctx.Session.Profile.IsProd() {
		note = hang(ui.Warning.Render("! "), ui.Label.Render(wrap(t("dcc.check.prodnote"), min(w-2, 74)))) + "\n"
	}
	out := intro + note + "\n"
	// The mark comes first, so a narrow window cuts the words, not the
	// result; the note on it takes the rest of the line.
	rows := make([][]string, 0, len(s.probes)+1)
	notes := make([]string, 0, len(s.probes)+1)
	for i, p := range s.probes {
		rows = append(rows, append(append([]string{ui.DCCOutcomeMark(p)}, checkCells(s.checkNumbers[i], p.Expected)...),
			ui.DCCBinCheckCell(p.Offer), ui.DCCQuoteCell(p)))
		notes = append(notes, ui.DCCOutcomeNote(p))
	}
	if s.checkRunning {
		n := s.checkNumbers[len(s.probes)]
		expect := ""
		if c, ok := cards.FindDCCTestCard(n); ok {
			expect = c.Currency
		}
		rows = append(rows, append(append([]string{spinView(s.spin)}, checkCells(n, expect)...), ui.Muted.Render(t("dcc.check.asking")), ""))
		notes = append(notes, "")
	}
	headings := []string{"", "BIN", t("dcc.check.col.card"), t("dcc.check.col.expect"), t("dcc.check.col.bin"), t("dcc.check.col.quote")}
	cols := newColumns(headings, rows)
	lines := []string{ui.Muted.Render(cols.row(headings...))}
	for i, r := range rows {
		lines = append(lines, ansi.Truncate(cols.row(r...)+notes[i], w, "…"))
	}
	foot := "\n" + ui.Muted.Render(fmt.Sprintf(t("dcc.check.progress"), len(s.probes), len(s.checkNumbers)))
	if !s.checkRunning {
		foot = ""
		if s.checkEnd != "" {
			foot = "\n" + ui.Warning.Render("! "+s.checkEnd)
		}
		if len(s.probes) > 0 { // stopped before the first answer: no verdict
			foot += "\n" + wrap(ui.DCCVerdict(core.SummarizeDCC(s.probes)), w)
		}
	}
	// In a low window the intro, then the blank line above the verdict
	// give way, so verdict, hint and counts stay whole. The production note
	// stays: the verdict relies on it.
	if h-lipgloss.Height(out)-lipgloss.Height(foot) < 1 {
		out = note
		if h-lipgloss.Height(out)-lipgloss.Height(foot) < 1 {
			foot = strings.TrimPrefix(foot, "\n")
		}
	}
	// The table scrolls and shows its end, the newest card, until the user
	// scrolls. The verdict always stays in view: out, the heading, the
	// rows and foot fill h exactly.
	room := max(h-lipgloss.Height(out)-lipgloss.Height(foot), 1)
	body := lines[1:]
	if s.checkFollow {
		s.checkScroll = max(len(body)-room, 0)
	}
	s.checkScroll = min(s.checkScroll, max(len(body)-room, 0))
	end := min(s.checkScroll+room, len(body))
	return out + lines[0] + "\n" + strings.Join(body[s.checkScroll:end], "\n") + "\n" + foot
}

// checkCells are the BIN, brand and expected currency of a card in the eDCC
// check. A card that is no test card shows 6 digits of its BIN.
func checkCells(number, expect string) []string {
	bin, brand := number, "–"
	if b, err := cards.BIN(number); err == nil {
		bin = b
	}
	if c, ok := cards.FindDCCTestCard(bin); ok {
		if b := brandName(c.Brand()); b != "" {
			brand = b
		}
	} else {
		bin = cards.ShortBIN(bin)
		if b := brandName(cards.DetectBrand(number)); b != "" {
			brand = b
		}
	}
	if expect == "" {
		expect = "–"
	}
	return []string{bin, brand, expect}
}

// offerView shows Paygate's offer with the rate, markup and source the
// eDCC rules ask to show before the customer chooses.
func (s *newPaymentScreen) offerView(w int) string {
	info := s.offer.Info()
	card := ""
	if n, err := core.CardNumber(s.input()); err == nil {
		card = strings.TrimSpace(brandName(cards.DetectBrand(n)) + " " + cards.Mask(n))
	}
	if s.offer.Country != "" {
		card += " · " + fmt.Sprintf(t("dcc.country"), s.offer.Country)
	}
	rate := ui.DCCRate(info.Amounts, info.Rate)
	if m := ui.DCCMarkup(info.Rate); m != "" {
		rate += ui.Muted.Render(" · " + m)
	}
	rows := [][2]string{
		{t("dcc.rate"), rate},
		{t("dcc.provider"), ui.DCCSource(info.Provider, info.Rate)},
		{"Quote ID", ui.Muted.Render(info.DCCRequestID)},
	}
	return ui.Label.Render(wrap(t("dcc.offer"), min(w, 74))) + "\n" + ui.Muted.Render(card) + "\n\n" +
		kv(rows) + "\n\n" + s.form.View() + "\n" +
		hang(ui.Accent.Render("i "), ui.Muted.Render(wrap(t("dcc.preselected"), min(w-2, 72))))
}

func (s *newPaymentScreen) header(ctx *Context) string {
	// A step's rank orders it: form, offer, review, result.
	type step struct {
		key  string
		rank int
	}
	steps := []step{{"new.step.form", 0}, {"new.step.review", 2}, {"new.step.result", 3}}
	name := title(t("new.title"))
	if s.dcc {
		// eDCC is not yet verified against an account with eDCC enabled.
		name = title(t("menu.dcc")) + " " + ui.Badge(t("dcc.badge"), ui.ToneAccent)
	}
	if s.dcc && s.flow == dccCheckFlow {
		return name + "   " + ui.Link.Render("● "+t("dcc.check.title"))
	}
	if s.asksDCC() {
		steps = []step{{"dcc.step.card", 0}, {"dcc.step.offer", 1}, {"new.step.review", 2}, {"new.step.result", 3}}
	}
	cur := 3
	switch s.stage {
	case stageForm:
		cur = 0
	case stageQuoting, stageDCC:
		cur = 1
	case stageReview:
		cur = 2
	}
	var parts []string
	for _, st := range steps {
		switch {
		case st.rank < cur:
			parts = append(parts, ui.Success.Render("✓ "+t(st.key)))
		case st.rank == cur:
			parts = append(parts, ui.Link.Render("● "+t(st.key)))
		default:
			parts = append(parts, ui.Muted.Render("○ "+t(st.key)))
		}
	}
	return name + "   " + strings.Join(parts, ui.Muted.Render(" ─ "))
}

func (s *newPaymentScreen) reviewView(ctx *Context, w, h int) string {
	p := ctx.Session.Profile
	req := s.req
	rows := [][2]string{{t("rev.flow"), flowLabel(s.flow)}}
	switch core.Flow(s.flow) {
	case core.FlowHosted, core.FlowLink:
		methods := t("rev.methods.all")
		if len(req.AllowedPaymentMethods) > 0 {
			var names []string
			for _, m := range req.AllowedPaymentMethods {
				names = append(names, core.MethodName(m))
			}
			methods = strings.Join(names, ", ")
		}
		rows = append(rows, [2]string{t("rev.methods"), methods})
		if core.Flow(s.flow) == core.FlowLink {
			rows = append(rows, s.linkRows()...)
		}
	default:
		rows = append(rows, [2]string{t("rev.method"), s.label})
	}
	if s.asksDCC() && s.offer != nil {
		rows = append(rows, [2]string{"eDCC", s.dccReview(req)})
	}
	if pm := req.PaymentMethods; pm != nil && pm.Card != nil && pm.Card.DCCEnabled != nil {
		page := t("rev.dccpage.off")
		if *pm.Card.DCCEnabled {
			page = t("rev.dccpage.on")
		}
		rows = append(rows, [2]string{"eDCC", page})
	}
	// The payment page runs 3-D Secure in the browser; only a direct
	// payment has to skip it.
	if pm := req.PaymentMethods; pm != nil && pm.Card != nil && core.Flow(s.flow) == core.FlowDirect {
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
		if s.simulated() {
			out += ui.Warning.Render("! ") + ui.Label.Render(fmt.Sprintf(t("rev.prod.sim"), req.Order.Description)) + "\n"
		} else {
			out += ui.Danger.Render("! "+t("rev.prod.real")) + "\n"
			if s.capture == "manual" {
				out += ui.Label.Render("  "+t("rev.prod.manual")) + "\n"
			}
		}
		// On the eDCC payment page the customer types the card in: a test
		// card there pays for real without a simulation.
		if s.dcc && core.Flow(s.flow) == core.FlowMethod && !s.simulated() {
			out += ui.Warning.Render("! ") + ui.Label.Render(wrap(t("rev.dccpage.prodwarn"), min(w-2, 74))) + "\n"
		}
		// Simulated or not, an e-mail or SMS reaches a real person.
		if to := core.LinkRecipient(req); to != "" {
			out += ui.Warning.Render("! ") + ui.Label.Render(fmt.Sprintf(t("rev.prod.send"), to)) + "\n"
		}
	}
	if core.Flow(s.flow) == core.FlowLink && s.simulate != "off" {
		out += hang(ui.Warning.Render("! "), ui.Label.Render(wrap(t("rev.link.nosim"), min(w-2, 74)))) + "\n"
	}
	if s.showJSON {
		lines := strings.Split(ui.HighlightJSON(maskCard(s.prepared.Body)), "\n")
		room := max(h-lipgloss.Height(out)-2, 5)
		s.jsonScroll = min(s.jsonScroll, max(len(lines)-room, 0))
		visible := lines[s.jsonScroll:min(len(lines), s.jsonScroll+room)]
		if rest := len(lines) - s.jsonScroll - room; rest > 0 {
			visible[len(visible)-1] = ui.Muted.Render(fmt.Sprintf(t("rev.json.more"), rest+1))
		}
		out += "\n" + ui.Muted.Render(s.prepared.Method+" "+s.prepared.URL) + "\n" + strings.Join(visible, "\n")
	}
	return out
}

// dccReview is the customer's answer to the eDCC offer in the review.
func (s *newPaymentScreen) dccReview(req *paygate.PaymentRequest) string {
	info := s.offer.Info()
	switch {
	case !s.offer.Available():
		return ui.Muted.Render(fmt.Sprintf(t("rev.dcc.none"), req.Amount.Currency))
	case s.dccChoice == core.DCCAccept:
		return ui.Success.Render(ui.DCCAmount(info.Amounts.Cardholder)) + ui.Muted.Render(" · "+ui.DCCRate(info.Amounts, info.Rate))
	}
	return fmt.Sprintf(t("rev.dcc.declined"), req.Amount.Currency)
}

// waitText is the status column shown next to (or instead of) the QR code.
// cardHint lists test cards for the payment page. Only on the test system:
// production declines test cards.
func (s *newPaymentScreen) cardHint(ctx *Context) string {
	pageWithMethods := core.Flow(s.flow) == core.FlowHosted || core.Flow(s.flow) == core.FlowLink
	offersCard := pageWithMethods && (len(s.methods) == 0 || containsFold(s.methods, "card")) ||
		core.Flow(s.flow) == core.FlowMethod && s.single == "card"
	if !offersCard || ctx.IsProd() {
		return ""
	}
	if s.dcc {
		// Foreign cards get the offer on the page.
		usd, _ := cards.FindDCCCard("dcc-usd")
		chf, _ := cards.FindDCCCard("dcc-chf")
		return ui.Label.Render(t("wait.dcccards")) + "\n" +
			ui.Text.Render(formatPAN(usd.Number)) + ui.Muted.Render("  USD") + "\n" +
			ui.Text.Render(formatPAN(chf.Number)) + ui.Muted.Render("  CHF")
	}
	visa, _ := cards.FindCard("visa-ok")
	challenge, _ := cards.FindCard("visa-3ds-challenge")
	return ui.Label.Render(t("wait.cards")) + "\n" +
		ui.Text.Render(formatPAN(visa.Number)) + "\n" +
		ui.Text.Render(formatPAN(challenge.Number)) + ui.Muted.Render("  3DS · OTP 1234")
}

func (s *newPaymentScreen) waitText(ctx *Context) string {
	heading, hint, valid := t("wait.title"), t("wait.scan"), ""
	switch core.Flow(s.flow) {
	case core.FlowDirect:
		heading, hint = t("wait.3ds.title"), t("wait.3ds")
	case core.FlowLink:
		heading, hint = t("wait.link.title"), t("wait.link.share")
		if to := core.LinkRecipient(s.req); to != "" {
			heading, hint = fmt.Sprintf(t("wait.link.sent"), to), t("wait.link.scan")
		}
		valid = "\n" + ui.Label.Render(t("wait.link.setting"))
		if ts, err := time.Parse(time.RFC3339, s.req.ExpirationTime); err == nil {
			valid = "\n" + ui.Label.Render(fmt.Sprintf(t("wait.link.valid"), ts.Local().Format("02.01. 15:04")))
		}
	}
	status := s.lastStatus
	if status == "" {
		status = t("wait.none")
	}
	el := time.Since(s.waitStart).Round(time.Second)
	out := ui.Bold.Render(heading) + "\n" +
		ui.Label.Render(money.Format(s.req.Amount.Value, s.req.Amount.Currency)+" · "+s.req.TransID) + valid + "\n\n" +
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
	label := "URL"
	if core.Flow(s.flow) == core.FlowLink {
		label = t("wait.link.label")
	}
	return ui.Label.Render(label) + "  " + ui.Muted.Render(t("wait.urlkeys"))
}

// copyKey names what c copies while waiting: the checkout URL or the link.
func (s *newPaymentScreen) copyKey() string {
	if core.Flow(s.flow) == core.FlowLink {
		return "key.copylink"
	}
	return "key.copyurl"
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
	keys := ui.Muted.Render("esc " + t("key.back") + "  ·  o " + t("key.openbrowser") + "  ·  c " + t(s.copyKey()))
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
		if s.simulated() && s.req.Order != nil {
			sim = s.req.Order.Description
		}
		out = indentLines(ui.Receipt(p, ui.ReceiptOpts{
			Amount: opts.Amount, Method: s.label, Simulation: sim, Prod: ctx.IsProd(), Time: s.paidAt,
		}), " ")
	}
	if s.details && s.simulated() && s.req.Order != nil {
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
	binRe        = regexp.MustCompile(`("bin"\s*:\s*")(\d{6})\d+"`)
)

// maskCard hides card number and CVC in a request body shown on screen.
func maskCard(body []byte) []byte {
	b := cardNumberRe.ReplaceAll(body, []byte(`${1}•••• ${2}"`))
	b = binRe.ReplaceAll(b, []byte(`${1}${2}••••"`))
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
	case core.FlowLink:
		return t("flow.link.short")
	}
	return t("flow.direct.short")
}

// simulated reports a payment Paygate runs as a test transaction. Paygate
// does not apply the simulation to payment links.
func (s *newPaymentScreen) simulated() bool {
	return s.simulate != "off" && core.Flow(s.flow) != core.FlowLink
}

// linkRows say how the customer gets a payment link and until when it is
// valid.
func (s *newPaymentScreen) linkRows() [][2]string {
	delivery := t("rev.link.shown")
	if to := core.LinkRecipient(s.req); to != "" {
		if s.name != "" {
			to = s.name + " · " + to
		}
		via := "rev.link.email"
		if s.req.ServiceType == "SMS" {
			via = "rev.link.sms"
		}
		delivery = fmt.Sprintf(t(via), to)
	}
	rows := [][2]string{{t("rev.link.send"), delivery}}
	if s.req.ServiceType != "" {
		tpl := s.req.ExternalTemplate
		if tpl == "" {
			tpl = ui.Muted.Render(t("rev.link.tpldef"))
		}
		rows = append(rows, [2]string{t("rev.link.tpl"), tpl})
	}
	valid := ui.Muted.Render(t("rev.link.default"))
	if ts, err := time.Parse(time.RFC3339, s.req.ExpirationTime); err == nil {
		valid = ts.Local().Format("02.01.2006 15:04")
	}
	return append(rows, [2]string{t("rev.link.valid"), valid})
}

func validEmail(v string) error {
	if a, err := mail.ParseAddress(v); err != nil || a.Address != v {
		return errors.New(t("link.email.bad"))
	}
	return nil
}

// validTemplate checks the name of an e-mail or SMS template: Paygate takes
// up to 32 characters and needs one for an SMS.
func validTemplate(required bool) func(string) error {
	return func(v string) error {
		switch v = strings.TrimSpace(v); {
		case required && v == "":
			return errors.New(t("link.tpl.bad"))
		case len(v) > 32:
			return errors.New(t("link.tpl.long"))
		}
		return nil
	}
}

func validPhone(v string) error {
	if _, err := core.ParsePhone(v); err != nil {
		return errors.New(t("link.phone.bad"))
	}
	return nil
}

func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}
