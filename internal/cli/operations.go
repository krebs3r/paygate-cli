package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/krebs3r/paygate-cli/internal/ids"
	"github.com/krebs3r/paygate-cli/internal/money"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

type opKind struct {
	use     string
	op      paygate.Operation
	short   string
	verb    string // "capture", used in prompts
	noun    string // "Capture", used in titles
	aliases []string
}

var (
	opCapture = opKind{"capture", paygate.OpCapture, "Capture an authorized payment", "capture", "Capture", nil}
	opRefund  = opKind{"refund", paygate.OpRefund, "Refund a captured payment (fully or partially)", "refund", "Refund", nil}
	opCancel  = opKind{"cancel", paygate.OpReversal, "Cancel (reverse) a payment before it is captured", "cancel", "Cancellation", []string{"reverse", "void"}}
)

type opOptions struct {
	amount   string
	currency string
	transID  string
	refNr    string
}

func newOperationCmd(app *App, k opKind) *cobra.Command {
	o := &opOptions{}
	cmd := &cobra.Command{
		Use:     k.use + " <payId>",
		Aliases: k.aliases,
		Short:   k.short,
		Example: fmt.Sprintf("  paygate %s 6B29FC4093DD010662DE\n  paygate %s 6B29FC4093DD010662DE --amount 5.00 --yes", k.use, k.use),
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runOperation(app, k, o, args[0])
		},
	}
	f := cmd.Flags()
	f.StringVarP(&o.amount, "amount", "a", "", "amount (default: the remaining amount of the payment)")
	f.StringVarP(&o.currency, "currency", "c", "", "currency (default: the payment's currency)")
	f.StringVar(&o.transID, "trans-id", "", "transaction ID (default: the payment's transId)")
	f.StringVar(&o.refNr, "ref-nr", "", "your reference number for this operation")
	return cmd
}

func runOperation(app *App, k opKind, o *opOptions, payID string) error {
	name, prof, client, err := app.Client()
	if err != nil {
		return err
	}
	if !app.Curl {
		app.PrintHeader(name, prof)
	}

	// Load the payment to fill in transId, currency and the remaining amount.
	var original *paygate.Payment
	needLookup := o.transID == "" || o.amount == "" || o.currency == ""
	if needLookup {
		err = ui.Spin("Loading payment "+short(payID)+" …", func() error {
			r, e := client.GetByPayID(context.Background(), payID)
			if e == nil {
				original = r.Payment
			}
			return e
		})
		if err != nil {
			if errors.Is(err, paygate.ErrUnauthorized) {
				return err
			}
			if o.transID == "" || o.amount == "" || o.currency == "" {
				return fmt.Errorf("could not load payment %s (%v) — pass --trans-id, --amount and --currency explicitly", payID, err)
			}
		}
	}

	transID := o.transID
	if transID == "" && original != nil {
		transID = original.TransID
	}
	currency := strings.ToUpper(o.currency)
	if currency == "" && original != nil && original.Amount != nil {
		currency = original.Amount.Currency
	}
	if currency == "" {
		currency = prof.DefaultCurrency()
	}
	var value int64
	if o.amount != "" {
		value, err = money.Parse(o.amount, currency)
		if err != nil {
			return err
		}
	} else {
		value = defaultOperationAmount(k, original)
		if value <= 0 {
			return fmt.Errorf("nothing left to %s on this payment — pass --amount to override", k.verb)
		}
	}
	if transID == "" {
		return errors.New("transaction ID unknown — pass --trans-id")
	}

	body := &paygate.OperationRequest{TransID: transID, Amount: paygate.Amount{Value: value, Currency: currency}, RefNr: o.refNr}
	prepared, err := client.OperationRequest(k.op, payID, body, ids.NewIdempotencyKey())
	if err != nil {
		return err
	}
	if app.Curl {
		printCurl(app, prepared, client)
		return nil
	}

	amountStr := money.Format(value, currency)
	if !app.Yes {
		if !ui.Interactive() {
			return fmt.Errorf("refusing to %s without confirmation — add --yes", k.verb)
		}
		of := ""
		if original != nil && original.Amount != nil && original.Amount.Currency != "" {
			of = " of " + money.Format(original.Amount.Value, original.Amount.Currency)
		}
		desc := "Payment " + payID
		if prof.IsProd() {
			desc += " · PRODUCTION"
		}
		ok, err := ui.Confirm(fmt.Sprintf("%s %s%s?", titleCase(k.verb), amountStr, of), desc, true)
		if err != nil {
			return err
		}
		if !ok {
			return ui.ErrAborted
		}
	}

	var res *paygate.Result
	err = ui.Spin(fmt.Sprintf("Sending %s …", k.verb), func() error {
		var e error
		res, e = client.Exec(context.Background(), prepared)
		return e
	})
	if err != nil {
		if failureFromError(app, err, cardOpts{title: k.noun + " failed", amount: amountStr}) {
			return errSilentFail
		}
		return err
	}
	if app.JSON() {
		printJSON(res.Response.Body)
	} else {
		if res.Payment.PayID == "" {
			res.Payment.PayID = payID
		}
		title := k.noun + " accepted"
		switch paygate.OutcomeOf(res.Payment) {
		case paygate.OutcomeFailed:
			title = k.noun + " failed"
		case paygate.OutcomePending:
			title = k.noun + " pending"
		}
		ui.Println(paymentCard(res.Payment, res.Response, cardOpts{title: title, amount: amountStr}))
		ui.Println()
		ui.Println(ui.Next("paygate get " + short(payID)))
		ui.Println()
	}
	if paygate.OutcomeOf(res.Payment) == paygate.OutcomeFailed {
		return errSilentFail
	}
	return nil
}

// defaultOperationAmount derives the remaining amount from the payment.
func defaultOperationAmount(k opKind, p *paygate.Payment) int64 {
	if p == nil || p.Amount == nil {
		return 0
	}
	a := p.Amount
	switch k.op {
	case paygate.OpCapture:
		if a.CapturedKnown {
			return a.Value - a.CapturedValue
		}
		return a.Value
	case paygate.OpRefund:
		base := a.Value
		if a.CapturedKnown && a.CapturedValue > 0 {
			base = a.CapturedValue
		}
		if a.RefundedKnown {
			return base - a.RefundedValue
		}
		return base
	default:
		return a.Value
	}
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
