package paygate_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/krebs3r/paygate-cli/internal/mockpaygate"
	"github.com/krebs3r/paygate-cli/internal/paygate"
)

func newMock(t *testing.T) (*mockpaygate.Server, string) {
	t.Helper()
	m := mockpaygate.New("demo-shop", "demo-key")
	srv := httptest.NewServer(m.Handler())
	t.Cleanup(srv.Close)
	m.BaseURL = srv.URL
	return m, srv.URL
}

func cardRequest(number string) *paygate.PaymentRequest {
	return &paygate.PaymentRequest{
		TransID: "TID1",
		Amount:  paygate.Amount{Value: 1299, Currency: "EUR"},
		PaymentMethods: &paygate.PaymentMethods{Type: "CARD", IntegrationType: "DIRECT",
			Card: &paygate.Card{Number: number, Brand: "VISA", ExpiryDate: "203012", SecurityCode: "123"}},
	}
}

func TestDirectCardPayment(t *testing.T) {
	_, url := newMock(t)
	c := paygate.New(url, "demo-shop", "demo-key", paygate.AuthBasic)
	res, err := c.CreatePayment(context.Background(), cardRequest("4111111111111111"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Response.StatusCode != http.StatusCreated || paygate.OutcomeOf(res.Payment) != paygate.OutcomeSuccess {
		t.Fatalf("got %d / %+v", res.Response.StatusCode, res.Payment)
	}
	if res.Response.CorrelationID == "" {
		t.Error("correlation ID not captured")
	}
	if got := res.Payment.Method().Label(); got != "VISA •••• 1111" {
		t.Errorf("method label = %q", got)
	}
}

func TestDeclineIsAPIErrorWithPayment(t *testing.T) {
	_, url := newMock(t)
	c := paygate.New(url, "demo-shop", "demo-key", paygate.AuthBasic)
	_, err := c.CreatePayment(context.Background(), cardRequest("4012888888881881"))
	e, ok := paygate.AsAPIError(err)
	if !ok || e.StatusCode != 422 || e.Payment == nil {
		t.Fatalf("expected 422 APIError with payment, got %v", err)
	}
	if paygate.OutcomeOf(e.Payment) != paygate.OutcomeFailed {
		t.Errorf("outcome = %v", paygate.OutcomeOf(e.Payment))
	}
	if e.Payment.Method().ProviderResponseCode != "05" {
		t.Errorf("provider code missing: %+v", e.Payment.Method())
	}
}

func TestSessionRedirectAndPolling(t *testing.T) {
	m, url := newMock(t)
	m.NotFoundPolls, m.PendingPolls = 1, 1
	c := paygate.New(url, "demo-shop", "demo-key", paygate.AuthBasic)
	res, err := c.CreateSession(context.Background(), &paygate.PaymentRequest{
		TransID: "TIDHOSTED",
		Amount:  paygate.Amount{Value: 4900, Currency: "EUR"},
		URLs:    &paygate.URLs{Return: "https://x/r", Cancel: "https://x/c", Webhook: "https://x/w"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Response.StatusCode != http.StatusAccepted || !strings.HasPrefix(res.Payment.RedirectURL(), url+"/checkout") {
		t.Fatalf("unexpected session answer: %d %s", res.Response.StatusCode, res.Payment.RedirectURL())
	}
	_, err = c.GetByTransID(context.Background(), "TIDHOSTED")
	if !paygate.IsNotFound(err) {
		t.Fatalf("first poll should be not found, got %v", err)
	}
	r, err := c.GetByTransID(context.Background(), "TIDHOSTED")
	if err != nil || paygate.OutcomeOf(r.Payment) != paygate.OutcomePending {
		t.Fatalf("second poll should be pending, got %v %+v", err, r)
	}
	r, err = c.GetByTransID(context.Background(), "TIDHOSTED")
	if err != nil || paygate.OutcomeOf(r.Payment) != paygate.OutcomeSuccess {
		t.Fatalf("third poll should be paid, got %v %+v", err, r)
	}
}

func TestValidationProblem(t *testing.T) {
	_, url := newMock(t)
	c := paygate.New(url, "demo-shop", "demo-key", paygate.AuthBasic)
	req := cardRequest("4111111111111111")
	req.TransID = ""
	_, err := c.CreatePayment(context.Background(), req)
	e, ok := paygate.AsAPIError(err)
	if !ok || e.StatusCode != 400 || e.Problem == nil {
		t.Fatalf("expected problem details, got %v", err)
	}
	if fe := e.Problem.FieldErrors(); len(fe) != 1 || !strings.HasPrefix(fe[0], "transId:") {
		t.Errorf("field errors = %v", fe)
	}
}

func TestUnauthorizedAndVerify(t *testing.T) {
	_, url := newMock(t)
	bad := paygate.New(url, "demo-shop", "wrong", paygate.AuthBasic)
	if err := bad.Verify(context.Background()); !errors.Is(err, paygate.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
	good := paygate.New(url, "demo-shop", "demo-key", paygate.AuthBasic)
	if err := good.Verify(context.Background()); err != nil {
		t.Fatalf("verify: %v", err)
	}
	oauth := paygate.New(url, "demo-shop", "demo-key", paygate.AuthOAuth2)
	if err := oauth.Verify(context.Background()); err != nil {
		t.Fatalf("oauth verify: %v", err)
	}
	if _, err := oauth.CreatePayment(context.Background(), cardRequest("4111111111111111")); err != nil {
		t.Fatalf("oauth payment: %v", err)
	}
	badOAuth := paygate.New(url, "demo-shop", "wrong", paygate.AuthOAuth2)
	if err := badOAuth.Verify(context.Background()); !errors.Is(err, paygate.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for oauth, got %v", err)
	}
}

func TestRefundFlow(t *testing.T) {
	_, url := newMock(t)
	c := paygate.New(url, "demo-shop", "demo-key", paygate.AuthBasic)
	res, err := c.CreatePayment(context.Background(), cardRequest("4111111111111111"))
	if err != nil {
		t.Fatal(err)
	}
	payID := res.Payment.PayID
	op := &paygate.OperationRequest{TransID: "TID1", Amount: paygate.Amount{Value: 500, Currency: "EUR"}}
	if _, err := c.Operate(context.Background(), paygate.OpRefund, payID, op, "k1"); err != nil {
		t.Fatalf("refund: %v", err)
	}
	details, err := c.GetByPayID(context.Background(), payID)
	if err != nil {
		t.Fatal(err)
	}
	if details.Payment.Amount.RefundedValue != 500 {
		t.Errorf("refunded = %d", details.Payment.Amount.RefundedValue)
	}
	op.Amount.Value = 5000
	if _, err := c.Operate(context.Background(), paygate.OpRefund, payID, op, "k2"); err == nil {
		t.Error("over-refund should fail")
	}
}

func TestLoggingTransportRedacts(t *testing.T) {
	_, url := newMock(t)
	var log bytes.Buffer
	c := paygate.New(url, "demo-shop", "demo-key", paygate.AuthOAuth2)
	c.HTTP.Transport = &paygate.LoggingTransport{Level: 2, Out: &log}
	if _, err := c.CreatePayment(context.Background(), cardRequest("4111111111111111")); err != nil {
		t.Fatal(err)
	}
	out := log.String()
	for _, secret := range []string{"demo-key", "mock-access-token"} {
		if strings.Contains(out, secret) {
			t.Errorf("verbose log leaks %q:\n%s", secret, out)
		}
	}
	if !strings.Contains(out, "Authorization: Bearer [REDACTED]") || !strings.Contains(out, "client_secret=%5BREDACTED%5D") {
		t.Errorf("expected redaction markers in log:\n%s", out)
	}
}
