package webhooks

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/krebs3r/paygate-cli/internal/paygate"
)

const (
	testKey = "demo-hmac-key"
	testTS  = int64(1792748527)
)

var basicBody = []byte(`{"payId":"3d81f0a6e2c94b57a0d1e8c6b4f29a73"}`)

// docExample is the Enhanced example of the public documentation.
var docExample = []byte(`{
  "payId": "78f5adccfe8640e5a549613389ff33we",
  "transId": "txn_7890",
  "refNr": "45687",
  "status": "OK",
  "responseCode": "00000000",
  "responseDescription": "success",
  "amount": { "value": 10000, "currency": "EUR" },
  "paymentMethods": [ { "type": "CARD" } ],
  "creationDate": "2025-09-23T13:20:30Z"
}`)

func signedHeader(key string, ts int64, body []byte) http.Header {
	h := http.Header{}
	h.Set(HeaderVersion, "v1")
	h.Set(HeaderTimestamp, "1792748527")
	h.Set(HeaderSignature, Sign(key, ts, body))
	return h
}

func TestSignMatchesReferenceValue(t *testing.T) {
	// Computed independently with Python's hmac module, written in upper case.
	want := "v1=" + strings.ToUpper("7f1466d30e5e2afbfe9c17d2f0a30a7ab30dbdf072c7850c33e780b3578474ce")
	if got := Sign(testKey, testTS, basicBody); got != want {
		t.Fatalf("Sign = %s, want %s", got, want)
	}
}

func TestVerify(t *testing.T) {
	at := time.Unix(testTS, 0).Add(2 * time.Second)

	c := Verify(signedHeader(testKey, testTS, docExample), docExample, testKey, at)
	if c.Kind != Enhanced || !c.Valid || c.Failed() {
		t.Fatalf("valid webhook: %+v", c)
	}

	h := signedHeader(testKey, testTS, docExample)
	h.Set(HeaderSignature, "v0=abcd, "+Sign(testKey, testTS, docExample)+",v2=ef01")
	if c := Verify(h, docExample, testKey, at); !c.Valid {
		t.Errorf("a v1 entry in a list should verify: %+v", c.Findings)
	}

	c = Verify(signedHeader(testKey, testTS, docExample), docExample, "other-key", at)
	if c.Valid || !c.Failed() || c.Expected == "" {
		t.Errorf("wrong key should fail with the expected value: %+v", c)
	}

	c = Verify(signedHeader(testKey, testTS, docExample), docExample, testKey, at.Add(time.Hour))
	if !c.Valid || c.Failed() || !hasLevel(c.Findings, HeaderTimestamp, Warn) {
		t.Errorf("an old timestamp is a warning, not a failure: %+v", c.Findings)
	}

	c = Verify(http.Header{}, docExample, testKey, at)
	if !c.Failed() || !hasLevel(c.Findings, HeaderSignature, Fail) {
		t.Errorf("Enhanced without signature should fail: %+v", c.Findings)
	}

	c = Verify(http.Header{}, basicBody, testKey, at)
	if c.Kind != Basic || c.Failed() || !hasLevel(c.Findings, HeaderSignature, Info) {
		t.Errorf("Basic without signature is expected: %+v", c.Findings)
	}

	c = Verify(signedHeader(testKey, testTS, docExample), docExample, "", at)
	if c.Checked || !hasLevel(c.Findings, HeaderSignature, Warn) {
		t.Errorf("without a key the signature is not checked: %+v", c.Findings)
	}
}

func hasLevel(fs []Finding, name string, l Level) bool {
	for _, f := range fs {
		if f.Name == name && f.Level == l {
			return true
		}
	}
	return false
}

func TestInspect(t *testing.T) {
	if k, fs := Inspect(basicBody); k != Basic || len(fs) != 1 || fs[0].Level != Pass {
		t.Errorf("basic: %s %+v", k, fs)
	}
	if k, fs := Inspect(NewBasic("p1").Body); k != Basic || !hasLevel(fs, "PayID", Warn) || hasLevel(fs, "PayID", Fail) {
		t.Errorf("PayID should be a note: %s %+v", k, fs)
	}
	if string(NewBasic("5e0f").Body) != `{"PayID": "5e0f"}` {
		t.Errorf("Basic body = %s", NewBasic("5e0f").Body)
	}
	if k, fs := Inspect([]byte(`{"payid":"p1"}`)); k != Basic || !hasLevel(fs, "payid", Fail) {
		t.Errorf("other spellings stay errors: %s %+v", k, fs)
	}
	if k, fs := Inspect(docExample); k != Enhanced || len(fs) != 1 || fs[0].Level != Pass {
		t.Errorf("documentation example: %s %+v", k, fs)
	}
	bad := []byte(`{"payId":"a","transId":"b","refNr":null,"status":"OK","responseCode":"0","responseDescription":"",` +
		`"amount":{"value":49.9,"currency":"eur"},"paymentMethods":[{"type":"CASH"}],"creationDate":"23.10.2026","extra":1}`)
	k, fs := Inspect(bad)
	if k != Enhanced {
		t.Fatalf("kind = %s", k)
	}
	for _, want := range []struct {
		name  string
		level Level
	}{
		{"amount.value", Fail}, {"amount.currency", Fail}, {"paymentMethods[0].type", Warn},
		{"creationDate", Fail}, {"extra", Warn},
	} {
		if !hasLevel(fs, want.name, want.level) {
			t.Errorf("missing %s finding for %s: %+v", want.level, want.name, fs)
		}
	}
	if hasLevel(fs, "refNr", Fail) {
		t.Error("refNr may be null")
	}
	if k, _ := Inspect([]byte(`[1,2]`)); k != Unknown {
		t.Errorf("array body: %s", k)
	}
}

func TestDiagnose(t *testing.T) {
	ts := "1792748527"
	right := mac256([]byte(testKey), signedPayload(ts, docExample))
	for _, v := range []string{"v1=" + hex.EncodeToString(right), strings.ToUpper(hex.EncodeToString(right))} {
		if d := Diagnose(testKey, ts, docExample, v); !d.Match {
			t.Errorf("correct value in either case: %+v", d)
		}
	}
	cases := []struct {
		yours string
		want  string
	}{
		{hex.EncodeToString(mac256([]byte(testKey), docExample)), "body alone"},
		{base64.StdEncoding.EncodeToString(right), "Base64 instead of hex"},
		{hex.EncodeToString(mac256([]byte(testKey), signedPayload(ts, compactJSON(t, docExample)))), "serialised again (compact)"},
		{hex.EncodeToString(mac256([]byte(testKey+"\n"), signedPayload(ts, docExample))), "trailing line break"},
		{base64.StdEncoding.EncodeToString(mac256([]byte(testKey), docExample)), "without the timestamp, Base64 instead of hex"},
	}
	for _, c := range cases {
		d := Diagnose(testKey, ts, docExample, c.yours)
		if d.Match || !strings.Contains(d.Cause, c.want) {
			t.Errorf("Diagnose(%s…) = %+v, want cause with %q", c.yours[:8], d, c.want)
		}
	}
	if d := Diagnose(testKey, ts, docExample, "v1=00ff"); d.Match || d.Cause != "" {
		t.Errorf("unknown value: %+v", d)
	}
}

func mac256(key, msg []byte) []byte { return mac(sha256.New, key, msg) }

// lenientEnhanced is an Enhanced webhook beyond the documented schema
// (example values): extra fields, "null" as text, paymentMethods as an object.
var lenientEnhanced = []byte(`{"merchantId": "demo-shop","payId": "5e0f1c2a9b8d4e7f8a6b3c2d1e0f9a8b",` +
	`"transId": "TIDDEMO0000000001","xid": "0a1b2c3d4e5f60718293a4b5c6d7e8f9","refNr": "null","status": "OK",` +
	`"responseCode": "00000000","responseDescription": "success","amount": {"value": 1,"currency": "EUR"},` +
	`"paymentMethods": {"type": "IDEAL"},"creationDate": "2026-10-23T10:00:00Z","channel": "null"}`)

func TestLenientEnhancedFormat(t *testing.T) {
	k, fs := Inspect(lenientEnhanced)
	if k != Enhanced {
		t.Fatalf("kind = %s", k)
	}
	for _, f := range fs {
		if f.Level == Fail {
			t.Errorf("these variations must not fail: %+v", f)
		}
	}
	for _, name := range []string{"paymentMethods", "refNr", "channel", "merchantId", "xid"} {
		if !hasLevel(fs, name, Warn) {
			t.Errorf("missing note for %s: %+v", name, fs)
		}
	}

	h := signedHeader(testKey, testTS, lenientEnhanced)
	c := Verify(h, lenientEnhanced, testKey, time.Unix(testTS, 0))
	if !c.Valid || c.Failed() || !hasLevel(c.Findings, "", Warn) {
		t.Errorf("upper-case signature should verify with a note: %+v", c.Findings)
	}
	h.Set(HeaderSignature, strings.ToLower(h.Get(HeaderSignature)))
	if c := Verify(h, lenientEnhanced, testKey, time.Unix(testTS, 0)); !c.Valid || hasLevel(c.Findings, "", Warn) {
		t.Errorf("lower case verifies without the note: %+v", c.Findings)
	}
}

func compactJSON(t *testing.T, b []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := json.Compact(&out, b); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestParseRequest(t *testing.T) {
	raw := "POST /paygate/webhook HTTP/1.1\r\nHost: shop.example.com\r\nContent-Type: application/json\r\n" +
		"Content-Length: 44\r\nX-Paygate-Timestamp: 1792748527\r\n\r\n" + string(basicBody) + "\n"
	r, err := ParseRequest([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if r.Method != "POST" || r.Target != "/paygate/webhook" || r.Header.Get("x-paygate-timestamp") != "1792748527" {
		t.Errorf("request: %+v", r)
	}
	if string(r.Body) != string(basicBody) || len(r.Notes) != 1 {
		t.Errorf("body should be cut to Content-Length: %q %v", r.Body, r.Notes)
	}

	r, err = ParseRequest([]byte("x-paygate-signature: v1=ab\ncontent-type: application/json\n\n" + string(docExample)))
	if err != nil || r.Method != "" || r.Header.Get(HeaderSignature) != "v1=ab" || string(r.Body) != string(docExample) {
		t.Errorf("header dump with LF: %+v %v", r, err)
	}

	r, err = ParseRequest(append([]byte("\n  "), basicBody...))
	if err != nil || string(r.Body) != string(basicBody) || len(r.Header) != 0 {
		t.Errorf("body only: %+v %v", r, err)
	}

	if _, err := ParseRequest([]byte("this is not a header\n\n{}")); err == nil {
		t.Error("garbage should fail")
	}
}

func TestTamper(t *testing.T) {
	now := time.Unix(testTS, 0)
	pl := Payload{PayID: "p", TransID: "t", Status: "OK", ResponseCode: "00000000", ResponseDescription: "success",
		Amount: Amount{Value: 4990, Currency: "EUR"}, PaymentMethods: []Method{{Type: "CARD"}}, CreationDate: "2026-10-23T09:41:29Z"}
	check := func(tm Tamper) Check {
		m, err := NewEnhanced(testKey, pl, now, tm)
		if err != nil {
			t.Fatal(err)
		}
		h := http.Header{}
		for _, kv := range m.Headers() {
			h.Set(kv[0], kv[1])
		}
		return Verify(h, m.Body, testKey, now)
	}
	if c := check(TamperNone); !c.Valid || c.Failed() {
		t.Errorf("untampered: %+v", c.Findings)
	}
	if c := check(TamperSignature); c.Valid {
		t.Error("tampered signature verified")
	}
	if c := check(TamperBody); c.Valid {
		t.Error("tampered body verified")
	}
	if c := check(TamperTimestamp); !c.Valid || c.Age < 9*time.Minute || !hasLevel(c.Findings, HeaderTimestamp, Warn) {
		t.Errorf("old timestamp: valid=%v age=%s %+v", c.Valid, c.Age, c.Findings)
	}
	if _, err := ParseTamper("everything"); err == nil {
		t.Error("unknown tamper mode accepted")
	}
}

func TestFromPayment(t *testing.T) {
	p := &paygate.Payment{PayID: "p1", TransID: "t1", Status: "OK", ResponseCode: "00000000", ResponseDescription: "success",
		Amount: &paygate.AmountDetails{Value: 1, Currency: "EUR"}, PaymentMethods: json.RawMessage(`{"type":"IDEAL"}`)}
	pl := FromPayment(p, time.Date(2026, 10, 23, 11, 42, 7, 0, time.FixedZone("CEST", 7200)))
	b, _ := json.Marshal(pl)
	want := `{"payId":"p1","transId":"t1","refNr":null,"status":"OK","responseCode":"00000000","responseDescription":"success",` +
		`"amount":{"value":1,"currency":"EUR"},"paymentMethods":[{"type":"IDEAL"}],"creationDate":"2026-10-23T09:42:07Z"}`
	if string(b) != want {
		t.Errorf("payload\n got %s\nwant %s", b, want)
	}
	if k, fs := Inspect(b); k != Enhanced || fs[0].Level != Pass {
		t.Errorf("own payload fails the schema: %+v", fs)
	}
}

func TestShortAndAge(t *testing.T) {
	if got := Short("v1=8e4c02aa11b17f"); got != "v1=8e4c…b17f" {
		t.Errorf("Short = %s", got)
	}
	if got := Age(3*time.Hour + 12*time.Minute); got != "3 h 12 min old" {
		t.Errorf("Age = %s", got)
	}
	if got := Age(-2 * time.Minute); got != "2 min in the future" {
		t.Errorf("Age = %s", got)
	}
}
