package cli

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/krebs3r/paygate-cli/internal/money"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

// statusBadge renders the payment status with an outcome colour.
func statusBadge(p *paygate.Payment) string {
	st := strings.ToUpper(p.Status)
	switch paygate.OutcomeOf(p) {
	case paygate.OutcomeSuccess:
		if st == "" {
			st = "OK"
		}
		return ui.Badge("✓ "+st, ui.ToneSuccess)
	case paygate.OutcomeFailed:
		if st == "" {
			st = "FAILED"
		}
		return ui.Badge("✗ "+st, ui.ToneDanger)
	default:
		if st == "" {
			st = "PENDING"
		}
		return ui.Badge("… "+st, ui.ToneWarning)
	}
}

func codeLine(p *paygate.Payment) string {
	code := string(p.ResponseCode)
	if code == "" {
		return ""
	}
	desc := p.ResponseDescription
	if desc == "" {
		desc = paygate.DescribeCode(p.ResponseCode)
	}
	style := ui.Success
	switch paygate.ClassifyCode(p.ResponseCode) {
	case paygate.CodeError, paygate.CodeFatal:
		style = ui.Danger
	case paygate.CodePending, paygate.Code3DS:
		style = ui.Warning
	}
	return style.Render(code) + "  " + ui.Muted.Render(desc)
}

func durationLine(resp *paygate.Response) string {
	if resp == nil {
		return ""
	}
	return fmt.Sprintf("%d %s · %s", resp.StatusCode, http.StatusText(resp.StatusCode), paygate.FormatDuration(resp.Duration))
}

type cardOpts struct {
	title  string
	amount string
	extra  []ui.Row
	footer string
}

// paymentCard renders a payment response as a result card.
func paymentCard(p *paygate.Payment, resp *paygate.Response, o cardOpts) string {
	mi := p.Method()
	rows := []ui.Row{
		{Label: "Pay ID", Value: p.PayID},
		{Label: "Trans ID", Value: p.TransID},
		{Label: "xID", Value: ui.Muted.Render(p.XID)},
		{Label: "Method", Value: mi.Label()},
		{Label: "Response", Value: codeLine(p)},
	}
	if mi.ProviderResponseCode != "" && paygate.OutcomeOf(p) == paygate.OutcomeFailed {
		rows = append(rows, ui.Row{Label: "Provider", Value: strings.TrimSpace(mi.ProviderResponseCode + "  " + ui.Muted.Render(mi.ProviderResponseMessage))})
	}
	if mi.IssuerResponseCode != "" {
		rows = append(rows, ui.Row{Label: "Issuer", Value: strings.TrimSpace(mi.IssuerResponseCode + "  " + ui.Muted.Render(mi.IssuerResponseMessage))})
	}
	for _, r := range o.extra {
		if r.Label == "Method" {
			if mi.Label() == "" {
				rows[3].Value = r.Value
			}
			continue
		}
		rows = append(rows, r)
	}
	if resp != nil {
		rows = append(rows, ui.Row{Label: "HTTP", Value: ui.Muted.Render(durationLine(resp))})
		if resp.CorrelationID != "" {
			rows = append(rows, ui.Row{Label: "Correlation", Value: ui.Muted.Render(resp.CorrelationID)})
		}
	}
	amount := o.amount
	if amount == "" && p.Amount != nil && p.Amount.Currency != "" {
		amount = money.Format(p.Amount.Value, p.Amount.Currency)
	}
	title := o.title
	if title == "" {
		title = outcomeTitle(p, "Payment")
	}
	return ui.Card{Badge: statusBadge(p), Title: title, Amount: amount, Rows: rows, Footer: o.footer}.String()
}

func outcomeTitle(p *paygate.Payment, subject string) string {
	switch paygate.OutcomeOf(p) {
	case paygate.OutcomeSuccess:
		return subject + " successful"
	case paygate.OutcomeFailed:
		return subject + " failed"
	default:
		return subject + " pending"
	}
}

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
