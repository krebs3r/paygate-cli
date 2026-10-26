package core

import (
	"strings"
	"testing"
	"time"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/paygate"
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

func TestBuildLinkRequest(t *testing.T) {
	prof := &config.Profile{Brand: "computop", Env: "test", MerchantID: "m"}
	before := time.Now()
	req, _, err := BuildPaymentRequest(PaymentInput{
		Flow: FlowLink, Methods: []string{"card,paypal"}, Amount: "49",
		Send: "email", Email: "customer@example.com", Name: "Max van Muster", Expires: "24h", Partial: true,
	}, prof)
	if err != nil {
		t.Fatal(err)
	}
	if req.ServiceType != "EMAIL" || req.URLs == nil || len(req.AllowedPaymentMethods) != 2 || !req.PartialPayment {
		t.Errorf("link request = %+v", req)
	}
	if ci := req.CustomerInfo; ci == nil || ci.Email != "customer@example.com" || ci.FirstName != "Max van" || ci.LastName != "Muster" {
		t.Errorf("customer = %+v", req.CustomerInfo)
	}
	exp, err := time.Parse(time.RFC3339, req.ExpirationTime)
	if err != nil || !strings.HasSuffix(req.ExpirationTime, "Z") || exp.Sub(before) < 23*time.Hour || exp.Sub(before) > 25*time.Hour {
		t.Errorf("expirationTime = %q", req.ExpirationTime)
	}
	if got := LinkRecipient(req); got != "customer@example.com" {
		t.Errorf("recipient = %q", got)
	}
	if req.ExternalTemplate != "" {
		t.Errorf("an e-mail without template uses Nexi's standard template, got %q", req.ExternalTemplate)
	}
	req, _, _ = BuildPaymentRequest(PaymentInput{Flow: FlowLink, Send: "email", Email: "customer@example.com", Template: " shop_mail_de "}, prof)
	if req.ExternalTemplate != "shop_mail_de" {
		t.Errorf("e-mail template = %q", req.ExternalTemplate)
	}

	// Without a delivery, Paygate's defaults apply and nothing extra is sent.
	req, _, err = BuildPaymentRequest(PaymentInput{Flow: FlowLink}, prof)
	if err != nil || req.ServiceType != "" || req.CustomerInfo != nil || req.ExpirationTime != "" || LinkRecipient(req) != "" {
		t.Errorf("plain link = %+v, %v", req, err)
	}

	req, _, err = BuildPaymentRequest(PaymentInput{Flow: FlowLink, Send: "sms", Phone: "+49 170 123-4567", Template: "shop_sms"}, prof)
	if err != nil || req.ServiceType != "SMS" || req.ExternalTemplate != "shop_sms" || *req.CustomerInfo.Phone != (paygate.Phone{CountryCode: "49", Number: "1701234567"}) {
		t.Errorf("sms link = %+v, %v", req.CustomerInfo, err)
	}
	if got := LinkRecipient(req); got != "+49 1701234567" {
		t.Errorf("sms recipient = %q", got)
	}

	for name, in := range map[string]PaymentInput{
		"email without address": {Send: "email"},
		"sms without number":    {Send: "sms", Email: "customer@example.com", Template: "shop_sms"},
		"sms without template":  {Send: "sms", Phone: "+49 170 1234567"},
		"template without send": {Template: "shop_mail_de"},
		"template too long":     {Send: "email", Email: "customer@example.com", Template: strings.Repeat("x", 33)},
		"bad address":           {Email: "customer at example.com"},
		"number without +":      {Phone: "0170 1234567"},
		"unknown delivery":      {Send: "fax"},
		"bad validity":          {Expires: "soon"},
		"too short validity":    {Expires: "10s"},
	} {
		in.Flow = FlowLink
		if _, _, err := BuildPaymentRequest(in, prof); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestWaitOutcome(t *testing.T) {
	linkRecord := &paygate.Payment{Status: "OK", ResponseCode: "00000000", Amount: &paygate.AmountDetails{}}
	if got := WaitOutcome(FlowLink, linkRecord); got != paygate.OutcomePending {
		t.Errorf("a link's own record must count as pending, got %v", got)
	}
	if got := WaitOutcome(FlowHosted, linkRecord); got != paygate.OutcomeSuccess {
		t.Errorf("other flows keep Paygate's outcome, got %v", got)
	}
	attempt := &paygate.Payment{Status: "FAILED", ResponseCode: "21151201", PaymentMethods: []byte(`{"type":"IDEAL"}`)}
	if got := WaitOutcome(FlowLink, attempt); got != paygate.OutcomeFailed {
		t.Errorf("a failed attempt ends the wait, got %v", got)
	}
}

func TestParseValidity(t *testing.T) {
	for in, want := range map[string]time.Duration{"": 0, "30m": 30 * time.Minute, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour} {
		if got, err := ParseValidity(in); err != nil || got != want {
			t.Errorf("ParseValidity(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"0d", "-1h", "1w", "d"} {
		if _, err := ParseValidity(in); err == nil {
			t.Errorf("ParseValidity(%q) should fail", in)
		}
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
	if req.BrowserInfo == nil || req.BrowserInfo.IPAddress == "" {
		t.Error("direct card payments must send browserInfo.ipAddress")
	}
	if r2, _, _ := BuildPaymentRequest(PaymentInput{Flow: FlowDirect, Card: "visa-ok", IP: "203.0.113.7"}, prof); r2.BrowserInfo.IPAddress != "203.0.113.7" {
		t.Errorf("--ip override ignored: %s", r2.BrowserInfo.IPAddress)
	}
	if _, _, err := BuildPaymentRequest(PaymentInput{Flow: FlowDirect, Card: "visa-ok", IP: "nope"}, prof); err == nil {
		t.Error("invalid IP must fail")
	}
	if label != "VISA •••• 1111" || req.PaymentMethods.Card.Number != "4111111111111111" || req.BrowserInfo == nil {
		t.Errorf("card request = %q %+v", label, req.PaymentMethods.Card)
	}
	if req.URLs != nil {
		t.Error("direct non-3DS payments need no callback URLs")
	}
	skips := func(in PaymentInput) bool {
		r, _, err := BuildPaymentRequest(in, prof)
		if err != nil {
			t.Fatal(err)
		}
		return r.PaymentMethods.Card.ThreeDsPolicy != nil && r.PaymentMethods.Card.ThreeDsPolicy.Skip == "THIS_TRANSACTION"
	}
	if !skips(PaymentInput{Flow: FlowDirect, Card: "mc-decline"}) {
		t.Error("test cards without 3DS must skip 3-D Secure")
	}
	if skips(PaymentInput{Flow: FlowDirect, Card: "visa-3ds-challenge"}) || skips(PaymentInput{Flow: FlowDirect, CardNumber: "4111111111111111"}) {
		t.Error("3DS cards and own cards request 3-D Secure unless asked to skip")
	}
	if !skips(PaymentInput{Flow: FlowDirect, CardNumber: "4111111111111111", Skip3DS: true}) {
		t.Error("Skip3DS must skip 3-D Secure")
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
