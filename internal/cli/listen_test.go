package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/krebs3r/paygate-cli/internal/listen"
	"github.com/krebs3r/paygate-cli/internal/webhooks"
)

func TestPayListenReceivesTheWebhook(t *testing.T) {
	m := setup(t)
	m.Webhook, m.HMACKey = "enhanced", testHMACKey
	mustInit(t)
	t.Setenv("PAYGATE_HMAC_KEY", testHMACKey)
	t.Setenv("PAYGATE_CLOUDFLARED", "/nonexistent")
	old := webhookGrace
	webhookGrace = 5 * time.Second
	t.Cleanup(func() { webhookGrace = old })

	// The mock runs on 127.0.0.1, so no tunnel is needed.
	out, _, err := run(t, "pay", "--hosted", "--amount", "12.99", "--listen", "--no-qr", "--poll-interval", "50ms")
	if err != nil {
		t.Fatalf("pay --listen: %v\n%s", err, out)
	}
	for _, want := range []string{"Webhook URL", "this computer only", "Webhook received", "ENHANCED", "✓ signature", "status OK", "Confirmed with GET"} {
		if !strings.Contains(out, want) {
			t.Errorf("output misses %q:\n%s", want, out)
		}
	}
	if _, ok := listen.Running(); ok {
		t.Error("pay --listen must remove its listener")
	}

	out, _, err = run(t, "webhooks", "list")
	if err != nil || !strings.Contains(out, "ENHANCED") || !strings.Contains(out, "✓ valid") {
		t.Errorf("list: %v\n%s", err, out)
	}
	out, _, err = run(t, "webhooks", "show")
	if err != nil || !strings.Contains(out, "matches the HMAC key") || !strings.Contains(out, "X-Paygate-Signature") {
		t.Errorf("show: %v\n%s", err, out)
	}
	strict := webhookReceiver(t, false)
	out, _, err = run(t, "webhooks", "resend", "1", "--to", strict.URL)
	if err != nil || !strings.Contains(out, "accepted the webhook") {
		t.Errorf("resend: %v\n%s", err, out)
	}
	out, _, err = run(t, "webhooks", "clear", "--yes")
	if err != nil || !strings.Contains(out, "Deleted 1 received webhooks") {
		t.Errorf("clear: %v\n%s", err, out)
	}
}

func TestPayUsesRunningListener(t *testing.T) {
	setup(t)
	mustInit(t)
	s, err := listen.Open(context.Background(), listen.SessionOptions{Options: listen.Options{Profile: "mock"}, Local: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	out, _, err := run(t, "pay", "--hosted", "--amount", "5", "--curl")
	if err != nil || !strings.Contains(out, `"webhook": "`+s.URL+`"`) {
		t.Errorf("hosted payments should send their webhook to the listener: %v\n%s", err, out)
	}
	out, _, _ = run(t, "pay", "--card", "visa-ok", "--amount", "5", "--curl")
	if strings.Contains(out, s.URL) {
		t.Errorf("direct payments have no webhook URL:\n%s", out)
	}
}

func TestListenWithoutCloudflared(t *testing.T) {
	setup(t)
	mustInit(t)
	t.Setenv("PAYGATE_BASE_URL", "https://paygate.example.com") // not local: a tunnel would be needed
	t.Setenv("PAYGATE_CLOUDFLARED", "/nonexistent")
	out, _, err := run(t, "listen")
	var ex exitError
	if !errors.As(err, &ex) || !strings.Contains(out, "No tunnel found") || !strings.Contains(out, "webhook.invalid") {
		t.Errorf("listen without cloudflared: %v\n%s", err, out)
	}
	if _, _, err := run(t, "pay", "--card", "visa-ok", "--listen"); err == nil || !strings.Contains(err.Error(), "hosted payment") {
		t.Errorf("--listen with a direct payment: %v", err)
	}
}

func TestEventLine(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	e := listen.Event{Time: time.Date(2026, 10, 23, 13, 47, 23, 0, time.Local), Kind: webhooks.Basic, Signature: "missing",
		PayID: "5e0f1c2a9b8d4e7f8a6b3c2d1e0f9a8b", Answer: 204, Repeat: 1, SinceFirst: 63 * time.Second}
	line := eventLine(e)
	for _, want := range []string{"13:47:23", "BASIC", "– unsigned", "PayID 5e0f1c2a9b8d4e7f8a6b3c2d1e0f9a8b", "→ 204", "↻ again · +63 s"} {
		if !strings.Contains(line, want) {
			t.Errorf("line misses %q: %s", want, line)
		}
	}
	b, _ := json.Marshal(eventJSON(listen.Event{Body: []byte(`{"PayID": "x"}`)}))
	if !strings.Contains(string(b), `"body":"{\"PayID\": \"x\"}"`) {
		t.Errorf("JSON should carry the body as text: %s", b)
	}
}
