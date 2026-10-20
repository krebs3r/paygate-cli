package cli

import (
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

type cardOpts = ui.PaymentCardOpts

func paymentCard(p *paygate.Payment, resp *paygate.Response, o cardOpts) string {
	return ui.PaymentCard(p, resp, o)
}

func outcomeTitle(p *paygate.Payment, subject string) string { return ui.OutcomeTitle(p, subject) }

func durationLine(resp *paygate.Response) string { return ui.DurationLine(resp) }

// printJSON writes a raw API body (pretty-printed) to stdout.
func printJSON(body []byte) {
	if len(body) == 0 {
		ui.Println("{}")
		return
	}
	ui.Println(ui.PrettyJSON(body))
}

// failureFromError renders a declined/failed payment that came back as an
// API error (422 etc.). It returns false if the error carries no payment.
func failureFromError(a *App, err error, o cardOpts) bool {
	e, ok := paygate.AsAPIError(err)
	if !ok || e.Payment == nil || (e.Payment.Status == "" && e.Payment.ResponseCode == "") {
		return false
	}
	if a.JSON() {
		printJSON(e.Response.Body)
		return true
	}
	if e.Payment.Status == "" {
		e.Payment.Status = "FAILED"
	}
	ui.Println(paymentCard(e.Payment, e.Response, o))
	return true
}
