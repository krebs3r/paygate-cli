package ui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/i18n"
	"github.com/krebs3r/paygate-cli/internal/paygate"
)

func TestDCCAmountKeepsPaygatesDigits(t *testing.T) {
	for _, c := range []struct {
		in   paygate.CardholderAmount
		want string
	}{
		{paygate.CardholderAmount{Value: 1450, Currency: "USD", DecimalValue: "14.5"}, "14.50 USD"},
		{paygate.CardholderAmount{Value: 1452, Currency: "USD", DecimalValue: "14,52"}, "14.52 USD"},
		{paygate.CardholderAmount{Value: 2227, Currency: "JPY", DecimalValue: "2227"}, "2227 JPY"},
		{paygate.CardholderAmount{Value: 1452, Currency: "USD"}, "14.52 USD"},
	} {
		if got := DCCAmount(&c.in); got != c.want {
			t.Errorf("DCCAmount(%+v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDCCVerdictRefused(t *testing.T) {
	i18n.Set(i18n.EN)
	refusal := &core.DCCBinRefusal{Status: 422, Code: "20500077", Description: "NO VALID PAYMETHOD FOUND"}
	for _, c := range []struct {
		name string
		sum  core.DCCSummary
		want []string
		not  []string
	}{
		{"all test cards", core.DCCSummary{Total: 28, BinRefused: 28, BinRefusal: refusal},
			[]string{"✗ Paygate refuses the BIN check for all 28 cards: 20500077 NO VALID PAYMETHOD FOUND", "first 10 digits of a real card", "0 offers · 28 BIN checks refused"},
			[]string{"as expected", "failed", "support"}},
		{"one failed", core.DCCSummary{Total: 28, BinRefused: 27, Failed: 1, BinRefusal: refusal},
			[]string{"for 27 of 28 cards: 20500077 NO VALID PAYMETHOD FOUND", "0 offers · 27 BIN checks refused · 1 failed"}, []string{"not active"}},
		{"one other card", core.DCCSummary{Total: 1, BinRefused: 1, OtherCards: 1, BinRefusal: refusal},
			[]string{"Paygate refuses the BIN check: 20500077 NO VALID PAYMETHOD FOUND", "If this is a real card, it is not the test BINs: ask support whether eDCC is enabled", "0 offers · 1 BIN check refused"},
			[]string{"first 10 digits", "checks"}},
		{"different codes", core.DCCSummary{Total: 2, BinRefused: 2},
			[]string{"for all 2 cards, with different codes (see BIN CHECK)"}, nil},
		{"no code", core.DCCSummary{Total: 2, BinRefused: 2, BinRefusal: &core.DCCBinRefusal{Status: 403}},
			[]string{"for all 2 cards: HTTP 403"}, nil},
		{"partly refused", core.DCCSummary{Total: 3, NotAsExpected: 1, BinRefused: 2, CouldOffer: 1},
			[]string{"probably not active", "0 offers · 1 not as expected · 2 BIN checks refused"}, []string{"refuses"}},
		{"one offer", core.DCCSummary{Total: 1, Offers: 1, AsExpected: 1},
			[]string{"1 offer · 1 as expected"}, []string{"offers"}},
		{"other card without offer", core.DCCSummary{Total: 1, OtherCards: 1},
			[]string{"No verdict: no offer, and paygate does not know the currency of this card"}, []string{"shop's currency"}},
		{"test card in the shop's currency", core.DCCSummary{Total: 1, AsExpected: 1},
			[]string{"no offer in the shop's currency anyway"}, []string{"does not know"}},
	} {
		v := ansi.Strip(DCCVerdict(c.sum))
		for _, w := range c.want {
			if !strings.Contains(v, w) {
				t.Errorf("%s: verdict misses %q:\n%s", c.name, w, v)
			}
		}
		for _, n := range c.not {
			if strings.Contains(v, n) {
				t.Errorf("%s: verdict has %q:\n%s", c.name, n, v)
			}
		}
	}
}

func TestReceiptShowsDCCOnlyWhenCharged(t *testing.T) {
	payment := func(status, code string) *paygate.Payment {
		return &paygate.Payment{Status: status, ResponseCode: paygate.Code(code), PaymentMethods: json.RawMessage(`{"type":"CARD","card":{"brand":"VISA","last4Digits":"3305",
			"dccInfo":{"dccSelected":true,"provider":"Nets","rate":{"exchangeRate":1.1179,"markupPercentage":3.5,"timestamp":"20261030"},
			"amounts":{"merchant":{"value":1299,"currency":"EUR"},"cardholder":{"value":1452,"currency":"USD"}}}}}`)}
	}
	ok := ansi.Strip(Receipt(payment("OK", "00000000"), ReceiptOpts{Amount: "12.99 EUR"}))
	for _, want := range []string{"14.52 USD", "eDCC applied", "12.99 EUR", "1 EUR = 1.1179 USD", "3.5 %", "Nets · 30.10.2026"} {
		if !strings.Contains(ok, want) {
			t.Errorf("receipt misses %q:\n%s", want, ok)
		}
	}
	// A failed payment charged nothing, nor did one waiting for 3-D Secure.
	for _, p := range []*paygate.Payment{payment("FAILED", "21000305"), payment("REQUEST", "70000000")} {
		for _, out := range []string{
			ansi.Strip(Receipt(p, ReceiptOpts{Amount: "12.99 EUR"})),
			ansi.Strip(PaymentCard(p, nil, PaymentCardOpts{Amount: "12.99 EUR"})),
		} {
			if strings.Contains(out, "14.52 USD") || strings.Contains(out, "applied") || !strings.Contains(out, "12.99 EUR") {
				t.Errorf("%s: nothing was charged in the card's currency:\n%s", p.Status, out)
			}
		}
	}
}
