package cli

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krebs3r/paygate-cli/internal/webhooks"
)

const testHMACKey = "demo-hmac-key"

// webhookReceiver answers 204 to Basic webhooks and to Enhanced ones that
// verify with testHMACKey and are at most 5 minutes old, 401 otherwise. A
// lax receiver accepts all.
func webhookReceiver(t *testing.T, lax bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c := webhooks.Verify(r.Header, body, testHMACKey, time.Now())
		fresh := c.Age <= webhooks.Tolerance && c.Age >= -webhooks.Tolerance
		if lax || c.Kind == webhooks.Basic || (c.Valid && !c.Failed() && fresh) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "invalid signature", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestWebhookSend(t *testing.T) {
	setup(t)
	mustInit(t)
	t.Setenv("PAYGATE_HMAC_KEY", "")
	pay := payJSON(t, "pay", "--card", "visa-ok", "--amount", "49.90")
	payID, _ := pay["payId"].(string)
	strict := webhookReceiver(t, false)

	if _, _, err := run(t, "webhooks", "send", payID, "--to", strict.URL); err == nil || !strings.Contains(err.Error(), "no HMAC key") {
		t.Fatalf("send without HMAC key: %v", err)
	}
	if out, _, err := run(t, "profile", "set", "hmac-key", testHMACKey); err != nil || !strings.Contains(out, "HMAC key") {
		t.Fatalf("set hmac-key: %v\n%s", err, out)
	}
	if out, _, _ := run(t, "profile", "show"); !strings.Contains(out, "HMAC key") || strings.Contains(out, testHMACKey) {
		t.Errorf("profile show should name the key without showing it:\n%s", out)
	}

	out, _, err := run(t, "webhooks", "send", payID, "--to", strict.URL+"/hook")
	if err != nil || !strings.Contains(out, "accepted the webhook") || !strings.Contains(out, "X-Paygate-Signature") {
		t.Errorf("send: %v\n%s", err, out)
	}
	for _, tamper := range []string{"signature", "timestamp", "body"} {
		out, _, err := run(t, "webhooks", "send", payID, "--to", strict.URL, "--tamper", tamper)
		if err != nil || !strings.Contains(out, "as it should") {
			t.Errorf("tamper %s against a strict endpoint: %v\n%s", tamper, err, out)
		}
	}

	lax := webhookReceiver(t, true)
	out, _, err = run(t, "webhooks", "send", payID, "--to", lax.URL, "--tamper", "signature")
	var ex exitError
	if !errors.As(err, &ex) || !strings.Contains(out, "accepted the wrong signature") {
		t.Errorf("a lax endpoint must fail the tamper test: %v\n%s", err, out)
	}

	out, _, err = run(t, "webhooks", "send", payID, "--to", strict.URL, "--type", "basic", "-o", "json")
	var res map[string]any
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || res["asExpected"] != true {
		t.Errorf("basic as JSON: %v\n%s", err, out)
	}

	out, _, err = run(t, "webhooks", "send", payID, "--to", strict.URL, "--curl")
	if err != nil || !strings.Contains(out, "--header 'X-Paygate-Signature: v1=") || !strings.Contains(out, `"payId":"`+payID+`"`) {
		t.Errorf("--curl: %v\n%s", err, out)
	}
}

func TestWebhookVerify(t *testing.T) {
	setup(t)
	mustInit(t)
	t.Setenv("PAYGATE_HMAC_KEY", testHMACKey)
	dir := t.TempDir()

	body := []byte(`{"payId":"p1","transId":"t1","refNr":null,"status":"OK","responseCode":"00000000","responseDescription":"success",` +
		`"amount":{"value":4990,"currency":"EUR"},"paymentMethods":[{"type":"CARD"}],"creationDate":"2026-10-23T09:41:29Z"}`)
	msg, err := webhooks.SignBody(testHMACKey, body, time.Now(), webhooks.TamperNone)
	if err != nil {
		t.Fatal(err)
	}
	var req strings.Builder
	req.WriteString("POST /paygate/webhook HTTP/1.1\r\n")
	for _, h := range msg.Headers() {
		req.WriteString(h[0] + ": " + h[1] + "\r\n")
	}
	req.WriteString("\r\n" + string(msg.Body) + "\n") // the line break an editor adds
	file := filepath.Join(dir, "webhook.http")
	if err := os.WriteFile(file, []byte(req.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	out, _, err := run(t, "webhooks", "verify", "--request", file)
	if err != nil || !strings.Contains(out, "matches the HMAC key") || !strings.Contains(out, "ENHANCED") {
		t.Errorf("verify: %v\n%s", err, out)
	}

	out, _, err = run(t, "webhooks", "verify", "--request", file, "--yours", hmacBodyOnly(body))
	var ex exitError
	if !errors.As(err, &ex) || !strings.Contains(out, "body alone") {
		t.Errorf("--yours over the body alone: %v\n%s", err, out)
	}

	basic := filepath.Join(dir, "basic.json")
	_ = os.WriteFile(basic, []byte(`{"payId":"p1"}`), 0o600)
	out, _, err = run(t, "webhooks", "verify", "--body", basic)
	if err != nil || !strings.Contains(out, "BASIC") || !strings.Contains(out, "Enhanced webhook is enabled") {
		t.Errorf("basic body: %v\n%s", err, out)
	}

	wrong := filepath.Join(dir, "wrong.http")
	_ = os.WriteFile(wrong, []byte(strings.Replace(req.String(), `"value":4990`, `"value":4991`, 1)), 0o600)
	out, _, err = run(t, "webhooks", "verify", "--request", wrong, "-o", "json")
	var res map[string]any
	if !errors.As(err, &ex) || json.Unmarshal([]byte(out), &res) != nil || res["signature"].(map[string]any)["valid"] != false {
		t.Errorf("changed body must fail: %v\n%s", err, out)
	}
}

// hmacBodyOnly signs the body without the timestamp, a common mistake.
func hmacBodyOnly(body []byte) string {
	m := hmac.New(sha256.New, []byte(testHMACKey))
	m.Write(body)
	return "v1=" + hex.EncodeToString(m.Sum(nil))
}
