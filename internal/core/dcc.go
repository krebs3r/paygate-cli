package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/krebs3r/paygate-cli/internal/cards"
	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/money"
	"github.com/krebs3r/paygate-cli/internal/paygate"
)

// The customer's answer to an eDCC offer in a direct card payment
// (PaymentInput.DCC). Asking for the offer comes first: QuoteDCC.
const (
	DCCAccept  = "accept"  // pay in the card's currency
	DCCDecline = "decline" // pay in the shop's currency
)

// ErrDCCTestCardNotSimulated keeps the eDCC test cards from paying for real
// on production: there they pay only simulated (order description
// Test:xxxx). BIN check and quote move no money and need no simulation.
var ErrDCCTestCardNotSimulated = errors.New("on production an eDCC test card pays only simulated: add a simulation (--simulate ok)")

// DCCOffer is what Paygate answers about paying with a card in its own
// currency: the card's country from the BIN check and the quote.
type DCCOffer struct {
	BIN     string
	Country string // "" when the BIN check failed
	BinErr  error  // the BIN check is optional; the quote decides
	Bin     *paygate.BinResult
	Quote   *paygate.QuoteResult
}

// Info is the quote's offer, or nil.
func (o *DCCOffer) Info() *paygate.DCCInfo {
	if o == nil {
		return nil
	}
	return o.Quote.Info()
}

// Available reports an offer the customer can accept.
func (o *DCCOffer) Available() bool { return o.Info().Available() }

// DCCAmount is the amount a payment input asks for, which the quote needs.
func DCCAmount(in PaymentInput, prof *config.Profile) (paygate.Amount, error) {
	currency := strings.ToUpper(in.Currency)
	if currency == "" {
		currency = prof.DefaultCurrency()
	}
	amount := in.Amount
	if amount == "" {
		amount = DefaultAmount
	}
	value, err := money.Parse(amount, currency)
	if err != nil {
		return paygate.Amount{}, err
	}
	return paygate.Amount{Value: value, Currency: currency}, nil
}

// CardNumber is the card a direct payment input pays with.
func CardNumber(in PaymentInput) (string, error) {
	if in.Card != "" {
		c, ok := cards.FindCard(in.Card)
		if !ok {
			return "", fmt.Errorf("unknown test card %q (see `paygate testcards`)", in.Card)
		}
		return c.Number, nil
	}
	n := strings.ReplaceAll(in.CardNumber, " ", "")
	if n == "" {
		return "", errors.New("no card selected")
	}
	return n, nil
}

// QuoteDCC asks Paygate whether the card can pay the amount in its own
// currency: first the BIN check for the card's country, then the quote.
// Only the first 10 digits of the card number are sent, so number may
// also be just those.
func QuoteDCC(ctx context.Context, c *paygate.Client, number string, amount paygate.Amount) (*DCCOffer, error) {
	bin, err := cards.BIN(number)
	if err != nil {
		return nil, err
	}
	o := &DCCOffer{BIN: bin}
	o.Bin, o.BinErr = c.CheckBin(ctx, bin)
	if o.BinErr == nil && o.Bin.Lookup != nil {
		o.Country = o.Bin.Lookup.Country()
	}
	o.Quote, err = c.QuoteDCC(ctx, &paygate.DCCQuoteRequest{Bin: bin, Amount: amount})
	return o, err
}

// BinStatus is the HTTP status of the BIN check, 0 without an answer.
func (o *DCCOffer) BinStatus() int {
	if o == nil || o.Bin == nil || o.Bin.Response == nil {
		return 0
	}
	return o.Bin.Response.StatusCode
}

// BinError is the response code and description of a failed BIN check,
// as Paygate sent them.
func (o *DCCOffer) BinError() (code, desc string) {
	if o == nil || o.Bin == nil || o.Bin.Response == nil {
		return "", ""
	}
	var b paygate.BinLookup
	if json.Unmarshal(o.Bin.Response.Body, &b) != nil {
		return "", ""
	}
	return string(b.ResponseCode), b.ResponseDescription
}

// DCCBinRefusal is how Paygate refused a BIN check: HTTP status, response
// code and description, as sent.
type DCCBinRefusal struct {
	Status      int    `json:"status"`
	Code        string `json:"code,omitempty"`
	Description string `json:"description,omitempty"`
}

// BinRefusal is the refusal of the BIN check, or nil when Paygate answered
// it or the BIN check failed.
func (o *DCCOffer) BinRefusal() *DCCBinRefusal {
	status := o.BinStatus()
	if status < 400 || o.binFailed() {
		return nil
	}
	code, desc := o.BinError()
	return &DCCBinRefusal{Status: status, Code: code, Description: desc}
}

// binFailed reports a BIN check without a usable answer: none at all, a
// server error or a throttle. It says nothing about card or merchant.
func (o *DCCOffer) binFailed() bool {
	status := o.BinStatus()
	return o.BinErr != nil && (status == 0 || status >= 500 || status == http.StatusTooManyRequests)
}

// QuoteStatus is the HTTP status of the quote, 0 without an answer.
func (o *DCCOffer) QuoteStatus() int {
	if o == nil || o.Quote == nil || o.Quote.Response == nil {
		return 0
	}
	return o.Quote.Response.StatusCode
}

// DCCProbe is the eDCC check of one card: the BIN check and the quote,
// and the card currency the test guide gives for it.
type DCCProbe struct {
	Offer    *DCCOffer
	Err      error  // the quote failed; the BIN check alone never fails a probe
	Expected string // card currency from the test guide, "" for another card
	Merchant string // the shop's currency the quote asked in
}

// DCCOutcome sorts a probe by what the card currency leads to expect: an
// offer in that currency, or none when it is the shop's own.
type DCCOutcome int

const (
	DCCAsExpected    DCCOutcome = iota // an offer in the card currency, or none in the shop's own
	DCCNotAsExpected                   // the other way round, or another currency
	DCCOfferOnly                       // an offer for a card the test guide does not list
	DCCNoOfferOnly                     // no offer for a card the test guide does not list
	DCCFailed                          // the quote failed, or without an offer the BIN check
	DCCBinRefused                      // no offer, and Paygate refused the BIN check
)

// Outcome sorts the probe.
func (p *DCCProbe) Outcome() DCCOutcome {
	if p.Err != nil || p.Offer == nil {
		return DCCFailed
	}
	offer := p.Offer.Available()
	switch {
	case !offer && p.Offer.binFailed():
		return DCCFailed
	case !offer && p.Offer.BinRefusal() != nil:
		return DCCBinRefused
	case p.Expected == "" && offer:
		return DCCOfferOnly
	case p.Expected == "":
		return DCCNoOfferOnly
	case strings.EqualFold(p.Expected, p.Merchant):
		if offer {
			return DCCNotAsExpected
		}
		return DCCAsExpected
	case offer && strings.EqualFold(p.Offer.Info().Amounts.Cardholder.Currency, p.Expected):
		return DCCAsExpected
	}
	return DCCNotAsExpected
}

// ProbeDCC checks eDCC for one card: BIN check and quote, without paying.
// Only the first 10 digits of the number are sent.
func ProbeDCC(ctx context.Context, c *paygate.Client, number string, amount paygate.Amount) *DCCProbe {
	p := &DCCProbe{Merchant: amount.Currency}
	if t, ok := cards.FindDCCTestCard(number); ok {
		p.Expected = t.Currency
	}
	p.Offer, p.Err = QuoteDCC(ctx, c, number, amount)
	return p
}

// Fatal reports a probe whose quote got no answer from Paygate at all
// (network error) or was refused for the credentials: the other cards
// would fail the same way, so the check stops.
func (p *DCCProbe) Fatal() bool {
	if p.Err == nil || p.Offer == nil {
		return false // an answer, or a card number that was no card number
	}
	return errors.Is(p.Err, paygate.ErrUnauthorized) || p.Offer.QuoteStatus() == 0
}

// expectsOffer reports a probe that could show eDCC is active: a test card
// in another currency than the shop's, whose BIN check and quote Paygate
// answered.
func (p *DCCProbe) expectsOffer() bool {
	o := p.Outcome()
	return (o == DCCAsExpected || o == DCCNotAsExpected) && p.Expected != "" && !strings.EqualFold(p.Expected, p.Merchant)
}

// DCCSummary counts the probes of an eDCC check.
type DCCSummary struct {
	Total         int `json:"total"`
	Offers        int `json:"offers"`
	AsExpected    int `json:"asExpected"`
	NotAsExpected int `json:"notAsExpected"`
	Failed        int `json:"failed"`
	// CouldOffer counts the answered test cards in another currency than
	// the shop's: only they can show that eDCC is not active.
	CouldOffer int `json:"couldOffer"`
	// BinRefused counts the cards without an offer whose BIN check Paygate
	// refused, such as 422 20500077 NO VALID PAYMETHOD FOUND.
	BinRefused int `json:"binRefused"`
	// OtherCards counts the cards that are none of the app's test cards
	// (paygate testcards). If they are real cards, a refusal for them is
	// not about the test BINs.
	OtherCards int `json:"otherCards"`
	// BinRefusal is the refusal every refused card got, by status and
	// code, as the BIN CHECK column shows them. A description they do not
	// share is left out.
	BinRefusal *DCCBinRefusal `json:"binRefusal,omitempty"`
}

// DCCVerdict is what an eDCC check says about the account.
type DCCVerdict string

const (
	DCCActive   DCCVerdict = "active"            // at least one offer
	DCCInactive DCCVerdict = "inactive"          // no offer where one could have come
	DCCNoAnswer DCCVerdict = "no-answer"         // no card got an answer
	DCCUnclear  DCCVerdict = "inconclusive"      // only cards that get no offer anyway
	DCCRefused  DCCVerdict = "bin-check-refused" // Paygate refused every BIN check
)

// Verdict says what the check shows. A BIN check refused for every card
// comes before the offers: it is the first step of eDCC. Cards that failed
// show nothing either way. Without any offer eDCC is probably not active,
// though on production, where the test guide does not plan for the test
// cards, that is no proof; a check without any answer, or only with cards
// in the shop's currency or unknown ones, shows nothing.
func (s DCCSummary) Verdict() DCCVerdict {
	switch {
	case s.Offers > 0:
		return DCCActive
	case s.Failed == s.Total:
		return DCCNoAnswer
	case s.BinRefused+s.Failed == s.Total:
		return DCCRefused
	case s.CouldOffer > 0:
		return DCCInactive
	}
	return DCCUnclear
}

// Active reports that Paygate made at least one offer: eDCC answers on the
// account.
func (s DCCSummary) Active() bool { return s.Verdict() == DCCActive }

// SummarizeDCC counts the probes.
func SummarizeDCC(ps []*DCCProbe) DCCSummary {
	s := DCCSummary{Total: len(ps)}
	mixed := false // refused with different statuses or codes
	for _, p := range ps {
		if p.Offer != nil && p.Offer.Available() {
			s.Offers++
		}
		if p.expectsOffer() {
			s.CouldOffer++
		}
		if p.Offer != nil && !cards.IsTestBIN(p.Offer.BIN) {
			s.OtherCards++
		}
		switch p.Outcome() {
		case DCCAsExpected:
			s.AsExpected++
		case DCCNotAsExpected:
			s.NotAsExpected++
		case DCCFailed:
			s.Failed++
		case DCCBinRefused:
			s.BinRefused++
			r := p.Offer.BinRefusal()
			switch {
			case s.BinRefusal == nil:
				s.BinRefusal = r
			case r.Status != s.BinRefusal.Status || r.Code != s.BinRefusal.Code:
				mixed = true
			case r.Description != s.BinRefusal.Description:
				s.BinRefusal.Description = ""
			}
		}
	}
	if mixed {
		s.BinRefusal = nil
	}
	return s
}

// DCCAccepted reports a direct payment that accepted an eDCC offer. Its
// outcome (charged amount, rate) is in the payment details only, not in the
// answer to the payment.
func DCCAccepted(req *paygate.PaymentRequest) bool {
	return req != nil && req.PaymentMethods != nil && req.PaymentMethods.Card != nil &&
		req.PaymentMethods.Card.DCC != nil && req.PaymentMethods.Card.DCC.Selected
}

// CheckProdTestCard refuses a direct payment with an eDCC test card on
// production that is not simulated. It runs before the quote, so nobody
// answers an offer for a payment that would be refused.
func CheckProdTestCard(in PaymentInput, prof *config.Profile) error {
	if !prof.IsProd() || in.Flow != FlowDirect {
		return nil
	}
	number, err := CardNumber(in)
	if err != nil || !cards.IsDCCBIN(number) {
		return nil
	}
	if sim, err := SimulationDescription(in.Simulate); err == nil && sim == "" {
		return ErrDCCTestCardNotSimulated
	}
	return nil
}

// applyDCC adds the eDCC fields of the input to a card payment. Direct
// payments carry the customer's answer to a quote, hosted ones the switch
// for the offer on the payment page.
func applyDCC(card *paygate.Card, in PaymentInput, direct bool) error {
	if direct {
		if in.DCCEnabled != "" {
			return errors.New("--dcc on|off is for the payment page; a direct payment answers a quote with accept or decline")
		}
		switch strings.ToLower(in.DCC) {
		case "":
		case DCCAccept:
			if in.DCCRequestID == "" {
				return errors.New("accepting eDCC needs the ID of a quote")
			}
			card.DCC = &paygate.DCCDecision{Selected: true, DCCRequestID: in.DCCRequestID}
		case DCCDecline:
			// Without an offer there is nothing to decline.
			if in.DCCRequestID != "" {
				card.DCC = &paygate.DCCDecision{Selected: false, DCCRequestID: in.DCCRequestID}
			}
		default:
			return fmt.Errorf("unknown eDCC answer %q (use accept or decline)", in.DCC)
		}
		return nil
	}
	if in.DCC != "" {
		return errors.New("accept and decline answer a quote in a direct card payment; the payment page asks the customer itself")
	}
	switch strings.ToLower(in.DCCEnabled) {
	case "":
	case "on", "true", "yes":
		on := true
		card.DCCEnabled = &on
	case "off", "false", "no":
		off := false
		card.DCCEnabled = &off
	default:
		return fmt.Errorf("unknown eDCC setting %q (use on or off)", in.DCCEnabled)
	}
	return nil
}
