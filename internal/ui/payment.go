package ui

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/krebs3r/paygate-cli/internal/i18n"
	"github.com/krebs3r/paygate-cli/internal/money"
	"github.com/krebs3r/paygate-cli/internal/paygate"
)

// StatusBadge renders the payment status with an outcome colour.
func StatusBadge(p *paygate.Payment) string {
	st := strings.ToUpper(p.Status)
	switch paygate.OutcomeOf(p) {
	case paygate.OutcomeSuccess:
		if st == "" {
			st = "OK"
		}
		return Badge("✓ "+st, ToneSuccess)
	case paygate.OutcomeFailed:
		if st == "" {
			st = "FAILED"
		}
		return Badge("✗ "+st, ToneDanger)
	default:
		if st == "" {
			st = "PENDING"
		}
		return Badge("… "+st, ToneWarning)
	}
}

// CodeLine renders "00000000  success" coloured by outcome.
func CodeLine(p *paygate.Payment) string {
	code := string(p.ResponseCode)
	if code == "" {
		return ""
	}
	desc := p.ResponseDescription
	if desc == "" {
		desc = paygate.DescribeCode(p.ResponseCode)
	}
	style := Success
	switch paygate.ClassifyCode(p.ResponseCode) {
	case paygate.CodeError, paygate.CodeFatal:
		style = Danger
	case paygate.CodePending, paygate.Code3DS:
		style = Warning
	}
	return style.Render(code) + "  " + Muted.Render(desc)
}

// DurationLine renders "201 Created · 412ms".
func DurationLine(resp *paygate.Response) string {
	if resp == nil {
		return ""
	}
	return fmt.Sprintf("%d %s · %s", resp.StatusCode, http.StatusText(resp.StatusCode), paygate.FormatDuration(resp.Duration))
}

// PaymentCardOpts customise PaymentCard.
type PaymentCardOpts struct {
	Title  string
	Amount string
	Extra  []Row
	Footer string
}

// PaymentCard renders a payment response as a result card.
func PaymentCard(p *paygate.Payment, resp *paygate.Response, o PaymentCardOpts) string {
	mi := p.Method()
	rows := []Row{
		{Label: "Pay ID", Value: p.PayID},
		{Label: "Trans ID", Value: p.TransID},
		{Label: "xID", Value: Muted.Render(p.XID)},
		{Label: i18n.T("card.method"), Value: mi.Label()},
		{Label: i18n.T("card.response"), Value: CodeLine(p)},
	}
	if mi.ProviderResponseCode != "" && paygate.OutcomeOf(p) == paygate.OutcomeFailed {
		rows = append(rows, Row{Label: "Provider", Value: strings.TrimSpace(mi.ProviderResponseCode + "  " + Muted.Render(mi.ProviderResponseMessage))})
	}
	if mi.IssuerResponseCode != "" {
		rows = append(rows, Row{Label: "Issuer", Value: strings.TrimSpace(mi.IssuerResponseCode + "  " + Muted.Render(mi.IssuerResponseMessage))})
	}
	for _, r := range o.Extra {
		if r.Label == "Method" || r.Label == i18n.T("card.method") {
			if mi.Label() == "" {
				rows[3].Value = r.Value
			}
			continue
		}
		rows = append(rows, r)
	}
	if resp != nil {
		rows = append(rows, Row{Label: "HTTP", Value: Muted.Render(DurationLine(resp))})
		if resp.CorrelationID != "" {
			rows = append(rows, Row{Label: "Correlation", Value: Muted.Render(resp.CorrelationID)})
		}
	}
	amount := o.Amount
	if amount == "" && p.Amount != nil && p.Amount.Currency != "" {
		amount = money.Format(p.Amount.Value, p.Amount.Currency)
	}
	title := o.Title
	if title == "" {
		title = OutcomeTitle(p, i18n.T("pay.subject"))
	}
	return Card{Badge: StatusBadge(p), Title: title, Amount: amount, Rows: rows, Footer: o.Footer}.String()
}

// OutcomeTitle returns "Payment successful" etc. for a subject.
func OutcomeTitle(p *paygate.Payment, subject string) string {
	switch paygate.OutcomeOf(p) {
	case paygate.OutcomeSuccess:
		return i18n.Tf("outcome.success", subject)
	case paygate.OutcomeFailed:
		return i18n.Tf("outcome.failed", subject)
	default:
		return i18n.Tf("outcome.pending", subject)
	}
}
