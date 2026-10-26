package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/pkg/browser"
	"github.com/spf13/cobra"

	"github.com/krebs3r/paygate-cli/internal/cards"
	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/export"
	"github.com/krebs3r/paygate-cli/internal/listen"
	"github.com/krebs3r/paygate-cli/internal/money"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

const (
	flowHosted = string(core.FlowHosted)
	flowMethod = string(core.FlowMethod)
	flowDirect = string(core.FlowDirect)
	flowLink   = string(core.FlowLink)

	manualCard = "__manual__"
)

type payOptions struct {
	flow        string
	hosted      bool
	methods     []string
	method      string
	card        string
	cardNumber  string
	expiry      string
	cvc         string
	holder      string
	amount      string
	currency    string
	description string
	transID     string
	refNr       string
	capture     string
	language    string
	channel     string
	skip3DS     bool
	simulate    string
	ip          string
	open        bool
	noWait      bool
	noQR        bool
	listen      bool
	timeout     time.Duration
	interval    time.Duration

	// paygate link
	send    string
	email   string
	phone   string
	name    string
	expires string
	partial bool
}

func newPayCmd(app *App) *cobra.Command {
	o := &payOptions{}
	cmd := &cobra.Command{
		Use:   "pay",
		Short: "Create a payment (interactive without flags)",
		Long: `Create a payment.

Without flow flags, paygate pay starts an interactive wizard. Choose a flow
with flags for scripts:

  --hosted               hosted payment page with all (or --methods) payment methods
  --method <name>        hosted form for one method (paypal, ideal, sepa, klarna, card …)
  --card <alias>         direct card payment with a test card (see paygate testcards)
  --card-number <pan>    direct card payment with your own card data

Hosted payments send their webhook to a running paygate listen of the same
profile. --listen opens a tunnel just for this payment, waits for the
webhook and closes the tunnel again.`,
		Example: `  paygate pay
  paygate pay --card visa-ok --amount 12.99
  paygate pay --hosted --amount 49 --methods card,paypal,ideal --open
  paygate pay --hosted --amount 49 --listen       # receive its webhook too
  paygate pay --method paypal --amount 5 --currency EUR
  paygate pay --card visa-ok --curl
  paygate pay --card visa-ok --simulate ok        # test transaction on production`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPay(app, o, cmd)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.hosted, "hosted", false, "hosted payment page (checkout session)")
	f.StringSliceVar(&o.methods, "methods", nil, "with --hosted: payment methods to offer (default: all enabled for the MID)")
	f.StringVar(&o.method, "method", "", "hosted form for a single payment method")
	f.StringVar(&o.card, "card", "", "direct card payment with a test card alias")
	f.StringVar(&o.cardNumber, "card-number", "", "direct card payment with this card number")
	f.StringVar(&o.expiry, "expiry", "", "card expiry YYYYMM (default: December, four years ahead)")
	f.StringVar(&o.cvc, "cvc", "123", "card security code")
	f.StringVar(&o.holder, "holder", "Paygate Tester", "card holder name")
	f.StringVar(&o.channel, "channel", "ECOM", "channel for direct payments: ECOM or MOTO")
	f.BoolVar(&o.skip3DS, "skip-3ds", false, "direct card payments: ask Paygate to skip 3-D Secure (test cards without 3DS always do)")
	f.StringVar(&o.ip, "ip", "", "customer IP for direct card payments (default: profile setting or this computer)")
	addPaymentFlags(cmd, o)
	_ = cmd.RegisterFlagCompletionFunc("card", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return cards.Aliases(), cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("method", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return core.MethodAliases(), cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

// addPaymentFlags registers the flags pay and link share.
func addPaymentFlags(cmd *cobra.Command, o *payOptions) {
	f := cmd.Flags()
	f.StringVarP(&o.amount, "amount", "a", "", "amount, e.g. 12.99 (default "+core.DefaultAmount+", on production "+core.ProdDefaultAmount+")")
	f.StringVarP(&o.currency, "currency", "c", "", "ISO currency (default: profile currency, EUR)")
	f.StringVarP(&o.description, "description", "d", "", "order description")
	f.StringVar(&o.transID, "trans-id", "", "your transaction ID (default: generated)")
	f.StringVar(&o.refNr, "ref-nr", "", "your reference number")
	f.StringVar(&o.capture, "capture", "auto", "capture method: auto or manual")
	f.StringVar(&o.language, "language", "", "language of the hosted page, e.g. de, en")
	f.StringVar(&o.simulate, "simulate", "", "test transaction, also on production: ok (Test:0000), decline (Test:0305) or a 4-digit code")
	f.BoolVar(&o.open, "open", false, "open the payment page in the browser")
	f.BoolVar(&o.noWait, "no-wait", false, "do not wait for the payment to complete")
	f.BoolVar(&o.noQR, "no-qr", false, "do not show a QR code for the payment page")
	f.BoolVar(&o.listen, "listen", false, "receive the webhook through a cloudflared tunnel and wait for it")
	f.DurationVar(&o.timeout, "timeout", 10*time.Minute, "how long to wait for the payment")
	f.DurationVar(&o.interval, "poll-interval", 2*time.Second, "status polling interval while waiting")
	_ = f.MarkHidden("poll-interval")
}

func (o *payOptions) detectFlow() error {
	if o.flow == flowLink {
		return nil // paygate link: --methods belongs to the link
	}
	n := 0
	if o.hosted || len(o.methods) > 0 {
		o.flow = flowHosted
		n++
	}
	if o.method != "" {
		o.flow = flowMethod
		n++
	}
	if o.card != "" || o.cardNumber != "" {
		o.flow = flowDirect
		n++
	}
	if n > 1 {
		return errors.New("choose only one flow: --hosted, --method or --card/--card-number")
	}
	return nil
}

func runPay(app *App, o *payOptions, cmd *cobra.Command) error {
	if err := o.detectFlow(); err != nil {
		return err
	}
	name, prof, client, err := app.Client()
	if err != nil {
		return err
	}
	if o.currency == "" {
		o.currency = prof.DefaultCurrency()
	}
	if o.language == "" {
		o.language = prof.DefaultLanguage()
	}
	headerShown := false
	if o.flow == "" {
		if !ui.Interactive() || app.JSON() {
			return errors.New("choose a flow: --hosted, --method <name> or --card <alias> (run `paygate pay --help`)")
		}
		app.PrintHeader(name, prof)
		headerShown = true
		if err := payWizard(o, prof.IsProd()); err != nil {
			return err
		}
		if !cmd.Flags().Changed("open") {
			o.open = true
		}
	}
	if o.amount == "" && prof.IsProd() {
		o.amount = core.ProdDefaultAmount
	}

	in := o.input()
	var hook *payHook
	listenNote := ""
	switch {
	case o.listen:
		switch {
		case o.flow == flowDirect:
			return errors.New("--listen needs a hosted payment (--hosted or --method); direct card payments send no webhook")
		case o.noWait:
			return errors.New("--listen waits for the webhook; leave out --no-wait")
		case app.Curl:
			return errors.New("--listen sends the payment; leave out --curl")
		}
		if !headerShown {
			app.PrintHeader(name, prof)
			headerShown = true
		}
		if hook, err = openPayHook(app, name, prof); err != nil {
			return err
		}
		defer hook.close()
		in.WebhookURL = hook.session.URL
	case o.flow != flowDirect:
		if st, ok := listen.For(name); ok {
			in.WebhookURL = st.URL
			listenNote = "Webhook goes to the running paygate listen " + ui.Muted.Render("("+st.URL+")")
		}
	}

	req, cardLabel, err := core.BuildPaymentRequest(in, prof)
	if err != nil {
		return err
	}
	prepared, err := core.Prepare(client, core.Flow(o.flow), req)
	if err != nil {
		return err
	}
	if app.Curl {
		printCurl(app, prepared, client)
		return nil
	}
	// The header comes first, so a production confirmation shows which
	// profile and merchant it is about.
	if !headerShown {
		app.PrintHeader(name, prof)
	}
	switch {
	case o.flow == flowLink:
		if err := confirmLink(app, prof, req, o.simulate != ""); err != nil {
			return err
		}
	case o.simulate == "":
		if err := app.ConfirmProd(prof, "send this payment"); err != nil {
			return err
		}
	}
	if listenNote != "" && !app.JSON() {
		ui.Println(ui.Info(listenNote))
	}

	sending := "Sending payment to "
	if o.flow == flowLink {
		sending = "Creating payment link at "
	}
	var res *paygate.Result
	err = ui.Spin(sending+hostOf(client.Origin)+" …", func() error {
		var e error
		res, e = client.Exec(context.Background(), prepared)
		return e
	})
	core.RecordPayment(name, prof, core.Flow(o.flow), req, cardLabel, res, err)
	amountStr := money.Format(req.Amount.Value, req.Amount.Currency)
	if err != nil {
		if failureFromError(app, err, cardOpts{Amount: amountStr, Extra: []ui.Row{{Label: "Method", Value: cardLabel}}}) {
			return errSilentFail
		}
		return err
	}

	if url := res.Payment.RedirectURL(); url != "" {
		return handleRedirect(app, name, client, req, res, o, hook)
	}
	if res.Payment.TransID == "" {
		res.Payment.TransID = req.TransID
	}
	if app.JSON() {
		printJSON(res.Response.Body)
	} else {
		ui.Println(ui.OK("Payment processed " + ui.Muted.Render("in "+paygate.FormatDuration(res.Response.Duration))))
		ui.Println()
		ui.Println(paymentCard(res.Payment, res.Response, cardOpts{Amount: amountStr}))
		ui.Println()
	}
	switch paygate.OutcomeOf(res.Payment) {
	case paygate.OutcomeSuccess:
		if !app.JSON() {
			ui.Println(ui.Next("paygate refund "+short(res.Payment.PayID), "paygate get "+short(res.Payment.PayID), "-o json"))
			ui.Println()
		}
		return nil
	case paygate.OutcomeFailed:
		return errSilentFail
	default:
		if !app.JSON() && paygate.ClassifyCode(res.Payment.ResponseCode) == paygate.Code3DS {
			ui.Println(ui.Warn("Paygate requires 3-D Secure, which a direct payment cannot run in the terminal."))
			ui.Println(ui.Info("Add " + ui.Link.Render("--skip-3ds") + ", use a test card without 3DS such as " + ui.Link.Render("visa-ok") + " or pay with " + ui.Link.Render("--hosted") + "."))
			ui.Println()
		}
		return nil
	}
}

// input maps the flags to the shared payment input.
func (o *payOptions) input() core.PaymentInput {
	return core.PaymentInput{
		Flow: core.Flow(o.flow), Methods: o.methods, Method: o.method,
		Card: o.card, CardNumber: o.cardNumber, Expiry: o.expiry, CVC: o.cvc, Holder: o.holder,
		Amount: o.amount, Currency: o.currency, Description: o.description,
		TransID: o.transID, RefNr: o.refNr, Capture: o.capture, Language: o.language,
		Channel: o.channel, Skip3DS: o.skip3DS, Simulate: o.simulate, IP: o.ip,
		Send: o.send, Email: o.email, Phone: o.phone, Name: o.name, Expires: o.expires, Partial: o.partial,
	}
}

func handleRedirect(app *App, profileName string, client *paygate.Client, req *paygate.PaymentRequest, res *paygate.Result, o *payOptions, hook *payHook) error {
	checkout := res.Payment.RedirectURL()
	transID := req.TransID
	created := time.Now()

	if !app.JSON() {
		what := "Checkout session created"
		switch o.flow {
		case flowDirect:
			what = "Customer action required (3-D Secure)"
		case flowMethod:
			what = core.MethodName(req.PaymentMethods.Type) + " checkout created"
		case flowLink:
			what = "Payment link created"
			if to := core.LinkRecipient(req); to != "" {
				what += " · Paygate sends it to " + to
			}
		}
		ui.Println(ui.OK(what + "  " + ui.Muted.Render(durationLine(res.Response))))
		ui.Println()
		withQR := !o.noQR && ui.Interactive()
		lines := []string{
			ui.Bold.Render("Ready for checkout"),
			ui.Label.Render(money.Format(req.Amount.Value, req.Amount.Currency) + " · " + req.TransID),
			"",
			ui.Muted.Render("Scan with your phone or open the link below."),
		}
		label := "Checkout URL"
		switch o.flow {
		case flowDirect:
			lines[0] = ui.Bold.Render("Authenticate the card")
			lines[3] = ui.Muted.Render("Test ACS: OTP 1234 = authenticated.")
		case flowLink:
			label, lines = "Payment link", linkLines(req)
		}
		ui.Println(ui.CheckoutBlock(label, checkout, lines, withQR))
		ui.Println()
	}

	openURL := func() error {
		browser.Stdout, browser.Stderr = io.Discard, io.Discard
		return browser.OpenURL(checkout)
	}
	if o.open {
		if err := openURL(); err != nil && !app.JSON() {
			ui.Println(ui.Warn("Could not open the browser: " + err.Error()))
		} else if !app.JSON() {
			ui.Println(ui.Info("Opened in your browser."))
		}
	}

	if o.noWait {
		if app.JSON() {
			printJSON(res.Response.Body)
		} else {
			ui.Println(ui.Next("paygate get --trans-id " + transID))
			ui.Println()
		}
		return nil
	}

	noun, pending := "URL", "The payment may still complete."
	if o.flow == flowLink {
		noun, pending = "link", "The link stays valid, the customer can still pay."
	}
	var last *paygate.Result
	result := ui.Wait(ui.WaitOptions{
		Noun:     noun,
		URL:      checkout,
		Interval: o.interval,
		Timeout:  o.timeout,
		Open:     openURL,
		Plain:    app.Verbose > 0,
		Fatal:    func(err error) bool { return errors.Is(err, paygate.ErrUnauthorized) },
		Poll: func(ctx context.Context) (string, bool, error) {
			r, err := client.GetByTransID(ctx, transID)
			if err != nil {
				if paygate.IsNotFound(err) {
					return "waiting for customer", false, nil
				}
				return "", false, err
			}
			last = r
			done := paygate.OutcomeOf(r.Payment) != paygate.OutcomePending
			status := r.Payment.Status
			if hook != nil && len(hook.received()) > 0 {
				status += " · webhook received"
			}
			return status, done, nil
		},
	})

	switch {
	case result.Err != nil:
		return result.Err
	case result.Done && last != nil:
		core.RecordStatus(profileName, last.Payment)
		if app.JSON() {
			printJSON(last.Response.Body)
		} else {
			if hook != nil {
				hook.report(created, last.Response)
			}
			p := last.Payment
			title := outcomeTitle(p, "Payment")
			if m := p.Method(); m.Type != "" && m.Type != "CARD" && paygate.OutcomeOf(p) == paygate.OutcomeSuccess {
				title = "Paid with " + core.MethodName(m.Type)
			}
			ui.Println(paymentCard(p, nil, cardOpts{Title: title, Extra: []ui.Row{{Label: "Waited", Value: ui.Muted.Render(result.Elapsed.Round(time.Second).String())}}}))
			ui.Println()
			if paygate.OutcomeOf(p) == paygate.OutcomeSuccess {
				ui.Println(ui.Next("paygate refund "+short(p.PayID), "paygate get "+short(p.PayID)))
				ui.Println()
			}
		}
		if paygate.OutcomeOf(last.Payment) == paygate.OutcomeFailed {
			return errSilentFail
		}
		return nil
	case result.TimedOut:
		if !app.JSON() {
			ui.Println(ui.Warn(fmt.Sprintf("No final status after %s. %s", o.timeout, pending)))
			ui.Println(ui.Next("paygate get --trans-id " + transID))
		}
		return errSilentFail
	default:
		if !app.JSON() {
			ui.Println(ui.Warn("Stopped waiting. " + pending))
			ui.Println(ui.Next("paygate get --trans-id " + transID))
			ui.Println()
		}
		return nil
	}
}

func payWizard(o *payOptions, prod bool) error {
	if o.flow == "" {
		o.flow = flowDirect
	}
	if o.amount == "" {
		o.amount = core.DefaultAmount
		if prod {
			o.amount = core.ProdDefaultAmount
		}
	}
	if o.description == "" {
		o.description = core.DefaultDescription
	}
	if o.simulate == "" && prod {
		o.simulate = "ok"
	}
	simulate := o.simulate
	if simulate == "" {
		simulate = "off"
	}
	cardChoice := "visa-ok"
	if o.card != "" {
		cardChoice = o.card
	}
	single, threeDS := "paypal", "skip"

	// Only cards without 3-D Secure: a direct payment cannot run the 3DS
	// browser steps in a terminal. 3DS cards work on the payment page.
	var cardChoices []huh.Option[string]
	for _, c := range cards.Cards {
		if c.ThreeDS {
			continue
		}
		label := fmt.Sprintf("%-10s  %-19s  %s", brandName(c.Brand), formatPAN(c.Number), c.Expect)
		cardChoices = append(cardChoices, huh.NewOption(label, c.Alias))
	}
	cardChoices = append(cardChoices, huh.NewOption("Enter card details …", manualCard))

	var multiOpts []huh.Option[string]
	for _, a := range []string{"card", "paypal", "ideal", "sepa", "klarna", "applepay", "googlepay", "bancontact", "eps", "twint"} {
		pm, _ := core.ResolveMethod(a)
		multiOpts = append(multiOpts, huh.NewOption(pm.Name, pm.Alias))
	}
	var singleOpts []huh.Option[string]
	for _, a := range core.HostedSingleMethods {
		pm, _ := core.ResolveMethod(a)
		singleOpts = append(singleOpts, huh.NewOption(pm.Name, pm.Alias))
	}
	currencyOpts := huh.NewOptions(money.Common...)
	if !contains(money.Common, o.currency) {
		currencyOpts = append([]huh.Option[string]{huh.NewOption(o.currency, o.currency)}, currencyOpts...)
	}

	err := ui.RunForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Payment flow").
				Options(
					huh.NewOption("Hosted payment page      all methods, opens browser", flowHosted),
					huh.NewOption("Card · direct            server-to-server, test cards", flowDirect),
					huh.NewOption("Single method · hosted   PayPal, iDEAL, SEPA, Klarna …", flowMethod),
				).
				Value(&o.flow),
		),
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Payment methods on the page").
				Description("Leave empty to show every method enabled for your MID.").
				Options(multiOpts...).
				Value(&o.methods),
		).WithHideFunc(func() bool { return o.flow != flowHosted }),
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Payment method").
				Options(singleOpts...).
				Value(&single),
		).WithHideFunc(func() bool { return o.flow != flowMethod }),
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Test card").
				Description("Sent without 3-D Secure. Test 3DS cards with --hosted.").
				Options(cardChoices...).
				Value(&cardChoice),
		).WithHideFunc(func() bool { return o.flow != flowDirect }),
		huh.NewGroup(
			huh.NewInput().Title("Card number").Value(&o.cardNumber).Validate(func(s string) error {
				if len(strings.ReplaceAll(s, " ", "")) < 12 {
					return errors.New("enter a card number")
				}
				return nil
			}),
			huh.NewInput().Title("Expiry (YYYYMM)").Placeholder(cards.DefaultExpiry()).Value(&o.expiry),
			huh.NewInput().Title("CVC").Value(&o.cvc),
			huh.NewSelect[string]().Title("3-D Secure").Options(
				huh.NewOption("Skip", "skip"),
				huh.NewOption("Request (needs a browser)", "request"),
			).Inline(true).Value(&threeDS),
		).WithHideFunc(func() bool { return o.flow != flowDirect || cardChoice != manualCard }),
		huh.NewGroup(
			huh.NewInput().Title("Amount").Value(&o.amount).Validate(func(s string) error {
				_, err := money.Parse(s, o.currency)
				return err
			}),
			huh.NewSelect[string]().Title("Currency").Options(currencyOpts...).Inline(true).Value(&o.currency),
			huh.NewInput().Title("Description").Value(&o.description),
			huh.NewSelect[string]().
				Title("Simulation").
				Description("Test transaction via order description, works on production too.").
				Options(
					huh.NewOption("Off · real transaction", "off"),
					huh.NewOption("Success · Test:0000", "ok"),
					huh.NewOption("Decline · Test:0305", "decline"),
				).Value(&simulate),
		),
	)
	if err != nil {
		return err
	}
	o.simulate = simulate
	if simulate == "off" {
		o.simulate = ""
	}
	switch o.flow {
	case flowMethod:
		o.method = single
	case flowDirect:
		if cardChoice == manualCard {
			o.card = ""
			o.skip3DS = o.skip3DS || threeDS == "skip"
		} else {
			o.card, o.cardNumber = cardChoice, ""
		}
	}
	return nil
}

func printCurl(app *App, p *paygate.Prepared, c *paygate.Client) {
	ui.Println(export.Curl(p, c, app.ShowSecrets))
	if !app.ShowSecrets {
		ui.Eprintln()
		ui.Eprintln(ui.Muted.Render("# The API key is read from $" + export.APIKeyEnv + ". Set it first, or add --show-secrets."))
	}
}

func hostOf(origin string) string {
	if u, err := url.Parse(origin); err == nil && u.Host != "" {
		return u.Host
	}
	return origin
}

func short(id string) string {
	if len(id) > 10 {
		return id[:8] + "…"
	}
	return id
}

func brandName(b string) string {
	switch b {
	case "MASTERCARD":
		return "Mastercard"
	case "AMEX":
		return "Amex"
	case "VISA":
		return "Visa"
	}
	return b
}

func formatPAN(n string) string {
	if len(n) == 15 { // Amex 4-6-5
		return n[:4] + " " + n[4:10] + " " + n[10:]
	}
	var parts []string
	for i := 0; i < len(n); i += 4 {
		end := i + 4
		if end > len(n) {
			end = len(n)
		}
		parts = append(parts, n[i:end])
	}
	return strings.Join(parts, " ")
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}
