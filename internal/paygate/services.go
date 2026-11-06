package paygate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// eDCC (dynamic currency conversion) lets a customer pay with a foreign
// card in the card's currency. The BIN check tells the card's country, the
// quote whether Paygate offers a conversion and at which rate. Paygate's
// figures are shown as they come: the rules forbid computing rates.

// BinLookupRequest is the body of POST /services/dcc/bin-check.
type BinLookupRequest struct {
	Bin string `json:"bin"` // the first 10 digits of the card number
}

// BinLookup is the answer of a BIN check.
type BinLookup struct {
	CardInfo            *CardInfo `json:"cardInfo,omitempty"`
	ResponseCode        Code      `json:"responseCode,omitempty"`
	ResponseDescription string    `json:"responseDescription,omitempty"`
}

type CardInfo struct {
	Country string `json:"country,omitempty"` // ISO 3166 alpha-3, e.g. USA
}

// Country is the card's country, or "".
func (b *BinLookup) Country() string {
	if b == nil || b.CardInfo == nil {
		return ""
	}
	return b.CardInfo.Country
}

// DCCQuoteRequest is the body of POST /services/dcc/quote: the amount in
// the shop's currency and the card, by BIN or pseudo card number.
type DCCQuoteRequest struct {
	Bin              string `json:"bin,omitempty"`
	PseudoCardNumber string `json:"pseudoCardNumber,omitempty"`
	Amount           Amount `json:"amount"`
}

// DCCQuote is the answer of a quote.
type DCCQuote struct {
	DCCInfo *DCCInfo `json:"dccInfo,omitempty"`
}

// DCCInfo is an offer to pay in the card's currency, or its absence.
type DCCInfo struct {
	Status       string      `json:"status,omitempty"` // AVAILABLE | NOT_AVAILABLE
	Provider     string      `json:"provider,omitempty"`
	DCCRequestID string      `json:"dccRequestId,omitempty"`
	Rate         *DCCRate    `json:"rate,omitempty"`
	Amounts      *DCCAmounts `json:"amounts,omitempty"`
}

// Available reports an offer the customer can accept: a quote ID and an
// amount in the card's currency.
func (d *DCCInfo) Available() bool {
	return d != nil && strings.EqualFold(d.Status, "AVAILABLE") && d.DCCRequestID != "" &&
		d.Amounts != nil && d.Amounts.Cardholder != nil && d.Amounts.Cardholder.Currency != ""
}

type DCCRate struct {
	ExchangeRate     Decimal `json:"exchangeRate,omitempty"`
	MarkupPercentage Decimal `json:"markupPercentage,omitempty"`
	Timestamp        string  `json:"timestamp,omitempty"` // when the rate was set, e.g. 20241129
}

type DCCAmounts struct {
	Merchant   *Amount           `json:"merchant,omitempty"`
	Cardholder *CardholderAmount `json:"cardholder,omitempty"`
}

// CardholderAmount is the converted amount in the card's currency.
type CardholderAmount struct {
	Value        int64   `json:"value"`
	Currency     string  `json:"currency"`
	DecimalValue Decimal `json:"decimalValue,omitempty"`
}

// CardDCC is the eDCC outcome of a card payment, in the card details of
// the payment (paymentMethods.card.dccInfo).
type CardDCC struct {
	Selected bool        `json:"dccSelected"`
	Provider string      `json:"provider,omitempty"`
	Rate     *DCCRate    `json:"rate,omitempty"`
	Amounts  *DCCAmounts `json:"amounts,omitempty"`
}

// DCCDecision is the customer's answer to a quote in a direct card
// payment (paymentMethods.card.dcc).
type DCCDecision struct {
	Selected     bool   `json:"selected"`
	DCCRequestID string `json:"dccRequestId,omitempty"`
}

// Decimal is a number Paygate sends as a JSON number or as a string, the
// documentation's examples also with a decimal comma ("1,2313"). It keeps
// the digits as sent.
type Decimal string

func (d *Decimal) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	switch {
	case s == "null":
		s = ""
	case strings.HasPrefix(s, `"`):
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
	}
	*d = Decimal(strings.TrimSpace(s))
	return nil
}

// String is the number with a decimal point.
func (d Decimal) String() string {
	s := string(d)
	if strings.Contains(s, ",") && !strings.Contains(s, ".") {
		s = strings.Replace(s, ",", ".", 1)
	}
	return s
}

// BinResult is a BIN check plus the raw response.
type BinResult struct {
	Lookup   *BinLookup
	Response *Response
}

// QuoteResult is a quote plus the raw response.
type QuoteResult struct {
	Quote    *DCCQuote
	Response *Response
}

// Info is the quote's offer, or nil.
func (r *QuoteResult) Info() *DCCInfo {
	if r == nil || r.Quote == nil {
		return nil
	}
	return r.Quote.DCCInfo
}

func (c *Client) CheckBinRequest(bin string) (*Prepared, error) {
	return c.Prepare(http.MethodPost, "/services/dcc/bin-check", &BinLookupRequest{Bin: bin}, "")
}

func (c *Client) QuoteDCCRequest(r *DCCQuoteRequest) (*Prepared, error) {
	return c.Prepare(http.MethodPost, "/services/dcc/quote", r, "")
}

// CheckBin asks for the country of a card by its first 10 digits.
func (c *Client) CheckBin(ctx context.Context, bin string) (*BinResult, error) {
	p, err := c.CheckBinRequest(bin)
	if err != nil {
		return nil, err
	}
	resp, err := c.Send(ctx, p)
	res := &BinResult{Response: resp}
	if err != nil {
		return res, err
	}
	var b BinLookup
	if err := json.Unmarshal(resp.Body, &b); err != nil {
		return res, fmt.Errorf("decode response: %w", err)
	}
	res.Lookup = &b
	return res, nil
}

// QuoteDCC asks for an offer to pay in the card's currency. A card without
// an offer is answered with 422 and status NOT_AVAILABLE; that is a result,
// not an error.
func (c *Client) QuoteDCC(ctx context.Context, r *DCCQuoteRequest) (*QuoteResult, error) {
	p, err := c.QuoteDCCRequest(r)
	if err != nil {
		return nil, err
	}
	resp, err := c.Send(ctx, p)
	res := &QuoteResult{Response: resp}
	var q DCCQuote
	decoded := resp != nil && len(resp.Body) > 0 && json.Unmarshal(resp.Body, &q) == nil
	if decoded {
		res.Quote = &q
	}
	var apiErr *APIError
	if err != nil && errors.As(err, &apiErr) && resp.StatusCode == http.StatusUnprocessableEntity && decoded &&
		q.DCCInfo != nil && strings.EqualFold(q.DCCInfo.Status, "NOT_AVAILABLE") {
		return res, nil
	}
	if err != nil {
		return res, err
	}
	if !decoded {
		return res, errors.New("decode response: no quote in the answer")
	}
	return res, nil
}
