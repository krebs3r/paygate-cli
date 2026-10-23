package mockpaygate_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
