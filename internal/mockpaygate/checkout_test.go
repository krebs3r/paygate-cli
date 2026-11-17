package mockpaygate_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/krebs3r/paygate-cli/internal/mockpaygate"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/webhooks"
)

// askMock is a mock whose checkout page asks the customer, like the demo.
func askMock(t *testing.T) (*mockpaygate.Server, *paygate.Client, string) {
	t.Helper()
	m := mockpaygate.New("demo-shop", "demo-key")
	m.Checkout = mockpaygate.CheckoutAsk
	m.Webhook, m.HMACKey, m.LocalWebhooksOnly = "enhanced", "demo-hmac-key", true
	srv := httptest.NewServer(m.Handler())
	t.Cleanup(srv.Close)
	m.BaseURL = srv.URL
	return m, paygate.New(srv.URL, "demo-shop", "demo-key", paygate.AuthBasic), srv.URL
}

var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func answer(t *testing.T, base, transID, action, method string) *http.Response {
	t.Helper()
	resp, err := noRedirect.PostForm(base+"/checkout", url.Values{"transId": {transID}, "action": {action}, "method": {method}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func TestAskCheckoutPays(t *testing.T) {
	got := make(chan received, 1)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- received{r.Header.Clone(), b}
	}))
	defer hook.Close()
	_, c, base := askMock(t)
	ctx := context.Background()

	res, err := c.CreateSession(ctx, &paygate.PaymentRequest{TransID: "TASK", Amount: paygate.Amount{Value: 4990, Currency: "EUR"},
		URLs: &paygate.URLs{Return: "https://shop.example/ok", Cancel: "https://shop.example/stop", Webhook: hook.URL + "/hook"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetByTransID(ctx, "TASK"); err == nil {
		t.Fatal("before the customer opens the checkout, the session is unknown")
	}

	page, err := http.Get(res.Payment.RedirectURL())
	if err != nil {
		t.Fatal(err)
	}
	html := body(t, page)
	page.Body.Close()
	for _, want := range []string{"Demo checkout", "49.90", `value="pay"`, `value="decline"`, `value="cancel"`, "DIRECTDEBIT", "https://shop.example/stop"} {
		if !strings.Contains(html, want) {
			t.Errorf("checkout page misses %q", want)
		}
	}
	if strings.Contains(html, "https://fonts.") || strings.Contains(html, "<script") {
		t.Error("the checkout page must load nothing from elsewhere")
	}
	if r, _ := c.GetByTransID(ctx, "TASK"); r == nil || r.Payment.Status != "AUTHORIZE_REQUEST" {
		t.Fatalf("an opened checkout waits for the customer, got %+v", r)
	}

	resp := answer(t, base, "TASK", "pay", "CARD")
	result := body(t, resp)
	if resp.StatusCode != 200 || !strings.Contains(result, "Payment successful") || !strings.Contains(result, "https://shop.example/ok") {
		t.Fatalf("a return URL elsewhere is shown, not followed: %d\n%s", resp.StatusCode, result)
	}
	r, err := c.GetByTransID(ctx, "TASK")
	if err != nil || r.Payment.Status != "OK" || r.Payment.Method().Type != "CARD" || r.Payment.Amount.CapturedValue != 4990 {
		t.Fatalf("paid session = %+v, %v", r, err)
	}
	select {
	case w := <-got:
		if check := webhooks.Verify(w.header, w.body, "demo-hmac-key", time.Now()); !check.Valid {
			t.Errorf("webhook does not verify: %+v", check.Findings)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no webhook to the local address")
	}
	if again := answer(t, base, "TASK", "decline", ""); !strings.Contains(body(t, again), "Payment successful") {
		t.Error("a second answer must not change a final payment")
	}
}

func TestAskCheckoutDeclinesAndCancels(t *testing.T) {
	m, c, base := askMock(t)
	ctx := context.Background()
	hooks := make(chan string, 4)
	m.OnWebhook = func(url string, status int, err error) { hooks <- url }
	shop := httptest.NewServer(http.NotFoundHandler())
	defer shop.Close()

	for _, id := range []string{"TDEC", "TCAN"} {
		if _, err := c.CreateSession(ctx, &paygate.PaymentRequest{TransID: id, Amount: paygate.Amount{Value: 100, Currency: "EUR"},
			URLs: &paygate.URLs{Return: "https://shop.example/ok", Cancel: shop.URL + "/cancel", Webhook: "https://webhook.invalid/x"}}); err != nil {
			t.Fatal(err)
		}
		if resp, err := http.Get(base + "/checkout?transId=" + id); err == nil {
			resp.Body.Close()
		}
	}

	if resp := answer(t, base, "TDEC", "decline", ""); !strings.Contains(body(t, resp), "Payment declined") {
		t.Error("decline page")
	}
	if r, _ := c.GetByTransID(ctx, "TDEC"); r == nil || r.Payment.Status != "FAILED" || paygate.OutcomeOf(r.Payment) != paygate.OutcomeFailed {
		t.Errorf("declined = %+v", r)
	}

	resp := answer(t, base, "TCAN", "cancel", "")
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != shop.URL+"/cancel" {
		t.Errorf("a local cancel URL is followed: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if r, _ := c.GetByTransID(ctx, "TCAN"); r == nil || paygate.OutcomeOf(r.Payment) != paygate.OutcomeFailed {
		t.Errorf("cancelled = %+v", r)
	}

	select {
	case u := <-hooks:
		t.Errorf("a webhook to %s left this computer", u)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestAskCheckoutPaymentLink(t *testing.T) {
	_, c, base := askMock(t)
	ctx := context.Background()
	res, err := c.CreateLink(ctx, &paygate.PaymentRequest{TransID: "TLINK", Amount: paygate.Amount{Value: 100, Currency: "EUR"}, ServiceType: "LINK",
		URLs: &paygate.URLs{Return: "https://shop.example/ok", Cancel: "https://shop.example/stop", Webhook: "https://webhook.invalid/x"}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := c.GetByTransID(ctx, "TLINK")
	if err != nil || before.Payment.Method().Type != "" {
		t.Fatalf("an unopened link answers with its own record: %+v, %v", before, err)
	}
	if resp, err := http.Get(res.Payment.RedirectURL()); err == nil {
		resp.Body.Close()
	}
	answer(t, base, "TLINK", "pay", "PAYPAL")
	after, err := c.GetByTransID(ctx, "TLINK")
	if err != nil || after.Payment.Status != "OK" || after.Payment.PayID == before.Payment.PayID || after.Payment.Method().Type != "PAYPAL" {
		t.Fatalf("the customer's attempt is a payment of its own: %+v, %v", after, err)
	}
}

func TestSnapshotRestore(t *testing.T) {
	m1 := mockpaygate.New("demo-shop", "demo-key")
	srv1 := httptest.NewServer(m1.Handler())
	defer srv1.Close()
	c1 := paygate.New(srv1.URL, "demo-shop", "demo-key", paygate.AuthBasic)
	ctx := context.Background()
	created, err := c1.CreatePayment(ctx, &paygate.PaymentRequest{
		TransID: "TSNAP", Amount: paygate.Amount{Value: 2500, Currency: "EUR"}, BrowserInfo: &paygate.BrowserInfo{IPAddress: "127.0.0.1"},
		CaptureMethod: &paygate.CaptureMethod{Type: "MANUAL"},
		PaymentMethods: &paygate.PaymentMethods{Type: "CARD", IntegrationType: "DIRECT", Card: &paygate.Card{
			Number: "4111111111111111", Brand: "VISA", ThreeDsPolicy: &paygate.ThreeDsPolicy{Skip: "THIS_TRANSACTION"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := m1.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(snap), "4111111111111111") || strings.Contains(string(snap), "demo-key") {
		t.Error("the snapshot must not hold card numbers or keys")
	}

	m2 := mockpaygate.New("demo-shop", "demo-key")
	if err := m2.Restore(snap); err != nil {
		t.Fatal(err)
	}
	srv2 := httptest.NewServer(m2.Handler())
	defer srv2.Close()
	c2 := paygate.New(srv2.URL, "demo-shop", "demo-key", paygate.AuthBasic)
	payID := created.Payment.PayID
	if _, err := c2.Operate(ctx, paygate.OpCapture, payID, &paygate.OperationRequest{TransID: "TSNAP", Amount: paygate.Amount{Value: 2500, Currency: "EUR"}}, ""); err != nil {
		t.Fatalf("capture after restore: %v", err)
	}
	r, err := c2.GetByPayID(ctx, payID)
	if err != nil || r.Payment.Amount.CapturedValue != 2500 {
		t.Fatalf("restored payment = %+v, %v", r, err)
	}
	m2.Clear()
	if _, err := c2.GetByPayID(ctx, payID); err == nil {
		t.Error("Clear should forget the payments")
	}
}
