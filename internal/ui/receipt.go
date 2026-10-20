package ui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/krebs3r/paygate-cli/internal/i18n"
	"github.com/krebs3r/paygate-cli/internal/money"
	"github.com/krebs3r/paygate-cli/internal/paygate"
)

// receiptInner is the printable width of the receipt.
const receiptInner = 32

// ReceiptOpts add context the payment response does not carry.
type ReceiptOpts struct {
	Amount     string // "12.99 EUR"; taken from the payment when empty
	Method     string // label used when the response has none
	Simulation string // "Test:0000"
	Prod       bool
	Time       time.Time
}

// Receipt renders a payment result as a printed receipt with a torn edge.
func Receipt(p *paygate.Payment, o ReceiptOpts) string {
	border := lipgloss.NewStyle().Foreground(ColMuted2)
	line := func(s string) string {
		pad := receiptInner - lipgloss.Width(s)
		if pad < 0 {
			s, pad = truncateCells(s, receiptInner), 0
		}
		return border.Render("│") + s + strings.Repeat(" ", pad) + border.Render("│")
	}
	center := func(s string) string {
		w := lipgloss.Width(s)
		left := (receiptInner - w) / 2
		if left < 0 {
			left = 0
		}
		return line(strings.Repeat(" ", left) + s)
	}
	row := func(label, value string) string {
		label = " " + strings.ToUpper(label)
		space := receiptInner - lipgloss.Width(label) - 1
		value = truncateCells(value, space-1)
		return line(label + strings.Repeat(" ", space-lipgloss.Width(value)) + value + " ")
	}
	dashes := line(Muted.Render(" " + strings.TrimSpace(strings.Repeat("- ", (receiptInner-2)/2)) + " "))

	var headline string
	switch paygate.OutcomeOf(p) {
	case paygate.OutcomeSuccess:
		headline = Success.Bold(true).Render("✓ " + i18n.T("receipt.approved"))
	case paygate.OutcomeFailed:
		headline = Danger.Bold(true).Render("✗ " + i18n.T("receipt.declined"))
	default:
		headline = Warning.Bold(true).Render("… " + i18n.T("receipt.pending"))
	}

	amount := o.Amount
	if amount == "" && p.Amount != nil && p.Amount.Currency != "" {
		amount = money.Format(p.Amount.Value, p.Amount.Currency)
	}
	mi := p.Method()
	method := mi.Label()
	if method == "" {
		method = o.Method
	}
	code := strings.TrimSpace(string(p.ResponseCode) + " " + p.ResponseDescription)
	when := o.Time
	if when.IsZero() {
		when = time.Now()
	}

	lines := []string{
		border.Render("┌" + strings.Repeat("─", receiptInner) + "┐"),
		center(Accent.Render("◆") + "  " + Bold.Render("P A Y G A T E")),
		center(headline),
		dashes,
		row(i18n.T("receipt.amount"), Bold.Render(amount)),
		row(i18n.T("receipt.method"), method),
		row(i18n.T("receipt.status"), p.Status),
		row("Pay ID", shortID(p.PayID)),
		row("Trans ID", p.TransID),
		row(i18n.T("receipt.code"), code),
	}
	if mi.ProviderResponseCode != "" && paygate.OutcomeOf(p) == paygate.OutcomeFailed {
		lines = append(lines, row(i18n.T("receipt.reason"), strings.TrimSpace(mi.ProviderResponseCode+" "+mi.ProviderResponseMessage)))
	}
	lines = append(lines,
		row(i18n.T("receipt.date"), when.Format("02.01.06 15:04")),
		dashes,
	)
	switch {
	case o.Simulation != "":
		lines = append(lines, center(Warning.Render(i18n.T("receipt.simulated")+" · "+o.Simulation)))
	case o.Prod:
		lines = append(lines, center(Danger.Render(i18n.T("receipt.prod"))))
	default:
		lines = append(lines, center(Muted.Render(i18n.T("receipt.test"))))
	}
	lines = append(lines, border.Render("└"+strings.Repeat("╲╱", receiptInner/2)+"┘"))
	return strings.Join(lines, "\n")
}

func shortID(id string) string {
	if len(id) > 16 {
		return id[:14] + "…"
	}
	return id
}

func truncateCells(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(w-1).Render(s) + "…"
}
