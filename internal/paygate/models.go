package paygate

import (
	"encoding/json"
	"strings"
)

// Amount is always expressed in minor units (cents).
type Amount struct {
	Value    int64  `json:"value"`
	Currency string `json:"currency"`
}

// AmountDetails is returned by the payment details endpoints.
type AmountDetails struct {
	Value         int64  `json:"value"`
	Currency      string `json:"currency"`
	CapturedValue int64  `json:"capturedValue,omitempty"`
	RefundedValue int64  `json:"refundedValue,omitempty"`
	CapturedKnown bool   `json:"-"`
	RefundedKnown bool   `json:"-"`
}

// UnmarshalJSON records whether captured/refunded values were present, so
// follow-up operations can tell "0 captured" from "not reported".
func (a *AmountDetails) UnmarshalJSON(b []byte) error {
	type plain AmountDetails
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(b, &keys)
	*a = AmountDetails(p)
	_, a.CapturedKnown = keys["capturedValue"]
	_, a.RefundedKnown = keys["refundedValue"]
	return nil
}

type URLs struct {
	Return  string `json:"return,omitempty"`
	Cancel  string `json:"cancel,omitempty"`
	Webhook string `json:"webhook,omitempty"`
}

type Order struct {
	MerchantReference string `json:"merchantReference,omitempty"`
	Description       string `json:"description,omitempty"`
}

type CaptureMethod struct {
	Type string `json:"type"` // AUTOMATIC | MANUAL
}

type BrowserInfo struct {
	AcceptHeaders     string `json:"acceptHeaders,omitempty"`
	IPAddress         string `json:"ipAddress,omitempty"`
	JavaEnabled       bool   `json:"javaEnabled"`
	JavaScriptEnabled bool   `json:"javaScriptEnabled"`
	Language          string `json:"language,omitempty"`
	ColorDepth        int    `json:"colorDepth,omitempty"`
	ScreenHeight      int    `json:"screenHeight,omitempty"`
	ScreenWidth       int    `json:"screenWidth,omitempty"`
	TimeZoneOffset    string `json:"timeZoneOffset,omitempty"`
	UserAgent         string `json:"userAgent,omitempty"`
}

type ThreeDsPolicy struct {
	Skip string `json:"skip,omitempty"` // THIS_TRANSACTION | OUT_OF_SCOPE | DATA_ONLY
}

type Card struct {
	Number         string         `json:"number,omitempty"`
	CardHolderName string         `json:"cardHolderName,omitempty"`
	ExpiryDate     string         `json:"expiryDate,omitempty"` // YYYYMM
	SecurityCode   string         `json:"securityCode,omitempty"`
	Brand          string         `json:"brand,omitempty"`
	SubType        []string       `json:"subType,omitempty"`
	ThreeDsPolicy  *ThreeDsPolicy `json:"threeDsPolicy,omitempty"`
}

type SubTyped struct {
	SubType string `json:"subType,omitempty"`
}

// PaymentMethods selects the method and integration type. Only the
// sub-objects the CLI sends are modelled.
type PaymentMethods struct {
	Type            string    `json:"type,omitempty"`
	IntegrationType string    `json:"integrationType,omitempty"` // HOSTED | DIRECT
	Card            *Card     `json:"card,omitempty"`
	Ideal           *SubTyped `json:"ideal,omitempty"`
}

// PaymentRequest is the body of POST /payments and POST /payments/sessions.
type PaymentRequest struct {
	TransID               string          `json:"transId"`
	RefNr                 string          `json:"refNr,omitempty"`
	Amount                Amount          `json:"amount"`
	Language              string          `json:"language,omitempty"`
	CaptureMethod         *CaptureMethod  `json:"captureMethod,omitempty"`
	Channel               string          `json:"channel,omitempty"`
	Order                 *Order          `json:"order,omitempty"`
	URLs                  *URLs           `json:"urls,omitempty"`
	BrowserInfo           *BrowserInfo    `json:"browserInfo,omitempty"`
	AllowedPaymentMethods []string        `json:"allowedPaymentMethods,omitempty"`
	PaymentMethods        *PaymentMethods `json:"paymentMethods,omitempty"`
}

// OperationRequest is the body of captures, refunds and reversals.
type OperationRequest struct {
	TransID string `json:"transId"`
	Amount  Amount `json:"amount"`
	RefNr   string `json:"refNr,omitempty"`
}

type Link struct {
	Href string `json:"href"`
	Type string `json:"type,omitempty"`
}

type Links struct {
	Redirect *Link `json:"redirect,omitempty"`
}

// Payment is the union of the fields the CLI reads from payment responses.
type Payment struct {
	MerchantID          string          `json:"merchantId,omitempty"`
	PayID               string          `json:"payId,omitempty"`
	XID                 string          `json:"xId,omitempty"`
	TransID             string          `json:"transId,omitempty"`
	RefNr               string          `json:"refNr,omitempty"`
	Status              string          `json:"status,omitempty"`
	ResponseCode        Code            `json:"responseCode,omitempty"`
	ResponseDescription string          `json:"responseDescription,omitempty"`
	Amount              *AmountDetails  `json:"amount,omitempty"`
	Channel             string          `json:"channel,omitempty"`
	Order               *Order          `json:"order,omitempty"`
	PaymentMethods      json.RawMessage `json:"paymentMethods,omitempty"`
	Links               *Links          `json:"_links,omitempty"`
}

// Code is a response code. Paygate sends it as a string ("00000000") but some
// error bodies use a number (422), so both are accepted.
type Code string

func (c *Code) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		*c = ""
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*c = Code(v)
		return nil
	}
	*c = Code(s)
	return nil
}

// RedirectURL returns the hosted page / challenge URL, if any.
func (p *Payment) RedirectURL() string {
	if p == nil || p.Links == nil || p.Links.Redirect == nil {
		return ""
	}
	return p.Links.Redirect.Href
}

// MethodInfo is a flattened view of the paymentMethods response object.
type MethodInfo struct {
	Type                    string
	Brand                   string
	Last4                   string
	PseudoCardNumber        string
	ProviderResponseCode    string
	ProviderResponseMessage string
	IssuerResponseCode      string
	IssuerResponseMessage   string
}

// Method extracts the payment method summary. Paygate returns either an
// object ({"type":"CARD","card":{...}}) or, in webhooks, an array.
func (p *Payment) Method() MethodInfo {
	var mi MethodInfo
	if p == nil || len(p.PaymentMethods) == 0 {
		return mi
	}
	raw := p.PaymentMethods
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "[") {
		var arr []json.RawMessage
		if json.Unmarshal(raw, &arr) != nil || len(arr) == 0 {
			return mi
		}
		raw = arr[0]
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return mi
	}
	if t, ok := obj["type"]; ok {
		_ = json.Unmarshal(t, &mi.Type)
	}
	for key, val := range obj {
		if key == "type" || key == "integrationType" {
			continue
		}
		var sub map[string]any
		if json.Unmarshal(val, &sub) != nil {
			continue
		}
		if mi.Type == "" {
			mi.Type = strings.ToUpper(key)
		}
		str := func(k string) string {
			if v, ok := sub[k].(string); ok {
				return v
			}
			return ""
		}
		mi.Brand = str("brand")
		mi.Last4 = str("last4Digits")
		mi.PseudoCardNumber = str("pseudoCardNumber")
		mi.ProviderResponseCode = str("providerResponseCode")
		mi.ProviderResponseMessage = str("providerResponseMessage")
		mi.IssuerResponseCode = str("issuerResponseCode")
		mi.IssuerResponseMessage = str("issuerResponseMessage")
		break
	}
	if mi.Last4 == "" && len(mi.PseudoCardNumber) >= 4 {
		mi.Last4 = mi.PseudoCardNumber[len(mi.PseudoCardNumber)-4:]
	}
	return mi
}

// Label renders "VISA •••• 1111", "PAYPAL" etc.
func (m MethodInfo) Label() string {
	switch {
	case m.Brand != "" && m.Last4 != "":
		return strings.ToUpper(m.Brand) + " •••• " + m.Last4
	case m.Brand != "":
		return strings.ToUpper(m.Brand)
	default:
		return m.Type
	}
}
