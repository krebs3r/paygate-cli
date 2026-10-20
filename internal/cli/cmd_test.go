package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/krebs3r/paygate-cli/internal/history"
	"github.com/krebs3r/paygate-cli/internal/mockpaygate"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

// setup starts a mock Paygate and isolates config, keychain and colours.
func setup(t *testing.T) *mockpaygate.Server {
	t.Helper()
	m := mockpaygate.New("demo-shop", "demo-key")
	m.NotFoundPolls, m.PendingPolls = 1, 1
	srv := httptest.NewServer(m.Handler())
	t.Cleanup(srv.Close)
	m.BaseURL = srv.URL
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PAYGATE_NO_KEYCHAIN", "1")
	t.Setenv("PAYGATE_BASE_URL", srv.URL)
	t.Setenv("PAYGATE_API_KEY", "")
	t.Setenv("PAYGATE_PROFILE", "")
	t.Setenv("NO_COLOR", "1")
	return m
}

func run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var out, errb bytes.Buffer
	oldOut, oldErr := ui.Stdout, ui.Stderr
	ui.Stdout, ui.Stderr = &out, &errb
	defer func() { ui.Stdout, ui.Stderr = oldOut, oldErr }()
	root := NewRootCmd(BuildInfo{Version: "test", Commit: "abc1234", Date: "today"})
	root.SetArgs(args)
	root.SetOut(&out)
	root.SetErr(&errb)
	err := root.Execute()
	return out.String(), errb.String(), err
}

func mustInit(t *testing.T) {
	t.Helper()
	out, _, err := run(t, "init", "--name", "mock", "--merchant-id", "demo-shop", "--api-key", "demo-key")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	for _, want := range []string{"Credentials verified", "Profile mock", "Basic Auth"} {
		if !strings.Contains(out, want) {
			t.Errorf("init output misses %q:\n%s", want, out)
		}
	}
}

func payJSON(t *testing.T, args ...string) map[string]any {
	t.Helper()
	out, _, err := run(t, append(args, "-o", "json")...)
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("invalid JSON from %v: %v\n%s", args, err, out)
	}
	return m
}

func TestInitRejectsBadCredentials(t *testing.T) {
	setup(t)
	_, stderr, err := run(t, "init", "--name", "mock", "--merchant-id", "demo-shop", "--api-key", "wrong")
	var ex exitError
	if !errors.As(err, &ex) {
		t.Fatalf("expected exit error, got %v", err)
	}
	for _, want := range []string{"rejected the credentials", "5 characters, ends with …rong", "Common causes", "REST API key"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr misses %q:\n%s", want, stderr)
		}
	}
}

func TestInitTrimsPastedCredentials(t *testing.T) {
	setup(t)
	out, stderr, err := run(t, "init", "--name", "mock", "--merchant-id", " demo-shop\u200b", "--api-key", "  demo-key \r\n")
	if err != nil {
		t.Fatalf("init with padded key: %v\n%s", err, stderr)
	}
	if !strings.Contains(out, "Removed spaces or invisible characters around the pasted API key") || !strings.Contains(out, "Credentials verified") {
		t.Errorf("output:\n%s", out)
	}
	if p := payJSON(t, "pay", "--card", "visa-ok"); p["status"] != "OK" {
		t.Errorf("payment with cleaned key: %v", p)
	}
}

func TestPayFlows(t *testing.T) {
	setup(t)
	mustInit(t)

	ok := payJSON(t, "pay", "--card", "visa-ok", "--amount", "12.99")
	if ok["status"] != "OK" {
		t.Errorf("visa-ok status = %v", ok["status"])
	}

	out, _, err := run(t, "pay", "--card", "visa-decline", "--amount", "3.50")
	var ex exitError
	if !errors.As(err, &ex) || ex.code != 1 {
		t.Fatalf("decline should exit 1, got %v", err)
	}
	for _, want := range []string{"FAILED", "Payment failed", "Do not honor", "3.50 EUR"} {
		if !strings.Contains(out, want) {
			t.Errorf("decline output misses %q:\n%s", want, out)
		}
	}

	hosted := payJSON(t, "pay", "--hosted", "--amount", "49", "--methods", "card,paypal", "--poll-interval", "10ms")
	if hosted["status"] != "OK" {
		t.Errorf("hosted final status = %v", hosted["status"])
	}

	out, _, err = run(t, "pay", "--method", "ideal", "--amount", "5", "--poll-interval", "10ms")
	if err != nil {
		t.Fatalf("ideal: %v", err)
	}
	if !strings.Contains(out, "Paid with iDEAL") {
		t.Errorf("ideal output:\n%s", out)
	}
}

func TestPayNeedsFlowWhenNotInteractive(t *testing.T) {
	setup(t)
	mustInit(t)
	_, _, err := run(t, "pay")
	if err == nil || !strings.Contains(err.Error(), "choose a flow") {
		t.Fatalf("expected flow error, got %v", err)
	}
	_, _, err = run(t, "pay", "--hosted", "--card", "visa-ok")
	if err == nil || !strings.Contains(err.Error(), "only one flow") {
		t.Fatalf("expected conflict error, got %v", err)
	}
}

func TestCurlDoesNotSend(t *testing.T) {
	m := setup(t)
	mustInit(t)
	before := m.Requests()
	out, stderr, err := run(t, "pay", "--card", "visa-ok", "--curl")
	if err != nil {
		t.Fatal(err)
	}
	if m.Requests() != before {
		t.Error("--curl must not send the request")
	}
	if !strings.Contains(out, "curl --request POST") || strings.Contains(out, "demo-key") {
		t.Errorf("curl output:\n%s", out)
	}
	if !strings.Contains(stderr, "PAYGATE_API_KEY") {
		t.Errorf("missing key hint on stderr: %s", stderr)
	}
}

func TestFollowUpOperations(t *testing.T) {
	setup(t)
	mustInit(t)
	p := payJSON(t, "pay", "--card", "mc-ok", "--capture", "manual", "--amount", "20")
	payID, _ := p["payId"].(string)
	if p["status"] != "AUTHORIZED" || payID == "" {
		t.Fatalf("manual capture payment = %v", p)
	}

	if _, _, err := run(t, "capture", payID); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("capture without --yes should refuse, got %v", err)
	}
	out, _, err := run(t, "capture", payID, "--yes")
	if err != nil || !strings.Contains(out, "Capture accepted") || !strings.Contains(out, "20.00 EUR") {
		t.Fatalf("capture: %v\n%s", err, out)
	}
	out, _, err = run(t, "refund", payID, "--amount", "5", "--yes")
	if err != nil || !strings.Contains(out, "Refund accepted") {
		t.Fatalf("refund: %v\n%s", err, out)
	}
	out, _, err = run(t, "get", payID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Payment details", "Captured", "20.00 EUR", "Refunded", "5.00 EUR"} {
		if !strings.Contains(out, want) {
			t.Errorf("get output misses %q:\n%s", want, out)
		}
	}
	// The remaining refundable amount is filled in automatically.
	out, _, err = run(t, "refund", payID, "--yes")
	if err != nil || !strings.Contains(out, "15.00 EUR") {
		t.Fatalf("auto-amount refund: %v\n%s", err, out)
	}
	_, _, err = run(t, "cancel", payID, "--yes")
	var ex exitError
	if !errors.As(err, &ex) {
		t.Fatalf("cancel after capture should fail, got %v", err)
	}
}

func TestGetNotFound(t *testing.T) {
	setup(t)
	mustInit(t)
	_, _, err := run(t, "get", "--trans-id", "NOPE")
	if err == nil || !strings.Contains(err.Error(), "PAYMENT NOT FOUND") {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestTestcardsAndProfiles(t *testing.T) {
	setup(t)
	out, _, err := run(t, "testcards", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var cards []map[string]string
	if err := json.Unmarshal([]byte(out), &cards); err != nil || len(cards) != 12 {
		t.Fatalf("testcards json: %v (%d cards)", err, len(cards))
	}

	if _, _, err := run(t, "pay", "--card", "visa-ok"); err == nil || !strings.Contains(err.Error(), "no profile") {
		t.Fatalf("expected missing profile error, got %v", err)
	}
	mustInit(t)
	out, _, err = run(t, "profile", "set", "return-url", "http://localhost/return")
	if err == nil {
		t.Fatalf("http callback URL must be rejected, got %s", out)
	}
	if _, _, err := run(t, "profile", "set", "currency", "chf"); err != nil {
		t.Fatal(err)
	}
	out, _, err = run(t, "profile", "show", "-o", "json")
	if err != nil || !strings.Contains(out, `"currency": "CHF"`) {
		t.Fatalf("profile show: %v\n%s", err, out)
	}
}

func TestAboutCredits(t *testing.T) {
	setup(t)
	out, _, err := run(t, "about")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Martin Krebs", "Florian Bender", "Hannes Reinberger", "github.com/fjbender/mollie-cli"} {
		if !strings.Contains(out, want) {
			t.Errorf("about misses %q", want)
		}
	}
}

func TestPaySimulation(t *testing.T) {
	setup(t)
	mustInit(t)
	// A decline card succeeds when simulated with Test:0000 ...
	ok := payJSON(t, "pay", "--card", "visa-decline", "--simulate", "ok")
	if ok["status"] != "OK" {
		t.Errorf("simulated success = %v", ok["status"])
	}
	// ... and a good card fails with Test:0305.
	out, _, err := run(t, "pay", "--card", "visa-ok", "--simulate", "decline")
	var ex exitError
	if !errors.As(err, &ex) || !strings.Contains(out, "FAILED") {
		t.Fatalf("simulated decline: %v\n%s", err, out)
	}
	curl, _, err := run(t, "pay", "--card", "visa-ok", "--simulate", "0305", "--curl")
	if err != nil || !strings.Contains(curl, `"description": "Test:0305"`) {
		t.Errorf("curl must carry the simulation description: %v\n%s", err, curl)
	}
	recs, _ := history.Load()
	if len(recs) != 2 || recs[0].Simulation != "Test:0305" {
		t.Errorf("history = %+v", recs)
	}
}
