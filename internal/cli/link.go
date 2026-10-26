package cli

import (
	"errors"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/money"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

func newLinkCmd(app *App) *cobra.Command {
	o := &payOptions{flow: flowLink}
	cmd := &cobra.Command{
		Use:   "link",
		Short: "Create a payment link to share (Pay by Link)",
		Long: `Create a payment link.

Paygate answers with a link to its hosted payment page. Share it with your
customer, scan its QR code, or let Paygate send it by e-mail or SMS (--send).
paygate then waits until the payment is final. The link stays valid when you
stop waiting; paygate get --trans-id shows its payment later.

Like hosted payments, links send their webhook to a running paygate listen
of the same profile, or with --listen to a tunnel just for this payment.`,
		Example: `  paygate link --amount 49
  paygate link --amount 49 --methods card,paypal --expires 24h
  paygate link --amount 49 --send email --email customer@example.com --name "Max Muster"
  paygate link --amount 49 --send sms --phone "+49 170 1234567"
  paygate link --amount 49 --no-wait -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPay(app, o, cmd)
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&o.methods, "methods", nil, "payment methods on the page (default: all enabled for the MID)")
	f.StringVar(&o.send, "send", core.SendLink, "how the customer gets the link: link (you share it), email or sms (Paygate sends it)")
	f.StringVar(&o.email, "email", "", "customer e-mail address (needed for --send email)")
	f.StringVar(&o.phone, "phone", "", `customer mobile number as "+<country code> <number>" (needed for --send sms)`)
	f.StringVar(&o.name, "name", "", "customer name, e.g. \"Max Muster\"")
	f.StringVar(&o.expires, "expires", "", "how long the link stays valid, e.g. 30m, 24h or 7d (default: Paygate's setting)")
	f.BoolVar(&o.partial, "partial", false, "allow the customer to pay in parts")
	addPaymentFlags(cmd, o)
	_ = cmd.RegisterFlagCompletionFunc("send", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{core.SendLink, core.SendEmail, core.SendSMS}, cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("methods", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return core.MethodAliases(), cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

// confirmLink asks before a link on production moves real money or Paygate
// sends it to a real customer. A simulated payment moves no money, but the
// e-mail or SMS still goes out.
func confirmLink(app *App, prof *config.Profile, req *paygate.PaymentRequest, simulated bool) error {
	to := core.LinkRecipient(req)
	if to == "" {
		if simulated {
			return nil
		}
		return app.ConfirmProd(prof, "create this payment link")
	}
	if !prof.IsProd() || app.Yes || app.Curl {
		return nil
	}
	if !ui.Interactive() {
		return errors.New("refusing to send a payment link to " + to + " on PRODUCTION without --yes")
	}
	question := "Paygate sends the link to " + to + "."
	if !simulated {
		question += " Paying it moves real money."
	}
	ok, err := ui.Confirm("This is a PRODUCTION profile", question+" Create and send it?", false)
	if err != nil {
		return err
	}
	if !ok {
		return ui.ErrAborted
	}
	return nil
}

// linkLines describe a payment link next to its QR code.
func linkLines(req *paygate.PaymentRequest) []string {
	head, hint := "Link ready to share", "Send it to your customer or scan it to pay yourself."
	if to := core.LinkRecipient(req); to != "" {
		head, hint = "Link on its way to "+to, "Scan it to pay yourself."
	}
	methods := "all methods of the MID"
	if len(req.AllowedPaymentMethods) > 0 {
		names := make([]string, len(req.AllowedPaymentMethods))
		for i, m := range req.AllowedPaymentMethods {
			names[i] = core.MethodName(m)
		}
		methods = strings.Join(names, ", ")
	}
	valid := "valid as set at Paygate"
	if t, err := time.Parse(time.RFC3339, req.ExpirationTime); err == nil {
		valid = "valid until " + t.Local().Format("2 Jan 15:04")
	}
	return []string{
		ui.Bold.Render(head),
		ui.Label.Render(money.Format(req.Amount.Value, req.Amount.Currency) + " · " + req.TransID),
		ui.Label.Render(methods + " · " + valid),
		"",
		ui.Muted.Render(hint),
	}
}
