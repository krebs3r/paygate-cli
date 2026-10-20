package core

import (
	"testing"

	"github.com/krebs3r/paygate-cli/internal/config"
)

func TestSimulationDescription(t *testing.T) {
	tests := map[string]string{
		"": "", "off": "", "ok": "Test:0000", "success": "Test:0000", "0000": "Test:0000",
		"decline": "Test:0305", "0305": "Test:0305", "1234": "Test:1234", "Test:0100": "Test:0100",
	}
	for in, want := range tests {
		got, err := SimulationDescription(in)
		if err != nil || got != want {
			t.Errorf("SimulationDescription(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := SimulationDescription("maybe"); err == nil {
		t.Error("unknown simulation must fail")
	}
}

func TestBuildPaymentRequest(t *testing.T) {
	prof := &config.Profile{Brand: "nexi", Env: "prod", MerchantID: "m", Currency: "chf"}

	req, label, err := BuildPaymentRequest(PaymentInput{Flow: FlowDirect, Card: "visa-ok", Amount: "1,50", Simulate: "ok"}, prof)
	if err != nil {
		t.Fatal(err)
	}
	if req.Amount.Value != 150 || req.Amount.Currency != "CHF" {
		t.Errorf("amount = %+v", req.Amount)
	}
	if req.Order.Description != SimSuccess {
		t.Errorf("simulation must replace the description, got %q", req.Order.Description)
	}
	if label != "VISA •••• 1111" || req.PaymentMethods.Card.Number != "4111111111111111" || req.BrowserInfo == nil {
		t.Errorf("card request = %q %+v", label, req.PaymentMethods.Card)
	}
	if req.URLs != nil {
		t.Error("direct non-3DS payments need no callback URLs")
	}

	req, _, err = BuildPaymentRequest(PaymentInput{Flow: FlowHosted, Methods: []string{"card,paypal", "sepa"}}, prof)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.AllowedPaymentMethods) != 3 || req.AllowedPaymentMethods[2] != "DIRECTDEBIT" || req.URLs == nil {
		t.Errorf("hosted request = %+v urls %+v", req.AllowedPaymentMethods, req.URLs)
	}
	if req.Amount.Value != 1299 || req.Order.Description != DefaultDescription {
		t.Errorf("defaults: %+v %q", req.Amount, req.Order.Description)
	}

	req, label, err = BuildPaymentRequest(PaymentInput{Flow: FlowMethod, Method: "ideal", Capture: "manual"}, prof)
	if err != nil || label != "iDEAL" || req.PaymentMethods.Ideal == nil || req.CaptureMethod.Type != "MANUAL" {
		t.Errorf("ideal request = %q %+v %v", label, req, err)
	}

	if _, _, err := BuildPaymentRequest(PaymentInput{Flow: FlowDirect}, prof); err == nil {
		t.Error("direct payment without card must fail")
	}
	if _, _, err := BuildPaymentRequest(PaymentInput{Flow: FlowDirect, Card: "visa-ok", Simulate: "x"}, prof); err == nil {
		t.Error("invalid simulation must fail")
	}
}
