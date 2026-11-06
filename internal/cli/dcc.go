package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/krebs3r/paygate-cli/internal/cards"
	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/i18n"
	"github.com/krebs3r/paygate-cli/internal/money"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

func newDCCCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dcc",
		Short: "eDCC (preview): whether eDCC answers, and the offer to pay in a card's currency",
		Long: `eDCC (dynamic currency conversion) lets a customer pay with a foreign card
in the card's currency. Paygate identifies the card's country from its first
10 digits (bin-check) and quotes the amount in the card's currency (quote).
Only the first 10 digits leave this computer; no money moves.

paygate dcc check asks both for the 28 eDCC test cards of the Nexi test
guide and says whether eDCC answers on the account. A card is a test card
alias (see paygate testcards), a card number or its first 10 digits. The
test guide lists the test cards for the test system only, so on production
no offer for them is no proof; check the first 10 digits of a real card as
well. There, pay with them only simulated (--simulate ok); paygate insists
on it for direct payments.

To pay with an offer: paygate pay --card <alias> --dcc ask

Preview: eDCC follows the Paygate API documentation but has not yet been
verified against a merchant account with eDCC enabled. Behaviour and output
may still change.`,
	}
	complete := func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		out := make([]string, len(cards.DCCCards))
		for i, c := range cards.DCCCards {
			out[i] = c.Alias
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
	var checkAmount, checkCurrency string
	check := &cobra.Command{
		Use:   "check [card]",
		Short: "eDCC (preview): does eDCC answer? All 28 test cards, or one card",
		Example: "  paygate dcc check                  # all test cards of the Nexi test guide\n" +
			"  paygate dcc check dcc-usd\n" +
			"  paygate dcc check 4176662280 -o json",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: complete,
		RunE: func(cmd *cobra.Command, args []string) error {
			arg := ""
			if len(args) == 1 {
				arg = args[0]
			}
			return dccCheck(app, arg, checkAmount, checkCurrency)
		},
	}
	check.Flags().StringVarP(&checkAmount, "amount", "a", "", "amount in the shop's currency (default "+core.DefaultAmount+")")
	check.Flags().StringVarP(&checkCurrency, "currency", "c", "", "the shop's currency (default: profile currency, EUR)")
	var amount, currency string
	quote := &cobra.Command{
		Use:   "quote <card>",
		Short: "eDCC (preview): ask whether a card can pay an amount in its own currency",
		Example: "  paygate dcc quote dcc-usd --amount 12.99\n" +
			"  paygate dcc quote 4176662280 --amount 49 --currency EUR -o json",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: complete,
		RunE:              func(cmd *cobra.Command, args []string) error { return dccQuote(app, args[0], amount, currency) },
	}
	quote.Flags().StringVarP(&amount, "amount", "a", "", "amount in the shop's currency (default "+core.DefaultAmount+")")
	quote.Flags().StringVarP(&currency, "currency", "c", "", "the shop's currency (default: profile currency, EUR)")
	cmd.AddCommand(check, quote)
	return cmd
}

// dccCard turns a test card alias, card number or BIN into card digits.
// Anything else is an error, before anything is sent.
func dccCard(arg string) (string, error) {
	if c, ok := cards.FindCard(arg); ok {
		return c.Number, nil
	}
	n := strings.NewReplacer(" ", "", "-", "").Replace(arg)
	if _, err := cards.BIN(n); err != nil {
		return "", fmt.Errorf("%q is no test card alias and no card number with at least 10 digits (see paygate testcards)", arg)
	}
	return n, nil
}

// dccNext suggests what to do after an eDCC check or quote: the same card
// with a payment, simulated where a test card must be.
func dccNext(prof *config.Profile, card string, amount paygate.Amount) []string {
	value := " --amount " + money.FormatValue(amount.Value, amount.Currency)
	if amount.Currency != prof.DefaultCurrency() {
		value += " --currency " + amount.Currency
	}
	pay := "paygate pay --card " + card + value + " --dcc ask"
	if n, err := dccCard(card); prof.IsProd() && (err != nil || cards.IsDCCBIN(n)) {
		pay += " --simulate ok"
	}
	return []string{"paygate dcc quote " + card + value, pay}
}

// dccCheck asks BIN check and quote for one card, or for all test cards of
// the Nexi test guide, and says whether eDCC answers on the account.
func dccCheck(app *App, arg, amount, currency string) error {
	name, prof, client, err := app.Client()
	if err != nil {
		return err
	}
	value, err := core.DCCAmount(core.PaymentInput{Amount: amount, Currency: currency}, prof)
	if err != nil {
		return err
	}
	var numbers []string
	if arg == "" {
		for _, c := range cards.DCCTestCards {
			numbers = append(numbers, c.Number)
		}
	} else {
		n, err := dccCard(arg)
		if err != nil {
			return err
		}
		numbers = []string{n}
	}
	if app.Curl {
		if arg == "" {
			return errors.New("--curl exports one card: paygate dcc check <card> --curl")
		}
		bin, err := cards.BIN(numbers[0])
		if err != nil {
			return err
		}
		for _, mk := range []func() (*paygate.Prepared, error){
			func() (*paygate.Prepared, error) { return client.CheckBinRequest(bin) },
			func() (*paygate.Prepared, error) {
				return client.QuoteDCCRequest(&paygate.DCCQuoteRequest{Bin: bin, Amount: value})
			},
		} {
			p, err := mk()
			if err != nil {
				return err
			}
			printCurl(app, p, client)
		}
		return nil
	}
	if !app.JSON() {
		app.PrintHeader(name, prof)
		ui.Println(ui.Info(i18n.T("dcc.check.intro")))
		ui.Println(ui.Info(i18n.T("dcc.preview")))
		if prof.IsProd() {
			ui.Println(ui.Warn(i18n.T("dcc.check.prodnote")))
		}
		ui.Println()
	}
	// A refused key or no answer at all would fail every card the same way:
	// the check stops at the first.
	var probes []*core.DCCProbe
	var fatal error
	_ = ui.Spin(fmt.Sprintf("Asking %s for %d card(s) …", hostOf(client.Origin), len(numbers)), func() error {
		for _, n := range numbers {
			p := core.ProbeDCC(context.Background(), client, n, value)
			probes = append(probes, p)
			if p.Fatal() {
				fatal = p.Err
				break
			}
		}
		return nil
	})
	sum := core.SummarizeDCC(probes)
	if app.JSON() {
		if err := printCheckJSON(probes, sum); err != nil {
			return err
		}
		return checkResult(fatal, sum)
	}
	rows := make([][]string, len(probes))
	for i, p := range probes {
		bin, brand, expect := "–", "–", p.Expected
		if p.Offer != nil {
			bin = p.Offer.BIN
		} else if b, err := cards.BIN(numbers[i]); err == nil {
			bin = b
		}
		if t, ok := cards.FindDCCTestCard(bin); ok {
			brand = brandName(t.Brand())
		} else {
			bin = cards.ShortBIN(bin) // a real card: 6 digits are enough
		}
		if brand == "" {
			brand = "–"
		}
		if expect == "" {
			expect = "–"
		}
		rows[i] = []string{bin, brand, expect, ui.DCCBinCheckCell(p.Offer), ui.DCCQuoteCell(p), ui.DCCOutcomeCell(p)}
	}
	ui.Println(ui.Table([]string{"BIN", i18n.T("dcc.check.col.card"), i18n.T("dcc.check.col.expect"),
		i18n.T("dcc.check.col.bin"), i18n.T("dcc.check.col.quote"), ""}, rows))
	ui.Println()
	if fatal != nil {
		ui.Println(ui.Warn(fmt.Sprintf(i18n.T("dcc.check.stopped"), len(probes), len(numbers))))
		return fatal
	}
	ui.Println(ui.IndentLines(ui.DCCVerdict(sum), " "))
	ui.Println()
	if sum.Active() {
		card := "dcc-usd"
		if arg != "" {
			card = cardHint(arg)
		}
		ui.Println(ui.Next(dccNext(prof, card, value)...))
		ui.Println()
	}
	return checkResult(nil, sum)
}

// checkResult is the exit of an eDCC check: the error that stopped it, or
// a failure when no card got an answer. Not active is an answer.
func checkResult(fatal error, sum core.DCCSummary) error {
	switch {
	case fatal != nil:
		return fatal
	case sum.Verdict() == core.DCCNoAnswer:
		return errSilentFail
	}
	return nil
}

// printCheckJSON writes an eDCC check as data: per card the BIN check and
// the quote as Paygate answered them, and the summary.
func printCheckJSON(probes []*core.DCCProbe, sum core.DCCSummary) error {
	type answer struct {
		Status int             `json:"status"`
		Body   json.RawMessage `json:"body,omitempty"`
		Error  string          `json:"error,omitempty"`
	}
	type card struct {
		BIN      string  `json:"bin"`
		Expected string  `json:"expectedCurrency,omitempty"`
		Merchant string  `json:"merchantCurrency"`
		BinCheck *answer `json:"binCheck,omitempty"`
		Quote    *answer `json:"quote,omitempty"`
		Outcome  string  `json:"outcome"`
	}
	body := func(r *paygate.Response) json.RawMessage {
		if r == nil || !json.Valid(r.Body) {
			return nil
		}
		return r.Body
	}
	outcomes := map[core.DCCOutcome]string{
		core.DCCAsExpected: "as-expected", core.DCCNotAsExpected: "not-as-expected",
		core.DCCOfferOnly: "offer", core.DCCNoOfferOnly: "no-offer", core.DCCFailed: "failed",
		core.DCCBinRefused: "bin-check-refused",
	}
	out := struct {
		Cards   []card `json:"cards"`
		Summary struct {
			core.DCCSummary
			Active  bool            `json:"active"`
			Verdict core.DCCVerdict `json:"verdict"`
		} `json:"summary"`
	}{}
	for _, p := range probes {
		c := card{Expected: p.Expected, Merchant: p.Merchant, Outcome: outcomes[p.Outcome()]}
		if o := p.Offer; o != nil {
			c.BIN = o.BIN
			if _, ok := cards.FindDCCTestCard(o.BIN); !ok {
				c.BIN = cards.ShortBIN(o.BIN)
			}
			if o.Bin != nil {
				c.BinCheck = &answer{Status: o.BinStatus(), Body: body(o.Bin.Response)}
			}
			if o.BinErr != nil {
				if c.BinCheck == nil {
					c.BinCheck = &answer{}
				}
				c.BinCheck.Error = o.BinErr.Error()
			}
			if o.Quote != nil {
				c.Quote = &answer{Status: o.QuoteStatus(), Body: body(o.Quote.Response)}
			}
		}
		if p.Err != nil {
			if c.Quote == nil {
				c.Quote = &answer{}
			}
			c.Quote.Error = p.Err.Error()
		}
		out.Cards = append(out.Cards, c)
	}
	out.Summary.DCCSummary, out.Summary.Active, out.Summary.Verdict = sum, sum.Active(), sum.Verdict()
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	ui.Println(string(b))
	return nil
}

// cardHint repeats a test card alias in a hint, never a card number.
func cardHint(arg string) string {
	if _, ok := cards.FindCard(arg); ok {
		return arg
	}
	return "<card>"
}

// dccOutcome is the payment to show after an accepted eDCC offer: its
// details, which carry the charged amount and the rate the answer to the
// payment lacks. Without them the answer is shown.
func dccOutcome(client *paygate.Client, req *paygate.PaymentRequest, p *paygate.Payment) *paygate.Payment {
	if !core.DCCAccepted(req) || paygate.OutcomeOf(p) != paygate.OutcomeSuccess {
		return p
	}
	var res *paygate.Result
	err := ui.Spin("Loading the eDCC outcome …", func() error {
		var e error
		res, e = client.GetByTransID(context.Background(), req.TransID)
		return e
	})
	if err != nil || res.Payment == nil {
		ui.Println(ui.Warn("The eDCC outcome could not be loaded; paygate get " + req.TransID + " shows it."))
		return p
	}
	return res.Payment
}

func dccQuote(app *App, arg, amount, currency string) error {
	name, prof, client, err := app.Client()
	if err != nil {
		return err
	}
	number, err := dccCard(arg)
	if err != nil {
		return err
	}
	value, err := core.DCCAmount(core.PaymentInput{Amount: amount, Currency: currency}, prof)
	if err != nil {
		return err
	}
	if app.Curl {
		bin, err := cards.BIN(number)
		if err != nil {
			return err
		}
		p, err := client.QuoteDCCRequest(&paygate.DCCQuoteRequest{Bin: bin, Amount: value})
		if err != nil {
			return err
		}
		printCurl(app, p, client)
		return nil
	}
	if !app.JSON() {
		app.PrintHeader(name, prof)
	}
	offer, err := askQuote(client, number, value)
	if err != nil {
		return err
	}
	if app.JSON() {
		printJSON(offer.Quote.Response.Body)
		return nil
	}
	printOffer(offer, value)
	if offer.Available() {
		ui.Println(ui.Next(dccNext(prof, cardHint(arg), value)[1], "-o json"))
		ui.Println()
	}
	return nil
}

// askQuote asks Paygate for the offer behind a spinner.
func askQuote(client *paygate.Client, number string, amount paygate.Amount) (*core.DCCOffer, error) {
	var offer *core.DCCOffer
	err := ui.Spin("Asking "+hostOf(client.Origin)+" for an eDCC quote …", func() error {
		var e error
		offer, e = core.QuoteDCC(context.Background(), client, number, amount)
		return e
	})
	return offer, err
}

// printOffer shows the BIN check and the quote with the details the eDCC
// rules ask for.
func printOffer(o *core.DCCOffer, amount paygate.Amount) {
	ui.Println(ui.Info(i18n.T("dcc.preview")))
	switch {
	case o.Country != "":
		ui.Println(ui.OK("BIN check   " + cards.ShortBIN(o.BIN) + ui.Muted.Render(" · card country ") + o.Country +
			ui.Muted.Render("   "+durationLine(o.Bin.Response))))
	case o.BinErr != nil:
		ui.Println(ui.Warn("BIN check   " + cards.ShortBIN(o.BIN) + ui.Muted.Render(" · "+o.BinErr.Error())))
	}
	info := o.Info()
	if !o.Available() {
		ui.Println(ui.Info("Quote       no eDCC offer: the card pays " + money.Format(amount.Value, amount.Currency) +
			ui.Muted.Render("   "+durationLine(o.Quote.Response))))
		ui.Println()
		return
	}
	ui.Println(ui.OK("Quote       eDCC available" + ui.Muted.Render("   "+durationLine(o.Quote.Response))))
	ui.Println()
	rows := ui.DCCRows(info.Provider, info.Rate, info.Amounts)
	rows = append(rows, ui.Row{Label: "Quote ID", Value: ui.Muted.Render(info.DCCRequestID)})
	ui.Println(ui.Card{
		Badge:  ui.Badge("eDCC", ui.ToneAccent),
		Title:  "Offer to pay in " + info.Amounts.Cardholder.Currency,
		Amount: ui.DCCAmount(info.Amounts.Cardholder),
		Rows:   rows,
	}.String())
	ui.Println()
}

// decideDCC asks Paygate for an offer and returns the customer's answer
// for a direct card payment: accept, decline, or "" when there is no
// offer. want is ask, accept or decline.
func decideDCC(app *App, client *paygate.Client, prof *config.Profile, in core.PaymentInput, want string) (string, string, error) {
	number, err := core.CardNumber(in)
	if err != nil {
		return "", "", err
	}
	amount, err := core.DCCAmount(in, prof)
	if err != nil {
		return "", "", err
	}
	offer, err := askQuote(client, number, amount)
	if err != nil {
		return "", "", err
	}
	if !app.JSON() {
		printOffer(offer, amount)
	}
	if !offer.Available() {
		if want == core.DCCAccept && !app.JSON() {
			ui.Println(ui.Warn("No offer to accept: the payment goes on in " + amount.Currency + "."))
			ui.Println()
		}
		return "", "", nil
	}
	id := offer.Info().DCCRequestID
	if want != dccAsk {
		return want, id, nil
	}
	if !ui.Interactive() || app.JSON() {
		return "", "", errors.New("--dcc ask needs a terminal to ask in; in scripts use --dcc accept or --dcc decline")
	}
	choice := core.DCCDecline // the shop's currency comes first, as the eDCC rules ask
	ch := offer.Info().Amounts.Cardholder
	err = ui.RunForm(huh.NewGroup(huh.NewSelect[string]().
		Title("How does the customer pay?").
		Options(
			huh.NewOption(money.Format(amount.Value, amount.Currency)+"   in the shop's currency", core.DCCDecline),
			huh.NewOption(ui.DCCAmount(ch)+"   in the card's currency", core.DCCAccept),
		).Value(&choice)))
	if err != nil {
		return "", "", err
	}
	return choice, id, nil
}

const dccAsk = "ask"
