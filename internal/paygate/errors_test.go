package paygate

import (
	"encoding/json"
	"testing"
)

func TestClassifyCode(t *testing.T) {
	tests := map[Code]CodeClass{
		"00000000": CodeOK,
		"20600061": CodeError,
		"40000000": CodeFatal,
		"60000000": CodePending,
		"70000001": Code3DS,
		"422":      CodeError,
		"200":      CodeOK,
		"":         CodeUnknown,
	}
	for code, want := range tests {
		if got := ClassifyCode(code); got != want {
			t.Errorf("ClassifyCode(%q) = %v, want %v", code, got, want)
		}
	}
}

func TestOutcomeOf(t *testing.T) {
	tests := []struct {
		status string
		code   Code
		want   Outcome
	}{
		{"OK", "00000000", OutcomeSuccess},
		{"AUTHORIZED", "00000000", OutcomeSuccess},
		{"FAILED", "422", OutcomeFailed},
		{"FAILED", "20600061", OutcomeFailed},
		{"AUTHORIZE_REQUEST", "60000000", OutcomePending},
		{"OK", "70000001", OutcomePending},
		{"REQUEST", "0", OutcomePending},
		{"SOMETHING_NEW", "00000000", OutcomeSuccess},
		{"SOMETHING_NEW", "21000100", OutcomeFailed},
	}
	for _, tt := range tests {
		p := &Payment{Status: tt.status, ResponseCode: tt.code}
		if got := OutcomeOf(p); got != tt.want {
			t.Errorf("OutcomeOf(%s, %s) = %v, want %v", tt.status, tt.code, got, tt.want)
		}
	}
}

func TestPaymentDecoding(t *testing.T) {
	raw := `{"payId":"p1","status":"FAILED","responseCode":422,
	  "amount":{"value":1000,"currency":"EUR","capturedValue":800},
	  "paymentMethods":{"card":{"brand":"VISA","pseudoCardNumber":"0723219245645111",
	    "providerResponseCode":"05","providerResponseMessage":"Do not honor"}}}`
	var p Payment
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	if p.ResponseCode != "422" {
		t.Errorf("numeric responseCode decoded as %q", p.ResponseCode)
	}
	if !p.Amount.CapturedKnown || p.Amount.RefundedKnown {
		t.Errorf("captured/refunded presence = %v/%v", p.Amount.CapturedKnown, p.Amount.RefundedKnown)
	}
	m := p.Method()
	if m.Label() != "VISA •••• 5111" || m.ProviderResponseCode != "05" {
		t.Errorf("method = %+v (label %q)", m, m.Label())
	}

	var webhook Payment
	_ = json.Unmarshal([]byte(`{"paymentMethods":[{"type":"PAYPAL"}]}`), &webhook)
	if got := webhook.Method().Label(); got != "PAYPAL" {
		t.Errorf("array paymentMethods label = %q", got)
	}
}
