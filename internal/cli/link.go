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
An SMS needs the name of your SMS template at Paygate (--template); e-mails
use Nexi's standard template unless you name your own.
paygate then waits until the payment is final. The link stays valid when you
stop waiting; paygate get --trans-id shows its payment later.

Like hosted payments, links send their webhook to a running paygate listen
of the same profile, or with --listen to a tunnel just for this payment.`,
		Example: `  paygate link --amount 49
  paygate link --amount 49 --methods card,paypal --expires 24h
  paygate link --amount 49 --send email --email customer@example.com --name "Max Muster"
  paygate link --amount 49 --send sms --phone "+49 170 1234567" --template <your-sms-template>
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
	f.StringVar(&o.template, "template", "", "name of the e-mail or SMS template at Paygate (required for --send sms; e-mails default to Nexi's standard template)")
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

// linkSimulationNote says what --simulate means for a payment link. In tests
// on Nexi, links created with Test:0000 ran their payments for real.
const linkSimulationNote = "Paygate does not apply the simulation to payment links: paying this link moves real money."

// confirmLink asks before a link is created on production, simulated or
// not: paying it moves real money, and with --send Paygate e-mails or texts
// a real person.
func confirmLink(app *App, prof *config.Profile, req *paygate.PaymentRequest) error {
	if !prof.IsProd() || app.Yes || app.Curl {
		return nil
	}
	to := core.LinkRecipient(req)
	if !ui.Interactive() {
		if to != "" {
			return errors.New("refusing to send a payment link to " + to + " on PRODUCTION without --yes")
		}
		return errors.New("refusing to create a payment link on PRODUCTION without --yes")
	}
	question, action := "Paying this link moves real money.", " Create it?"
	if to != "" {
		question, action = "Paygate sends the link to "+to+". "+question, " Create and send it?"
	}
	ok, err := ui.Confirm("This is a PRODUCTION profile", question+action, false)
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
