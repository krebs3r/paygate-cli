package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/i18n"
	"github.com/krebs3r/paygate-cli/internal/money"
	"github.com/krebs3r/paygate-cli/internal/paygate"
)

// The eDCC rules ask to show the amount and currency charged, the rate,
// the markup, where the rate comes from and its date. All figures are
// Paygate's, as sent.

// DCCAmount is the amount in the card's currency: Paygate's decimal value
// when it sends one, else its minor units. A decimal value sent as a JSON
// number may have lost trailing zeros ("14.5"); they are put back.
func DCCAmount(a *paygate.CardholderAmount) string {
	if a == nil {
		return ""
	}
	v := a.DecimalValue.String()
	if v == "" {
		return money.Format(a.Value, a.Currency)
	}
	digits := money.Exponent(a.Currency)
	whole, frac, _ := strings.Cut(v, ".")
	if strings.Trim(whole+frac, "0123456789") == "" && len(frac) < digits {
		v = whole + "." + frac + strings.Repeat("0", digits-len(frac))
	}
	return v + " " + a.Currency
}

// DCCRate renders the rate as "1 EUR = 1.1179 USD".
func DCCRate(a *paygate.DCCAmounts, r *paygate.DCCRate) string {
	if r == nil || r.ExchangeRate == "" || a == nil || a.Merchant == nil || a.Cardholder == nil {
		return ""
	}
	return "1 " + a.Merchant.Currency + " = " + r.ExchangeRate.String() + " " + a.Cardholder.Currency
}

// DCCMarkup renders the markup as "3.5 % markup".
func DCCMarkup(r *paygate.DCCRate) string {
	if r == nil || r.MarkupPercentage == "" {
		return ""
	}
	return i18n.Tf("dcc.markup", r.MarkupPercentage.String())
}

// DCCSource is the provider and the date of the rate: "Nets · 30.10.2026".
func DCCSource(provider string, r *paygate.DCCRate) string {
	var parts []string
	if provider != "" {
		parts = append(parts, provider)
	}
	if r != nil && r.Timestamp != "" {
		ts := r.Timestamp
		if t, err := time.Parse("20060102", ts); err == nil {
			ts = t.Format("02.01.2006")
		}
		parts = append(parts, ts)
	}
	return strings.Join(parts, " · ")
}

// DCCRows describe a conversion for a card: amount charged, the shop's
// amount, rate with markup and its source.
func DCCRows(provider string, r *paygate.DCCRate, a *paygate.DCCAmounts) []Row {
	var rows []Row
	if a != nil && a.Cardholder != nil {
		rows = append(rows, Row{Label: i18n.T("dcc.charged"), Value: Bold.Render(DCCAmount(a.Cardholder))})
	}
	if a != nil && a.Merchant != nil {
		rows = append(rows, Row{Label: i18n.T("dcc.shop"), Value: money.Format(a.Merchant.Value, a.Merchant.Currency)})
	}
	if rate := DCCRate(a, r); rate != "" {
		if m := DCCMarkup(r); m != "" {
			rate += Muted.Render(" · " + m)
		}
		rows = append(rows, Row{Label: i18n.T("dcc.rate"), Value: rate})
	}
	if src := DCCSource(provider, r); src != "" {
		rows = append(rows, Row{Label: i18n.T("dcc.provider"), Value: src})
	}
	return rows
}

// dccApplied is the eDCC outcome of a payment the customer paid in the
// card's currency, or nil. A failed payment charged nothing, nor did one
// that still waits, e.g. for 3-D Secure.
func dccApplied(p *paygate.Payment) *paygate.CardDCC {
	mi := p.Method()
	if mi.DCC == nil || !mi.DCC.Selected || mi.DCC.Amounts == nil || mi.DCC.Amounts.Cardholder == nil ||
		paygate.OutcomeOf(p) != paygate.OutcomeSuccess {
		return nil
	}
	return mi.DCC
}

// DCCBinCheckCell says what the BIN check of a probe answered: "200 · USA",
// "422 · 20500077" or the error.
func DCCBinCheckCell(o *core.DCCOffer) string {
	status := o.BinStatus()
	switch {
	case o == nil:
		return Muted.Render("–")
	case o.BinErr == nil && o.Bin != nil && o.Bin.Lookup != nil:
		return Success.Render(fmt.Sprint(status)) + Muted.Render(" · ") + o.Country
	case status != 0:
		code, _ := o.BinError()
		return Danger.Render(fmt.Sprint(status)) + Muted.Render(" · ") + code
	case o.BinErr != nil:
		return Danger.Render("ERR")
	}
	return Muted.Render("–")
}

// DCCQuoteCell says what the quote of a probe answered: the offer with its
// amount, NOT_AVAILABLE, or the error.
func DCCQuoteCell(p *core.DCCProbe) string {
	o := p.Offer
	status := o.QuoteStatus()
	info := o.Info()
	switch {
	case p.Err != nil && status != 0:
		return Danger.Render(fmt.Sprint(status)) + Muted.Render(" · ") + truncateCells(p.Err.Error(), 28)
	case p.Err != nil:
		return Danger.Render("ERR") + Muted.Render(" · ") + truncateCells(p.Err.Error(), 28)
	case o.Available():
		return Success.Render(fmt.Sprintf("%d %s", status, info.Status)) + Muted.Render(" · ") + DCCAmount(info.Amounts.Cardholder)
	case info != nil && info.Status != "":
		return Muted.Render(fmt.Sprintf("%d %s", status, info.Status))
	}
	return Warning.Render(fmt.Sprintf("%d", status)) + Muted.Render(" · "+i18n.T("dcc.check.noinfo"))
}

// DCCOutcomeMark marks a probe in one cell: ✓ as expected, ✗ not as
// expected, BIN check refused or failed, – for a card the test guide does
// not list.
func DCCOutcomeMark(p *core.DCCProbe) string {
	switch p.Outcome() {
	case core.DCCAsExpected:
		return Success.Render("✓")
	case core.DCCNotAsExpected, core.DCCBinRefused:
		return Warning.Render("✗")
	case core.DCCOfferOnly, core.DCCNoOfferOnly:
		return Muted.Render("–")
	}
	return Danger.Render("✗")
}

// DCCOutcomeNote says in words what the mark of a probe means, where it
// needs words.
func DCCOutcomeNote(p *core.DCCProbe) string {
	switch p.Outcome() {
	case core.DCCAsExpected:
		if strings.EqualFold(p.Expected, p.Merchant) {
			return Muted.Render(i18n.T("dcc.check.same"))
		}
	case core.DCCNotAsExpected:
		return Warning.Render(i18n.T("dcc.check.unexpected"))
	case core.DCCOfferOnly:
		return Success.Render(i18n.T("dcc.check.offer"))
	case core.DCCNoOfferOnly:
		return Muted.Render(i18n.T("dcc.check.nooffer"))
	case core.DCCBinRefused:
		return Warning.Render(i18n.T("dcc.check.binrefused"))
	}
	return ""
}

// DCCOutcomeCell is mark and note of a probe together.
func DCCOutcomeCell(p *core.DCCProbe) string {
	if note := DCCOutcomeNote(p); note != "" {
		return DCCOutcomeMark(p) + " " + note
	}
	return DCCOutcomeMark(p)
}

// DCCVerdict sums up an eDCC check: does eDCC answer on the account? On
// production the check's heading says that no offer is no proof there.
func DCCVerdict(s core.DCCSummary) string {
	var line string
	switch s.Verdict() {
	case core.DCCActive:
		line = Success.Render("✓ " + i18n.T("dcc.check.active"))
	case core.DCCRefused:
		hint := "dcc.check.refused.test"
		if s.OtherCards > 0 {
			hint = "dcc.check.refused.other"
		}
		line = Danger.Render("✗ "+dccRefused(s)) + "\n" + Label.Render(i18n.T(hint))
	case core.DCCInactive:
		line = Warning.Render("! " + i18n.T("dcc.check.inactive"))
	case core.DCCNoAnswer:
		line = Danger.Render("✗ " + i18n.T("dcc.check.noanswer"))
	default:
		key := "dcc.check.unclear"
		if s.AsExpected == 0 { // only cards the test guide does not list
			key = "dcc.check.unclear.other"
		}
		line = Label.Render("· " + i18n.T(key))
	}
	return line + "\n" + Muted.Render(dccCounts(s))
}

// dccRefused says for how many cards Paygate refused the BIN check, and
// how: response code and description as sent, else the HTTP status.
// Cards that failed are not among them.
func dccRefused(s core.DCCSummary) string {
	var line string
	switch {
	case s.Total == 1:
		line = i18n.T("dcc.check.refused.one")
	case s.BinRefused == s.Total:
		line = i18n.Tf("dcc.check.refused.all", s.Total)
	default:
		line = i18n.Tf("dcc.check.refused.some", s.BinRefused, s.Total)
	}
	r := s.BinRefusal
	if r == nil {
		return line + i18n.T("dcc.check.refused.codes")
	}
	why := strings.TrimSpace(r.Code + " " + r.Description)
	if why == "" {
		why = fmt.Sprintf("HTTP %d", r.Status)
	}
	return line + ": " + why
}

// dccCounts counts the cards of a check by what they got: the offers
// always, the rest where there are any.
func dccCounts(s core.DCCSummary) string {
	var parts []string
	for i, c := range []struct {
		n         int
		many, one string
	}{
		{s.Offers, "dcc.check.n.offers", "dcc.check.n.offers.one"},
		{s.AsExpected, "dcc.check.n.asexpected", ""},
		{s.NotAsExpected, "dcc.check.n.unexpected", ""},
		{s.BinRefused, "dcc.check.n.refused", "dcc.check.n.refused.one"},
		{s.Failed, "dcc.check.n.failed", ""},
	} {
		if c.n == 0 && i > 0 {
			continue
		}
		key := c.many
		if c.n == 1 && c.one != "" {
			key = c.one
		}
		parts = append(parts, i18n.Tf(key, c.n))
	}
	return strings.Join(parts, " · ")
}
