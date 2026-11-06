package mockpaygate_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/krebs3r/paygate-cli/internal/cards"
	"github.com/krebs3r/paygate-cli/internal/mockpaygate"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/webhooks"
)

type received struct {
	header http.Header
	body   []byte
}

func TestHostedPaymentSendsSignedWebhook(t *testing.T) {
	got := make(chan received, 1)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- received{r.Header.Clone(), b}
	}))
	defer hook.Close()

	m := mockpaygate.New("demo-shop", "demo-key")
	m.Webhook, m.HMACKey = "enhanced", "demo-hmac-key"
	srv := httptest.NewServer(m.Handler())
	defer srv.Close()
	m.BaseURL = srv.URL

	c := paygate.New(srv.URL, "demo-shop", "demo-key", paygate.AuthBasic)
	ctx := context.Background()
	req := &paygate.PaymentRequest{TransID: "TID1", Amount: paygate.Amount{Value: 4990, Currency: "EUR"},
		URLs: &paygate.URLs{Return: "https://r", Cancel: "https://c", Webhook: hook.URL + "/paygate/webhook"}}
	if _, err := c.CreateSession(ctx, req); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ { // not found, pending, then OK
		_, _ = c.GetByTransID(ctx, "TID1")
	}

	select {
	case r := <-got:
		check := webhooks.Verify(r.header, r.body, "demo-hmac-key", time.Now())
		if check.Kind != webhooks.Enhanced || !check.Valid || check.Failed() {
			t.Errorf("webhook does not verify: %+v\n%s", check.Findings, r.body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no webhook arrived")
	}

	_, _ = c.GetByTransID(ctx, "TID1")
	select {
	case <-got:
		t.Error("a second lookup sent the webhook again")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestDCCQuoteAndPayment(t *testing.T) {
	m := mockpaygate.New("demo-shop", "demo-key")
	srv := httptest.NewServer(m.Handler())
	defer srv.Close()
	c := paygate.New(srv.URL, "demo-shop", "demo-key", paygate.AuthBasic)
	ctx := context.Background()

	b, err := c.CheckBin(ctx, "4176662280")
	if err != nil || b.Lookup.Country() != "USA" {
		t.Fatalf("bin check: %+v, %v", b, err)
	}
	amount := paygate.Amount{Value: 1299, Currency: "EUR"}
	q, err := c.QuoteDCC(ctx, &paygate.DCCQuoteRequest{Bin: "4176662280", Amount: amount})
	if err != nil || !q.Info().Available() || q.Info().Amounts.Cardholder.Value != 1452 || q.Info().Amounts.Cardholder.Currency != "USD" {
		t.Fatalf("quote: %+v, %v", q.Info(), err)
	}
	if q, err := c.QuoteDCC(ctx, &paygate.DCCQuoteRequest{Bin: "4176662220", Amount: amount}); err != nil || q.Info().Available() {
		t.Errorf("a euro card gets no offer: %+v, %v", q.Info(), err)
	}

	pay := func(transID, number string, dcc *paygate.DCCDecision) (*paygate.Result, error) {
		return c.CreatePayment(ctx, &paygate.PaymentRequest{
			TransID: transID, Amount: amount, BrowserInfo: &paygate.BrowserInfo{IPAddress: "127.0.0.1"},
			PaymentMethods: &paygate.PaymentMethods{Type: "CARD", IntegrationType: "DIRECT", Card: &paygate.Card{
				Number: number, Brand: "VISA", ThreeDsPolicy: &paygate.ThreeDsPolicy{Skip: "THIS_TRANSACTION"}, DCC: dcc,
			}},
		})
	}
	accept := &paygate.DCCDecision{Selected: true, DCCRequestID: q.Info().DCCRequestID}
	if _, err := pay("TWRONG", "4111111111111111", accept); err == nil {
		t.Error("a quote for another card must be refused")
	}
	created, err := pay("TDCC", "4176662280613305", accept)
	if err != nil {
		t.Fatal(err)
	}
	if created.Payment.Method().DCC != nil {
		t.Error("like the spec, the answer to the payment carries no dccInfo; the details do")
	}
	res, err := c.GetByTransID(ctx, "TDCC")
	if err != nil {
		t.Fatal(err)
	}
	if d := res.Payment.Method().DCC; d == nil || !d.Selected || d.Amounts.Cardholder.Value != 1452 || d.Rate.ExchangeRate.String() != "1.1179" {
		t.Errorf("payment dccInfo = %+v", d)
	}
	if _, err := pay("TAGAIN", "4176662280613305", accept); err == nil {
		t.Error("a quote pays once")
	}
}

func TestMockKnowsEveryDCCTestCard(t *testing.T) {
	m := mockpaygate.New("demo-shop", "demo-key")
	srv := httptest.NewServer(m.Handler())
	defer srv.Close()
	c := paygate.New(srv.URL, "demo-shop", "demo-key", paygate.AuthBasic)
	for _, card := range cards.DCCTestCards {
		q, err := c.QuoteDCC(context.Background(), &paygate.DCCQuoteRequest{Bin: card.BIN, Amount: paygate.Amount{Value: 1299, Currency: "EUR"}})
		if err != nil {
			t.Fatalf("%s: %v", card.BIN, err)
		}
		if card.Currency == "EUR" {
			if q.Info().Available() {
				t.Errorf("%s: a euro card gets no offer", card.BIN)
			}
		} else if !q.Info().Available() || q.Info().Amounts.Cardholder.Currency != card.Currency {
			t.Errorf("%s: want an offer in %s, got %+v", card.BIN, card.Currency, q.Info())
		}
	}
}
