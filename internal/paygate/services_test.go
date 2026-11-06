package paygate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDecimal(t *testing.T) {
	for in, want := range map[string]string{
		`1.2313`: "1.2313", `"1,2313"`: "1.2313", `"3.5"`: "3.5", `3`: "3", `null`: "", `"1,234.5"`: "1,234.5",
	} {
		var d Decimal
		if err := json.Unmarshal([]byte(in), &d); err != nil || d.String() != want {
			t.Errorf("Decimal(%s) = %q, %v; want %q", in, d.String(), err, want)
		}
	}
}

func TestQuoteDCC(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req DCCQuoteRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Bin {
		case "4176662280":
			_, _ = w.Write([]byte(`{"dccInfo":{"status":"AVAILABLE","provider":"Nets","dccRequestId":"q1",
				"rate":{"exchangeRate":"1,1179","markupPercentage":3.5,"timestamp":"20261030"},
				"amounts":{"merchant":{"value":1299,"currency":"EUR"},"cardholder":{"value":1452,"currency":"USD","decimalValue":14.52}}}}`))
		case "4176662220":
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"dccInfo":{"status":"NOT_AVAILABLE"}}`))
		default:
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"responseCode":"20000083","responseDescription":"system error"}`))
		}
	}))
	defer srv.Close()
	c := &Client{Origin: srv.URL, HTTP: srv.Client(), Auth: AuthBasic, MerchantID: "m", APIKey: "k"}

	res, err := c.QuoteDCC(context.Background(), &DCCQuoteRequest{Bin: "4176662280", Amount: Amount{Value: 1299, Currency: "EUR"}})
	if err != nil || !res.Info().Available() {
		t.Fatalf("offer: %+v, %v", res, err)
	}
	if i := res.Info(); i.Rate.ExchangeRate.String() != "1.1179" || i.Rate.MarkupPercentage.String() != "3.5" ||
		i.Amounts.Cardholder.Value != 1452 || i.Amounts.Cardholder.DecimalValue.String() != "14.52" {
		t.Errorf("offer = %+v", i)
	}
	res, err = c.QuoteDCC(context.Background(), &DCCQuoteRequest{Bin: "4176662220", Amount: Amount{Value: 1299, Currency: "EUR"}})
	if err != nil || res.Info() == nil || res.Info().Available() {
		t.Errorf("no offer must be a result: %+v, %v", res, err)
	}
	if _, err = c.QuoteDCC(context.Background(), &DCCQuoteRequest{Bin: "1234567890", Amount: Amount{Value: 1, Currency: "EUR"}}); err == nil {
		t.Error("a 422 without dccInfo is an error")
	}
}

func TestMethodReadsDCCInfo(t *testing.T) {
	p := &Payment{PaymentMethods: json.RawMessage(`{"type":"CARD","card":{"brand":"VISA","last4Digits":"3305",
		"dccInfo":{"dccSelected":true,"provider":"Nets","rate":{"exchangeRate":1.1179},"amounts":{"cardholder":{"value":1452,"currency":"USD"}}}}}`)}
	mi := p.Method()
	if mi.DCC == nil || !mi.DCC.Selected || mi.DCC.Amounts.Cardholder.Currency != "USD" || mi.Last4 != "3305" {
		t.Errorf("method = %+v, dcc %+v", mi, mi.DCC)
	}
}
