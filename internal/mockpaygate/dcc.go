package mockpaygate

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/krebs3r/paygate-cli/internal/cards"
	"github.com/krebs3r/paygate-cli/internal/money"
)

// dccCard is how the mock treats the BIN of an eDCC test card: the card's
// country and currency and a made-up rate from euros, markup included.
type dccCard struct {
	Country, Currency string
	Rate              float64
}

// dccCurrencies are the made-up countries and rates of the card currencies
// in the Nexi test guide.
var dccCurrencies = map[string]dccCard{
	"USD": {"USA", "USD", 1.1179},
	"CHF": {"CHE", "CHF", 0.9553},
	"JPY": {"JPN", "JPY", 171.42},
	"SEK": {"SWE", "SEK", 11.6342},
	"NOK": {"NOR", "NOK", 12.0518},
	"DKK": {"DNK", "DKK", 7.7183},
	"PLN": {"POL", "PLN", 4.3605},
	"NZD": {"NZL", "NZD", 1.9387},
	"THB": {"THA", "THB", 37.9104},
	"ISK": {"ISL", "ISK", 149.85},
	"EUR": {"DEU", "EUR", 1},
}

// dccBINs are the eDCC test cards of the Nexi test guide
// (cards.DCCTestCards). Other BINs are treated as cards from Germany in
// euros.
var dccBINs = func() map[string]dccCard {
	out := map[string]dccCard{}
	for _, c := range cards.DCCTestCards {
		out[c.BIN] = dccCurrencies[c.Currency]
	}
	return out
}()

const dccMarkup = 3.5

// quote is an offer the mock made; a direct payment may accept it once.
type quote struct {
	ID, BIN          string
	Merchant         int64
	MerchantCurrency string
	Cardholder       int64
	Currency         string
	Rate             float64
	Time             time.Time
}

func (q *quote) info(selected bool, withID bool) map[string]any {
	digits := money.Exponent(q.Currency)
	out := map[string]any{
		"provider": "Nets",
		"rate": map[string]any{
			"exchangeRate": q.Rate, "markupPercentage": dccMarkup, "timestamp": q.Time.Format("20060102"),
		},
		"amounts": map[string]any{
			"merchant": map[string]any{"value": q.Merchant, "currency": q.MerchantCurrency},
			"cardholder": map[string]any{
				"value": q.Cardholder, "currency": q.Currency,
				"decimalValue": json.Number(strconv.FormatFloat(float64(q.Cardholder)/math.Pow10(digits), 'f', digits, 64)),
			},
		},
	}
	if withID {
		out["status"], out["dccRequestId"] = "AVAILABLE", q.ID
	} else {
		out["dccSelected"] = selected
	}
	return out
}

func validBIN(bin string) bool {
	return len(bin) == 10 && strings.Trim(bin, "0123456789") == ""
}

func (s *Server) binCheck(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Bin string `json:"bin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil || !validBIN(b.Bin) {
		problem(w, map[string][]string{"bin": {"The bin must be 10 digits."}})
		return
	}
	if s.DCCOff {
		// What a production merchant without eDCC got on 2026-11-03.
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"responseCode": "20500077", "responseDescription": "NO VALID PAYMETHOD FOUND", "title": "Processing error",
		})
		return
	}
	country := "DEU"
	if c, ok := dccBINs[b.Bin]; ok {
		country = c.Country
	}
	writeJSON(w, http.StatusOK, map[string]any{"cardInfo": map[string]any{"country": country}})
}

func (s *Server) dccQuote(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Bin    string `json:"bin"`
		Amount *struct {
			Value    int64  `json:"value"`
			Currency string `json:"currency"`
		} `json:"amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil || !validBIN(b.Bin) {
		problem(w, map[string][]string{"bin": {"The bin must be 10 digits."}})
		return
	}
	if b.Amount == nil || b.Amount.Value <= 0 || len(b.Amount.Currency) != 3 {
		problem(w, map[string][]string{"amount": {"The amount value must be greater than 0."}})
		return
	}
	card, ok := dccBINs[b.Bin]
	ok = ok && !s.DCCOff
	from := strings.ToUpper(b.Amount.Currency)
	// The made-up rates start in euros; cards in the shop's currency get
	// no offer.
	if !ok || from != "EUR" || card.Currency == from {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"dccInfo": map[string]any{"status": "NOT_AVAILABLE"}})
		return
	}
	major := float64(b.Amount.Value) / math.Pow10(money.Exponent(from))
	q := &quote{
		ID: randHex(16), BIN: b.Bin, Merchant: b.Amount.Value, MerchantCurrency: from,
		Cardholder: int64(math.Round(major * card.Rate * math.Pow10(money.Exponent(card.Currency)))),
		Currency:   card.Currency, Rate: card.Rate, Time: time.Now(),
	}
	s.mu.Lock()
	s.quotes[q.ID] = q
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"dccInfo": q.info(false, true)})
}

// acceptQuote checks the customer's answer to a quote in a direct card
// payment. It returns the quote, or a problem for the request.
func (s *Server) acceptQuote(b *paymentBody) (*quote, map[string][]string) {
	card := b.PaymentMethods.Card
	if card.DCC == nil {
		return nil, nil
	}
	if card.DCC.DCCRequestID == "" {
		if card.DCC.Selected {
			return nil, map[string][]string{"paymentMethods.card.dcc.dccRequestId": {"Required if selected is true."}}
		}
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	q, ok := s.quotes[card.DCC.DCCRequestID]
	switch {
	case !ok:
		return nil, map[string][]string{"paymentMethods.card.dcc.dccRequestId": {"Unknown or used DCC quote."}}
	case !strings.HasPrefix(card.Number, q.BIN) || q.Merchant != b.Amount.Value || q.MerchantCurrency != strings.ToUpper(b.Amount.Currency):
		return nil, map[string][]string{"paymentMethods.card.dcc.dccRequestId": {"The DCC quote is for another card or amount."}}
	}
	if card.DCC.Selected {
		delete(s.quotes, q.ID) // a quote pays once
	}
	return q, nil
}
