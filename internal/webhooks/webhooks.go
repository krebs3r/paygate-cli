// Package webhooks builds, signs and checks Paygate webhooks as described in
// the public REST API documentation: the Basic webhook carries only the
// payId, the Enhanced webhook the payment result plus an HMAC signature.
package webhooks

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"strconv"
	"strings"
	"time"

	"github.com/krebs3r/paygate-cli/internal/paygate"
)

// Signature headers of the Enhanced webhook.
const (
	HeaderVersion   = "X-Paygate-Signature-Version"
	HeaderTimestamp = "X-Paygate-Timestamp"
	HeaderSignature = "X-Paygate-Signature"
)

// Tolerance is how far the timestamp may be off before a receiver has to
// reject the webhook.
const Tolerance = 5 * time.Minute

// Kind is the type of a webhook.
type Kind string

const (
	Basic    Kind = "BASIC"
	Enhanced Kind = "ENHANCED"
	Unknown  Kind = "UNKNOWN"
)

// Payload is the body of an Enhanced webhook.
type Payload struct {
	PayID               string   `json:"payId"`
	TransID             string   `json:"transId"`
	RefNr               *string  `json:"refNr"`
	Status              string   `json:"status"`
	ResponseCode        string   `json:"responseCode"`
	ResponseDescription string   `json:"responseDescription"`
	Amount              Amount   `json:"amount"`
	PaymentMethods      []Method `json:"paymentMethods"`
	CreationDate        string   `json:"creationDate"`
}

// Amount is in minor units.
type Amount struct {
	Value    int64  `json:"value"`
	Currency string `json:"currency"`
}

// Method names the payment method of a webhook.
type Method struct {
	Type string `json:"type"`
}

// FromPayment fills an Enhanced payload from a payment as returned by GET.
// The GET answer has no creation date, so the caller passes it.
func FromPayment(p *paygate.Payment, created time.Time) Payload {
	pl := Payload{
		PayID:               p.PayID,
		TransID:             p.TransID,
		Status:              p.Status,
		ResponseCode:        string(p.ResponseCode),
		ResponseDescription: p.ResponseDescription,
		PaymentMethods:      []Method{},
		CreationDate:        created.UTC().Format(time.RFC3339),
	}
	if p.RefNr != "" {
		ref := p.RefNr
		pl.RefNr = &ref
	}
	if p.Amount != nil {
		pl.Amount = Amount{Value: p.Amount.Value, Currency: p.Amount.Currency}
	}
	if t := p.Method().Type; t != "" {
		pl.PaymentMethods = append(pl.PaymentMethods, Method{Type: strings.ToUpper(t)})
	}
	return pl
}

// Message is a webhook ready to send: the exact body and its signature.
type Message struct {
	Kind      Kind
	Body      []byte
	Timestamp int64  // Enhanced only
	Signature string // "v1=<hex>", Enhanced only
}

// Headers lists the request headers in a stable order.
func (m Message) Headers() [][2]string {
	h := [][2]string{{"Content-Type", "application/json"}}
	if m.Signature != "" {
		h = append(h,
			[2]string{HeaderVersion, "v1"},
			[2]string{HeaderTimestamp, strconv.FormatInt(m.Timestamp, 10)},
			[2]string{HeaderSignature, m.Signature},
		)
	}
	return h
}

// Tamper breaks one part of a signed webhook, to see whether a receiver
// rejects it.
type Tamper string

const (
	TamperNone      Tamper = ""
	TamperSignature Tamper = "signature" // a signature made with another key
	TamperTimestamp Tamper = "timestamp" // correctly signed, but 10 minutes old
	TamperBody      Tamper = "body"      // signed, then the amount changed
)

// ParseTamper accepts signature, timestamp, body or an empty string.
func ParseTamper(s string) (Tamper, error) {
	switch t := Tamper(strings.ToLower(strings.TrimSpace(s))); t {
	case TamperNone, TamperSignature, TamperTimestamp, TamperBody:
		return t, nil
	}
	return "", fmt.Errorf("unknown --tamper %q (use signature, timestamp or body)", s)
}

// NewBasic builds a Basic webhook.
func NewBasic(payID string) Message {
	b, _ := json.Marshal(struct {
		PayID string `json:"payId"`
	}{payID})
	return Message{Kind: Basic, Body: b}
}

// NewEnhanced signs a payload with key at now, broken as t asks.
func NewEnhanced(key string, pl Payload, now time.Time, t Tamper) (Message, error) {
	body, err := json.Marshal(pl)
	if err != nil {
		return Message{}, err
	}
	return SignBody(key, body, now, t)
}

// SignBody signs a raw body as Paygate would, broken as t asks.
func SignBody(key string, body []byte, now time.Time, t Tamper) (Message, error) {
	if key == "" {
		return Message{}, fmt.Errorf("no HMAC key to sign with")
	}
	ts := now.Unix()
	if t == TamperTimestamp {
		ts = now.Add(-10 * time.Minute).Unix()
	}
	m := Message{Kind: Enhanced, Body: body, Timestamp: ts, Signature: Sign(key, ts, body)}
	switch t {
	case TamperSignature:
		m.Signature = Sign(key+" (wrong)", ts, body)
	case TamperBody:
		changed, err := changeAmount(body)
		if err != nil {
			return Message{}, err
		}
		m.Body = changed
	}
	return m, nil
}

// changeAmount raises amount.value by one, or appends a space when the body
// has no amount, so the signed bytes no longer match.
func changeAmount(body []byte) ([]byte, error) {
	var obj map[string]any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&obj); err != nil {
		return append(append([]byte{}, body...), ' '), nil
	}
	amount, ok := obj["amount"].(map[string]any)
	if !ok {
		return append(append([]byte{}, body...), ' '), nil
	}
	v, err := amount["value"].(json.Number).Int64()
	if err != nil {
		return append(append([]byte{}, body...), ' '), nil
	}
	amount["value"] = v + 1
	return json.Marshal(obj)
}

// Sign returns the header value "v1=<hex>": HMAC-SHA256 with the key over
// timestamp + "." + raw body.
func Sign(key string, ts int64, body []byte) string {
	return "v1=" + signHex(key, strconv.FormatInt(ts, 10), body)
}

func signHex(key, ts string, body []byte) string {
	return hex.EncodeToString(mac(sha256.New, []byte(key), signedPayload(ts, body)))
}

func signedPayload(ts string, body []byte) []byte {
	out := make([]byte, 0, len(ts)+1+len(body))
	out = append(out, ts...)
	out = append(out, '.')
	return append(out, body...)
}

func mac(h func() hash.Hash, key, msg []byte) []byte {
	m := hmac.New(h, key)
	m.Write(msg)
	return m.Sum(nil)
}

// Short abbreviates a signature for display: "v1=8e4c…b17f".
func Short(sig string) string {
	prefix, value := "", sig
	if i := strings.Index(sig, "="); i >= 0 {
		prefix, value = sig[:i+1], sig[i+1:]
	}
	if len(value) <= 12 {
		return sig
	}
	return prefix + value[:4] + "…" + value[len(value)-4:]
}
