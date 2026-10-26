package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/krebs3r/paygate-cli/internal/config"
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

func TestPaymentLink(t *testing.T) {
	m := setup(t)
	mustInit(t)

	// The link's own record says OK as well; the wait ends with the attempt.
	paid := payJSON(t, "link", "--amount", "49", "--methods", "card,paypal", "--poll-interval", "10ms")
	if paid["status"] != "OK" || paid["paymentMethods"] == nil {
		t.Errorf("link should end with the paid attempt, got %v", paid)
	}

	out, _, err := run(t, "link", "--amount", "49", "--expires", "24h", "--trans-id", "TIDLINKNOWAIT", "--no-wait")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Payment link created", "Payment link  " + m.BaseURL, "paygate get --trans-id TIDLINKNOWAIT"} {
		if !strings.Contains(out, want) {
			t.Errorf("--no-wait output misses %q:\n%s", want, out)
		}
	}
	recs, _ := history.Load()
	if len(recs) != 2 || recs[0].Flow != "link" || recs[0].TransID != "TIDLINKNOWAIT" || recs[1].Flow != "link" {
		t.Errorf("history = %+v", recs)
	}

	before := m.Requests()
	curl, _, err := run(t, "link", "--methods", "card,paypal", "--send", "email", "--email", "customer@example.com", "--name", "Max Muster", "--curl")
	if err != nil || m.Requests() != before {
		t.Fatalf("--curl: %v, sent %d requests", err, m.Requests()-before)
	}
	// --methods must not turn the link into a checkout session.
	for _, want := range []string{"/api/v2/payments/links", `"PAYPAL"`, `"serviceType": "EMAIL"`, `"email": "customer@example.com"`, `"lastName": "Muster"`} {
		if !strings.Contains(curl, want) {
			t.Errorf("curl misses %q:\n%s", want, curl)
		}
	}

	if _, _, err := run(t, "link", "--send", "email"); err == nil || !strings.Contains(err.Error(), "e-mail address") {
		t.Errorf("--send email without --email: %v", err)
	}
	if _, _, err := run(t, "link", "--expires", "soon"); err == nil || !strings.Contains(err.Error(), "24h") {
		t.Errorf("bad --expires: %v", err)
	}

	// Like Paygate, the mock ignores the simulation for links: a simulated
	// decline is paid, and paygate says why up front.
	out, _, err = run(t, "link", "--simulate", "decline", "--poll-interval", "10ms")
	if err != nil || !strings.Contains(out, "does not apply the simulation") || !strings.Contains(out, "Paid with PayPal") {
		t.Errorf("--simulate decline on a link: %v\n%s", err, out)
	}
}

// On production, a script must confirm every link with --yes: Paygate does
// not apply the simulation to payment links, and --send reaches a real person.
func TestPaymentLinkDeliveryOnProduction(t *testing.T) {
	m := setup(t)
	if _, _, err := run(t, "init", "--name", "prod", "--env", "prod", "--merchant-id", "demo-shop", "--api-key", "demo-key"); err != nil {
		t.Fatal(err)
	}
	before := m.Requests()
	out, _, err := run(t, "link", "--simulate", "ok", "--send", "email", "--email", "customer@example.com", "--no-wait")
	if err == nil || !strings.Contains(err.Error(), "customer@example.com on PRODUCTION without --yes") || m.Requests() != before {
		t.Fatalf("expected a refusal without sending, got %v", err)
	}
	// The header comes before the confirmation and names the profile.
	if !strings.Contains(out, "PROD") || !strings.Contains(out, "nexi · prod (demo-shop)") {
		t.Errorf("the header should precede the confirmation:\n%s", out)
	}
	_, _, err = run(t, "link", "--simulate", "ok", "--no-wait")
	if err == nil || !strings.Contains(err.Error(), "refusing to create a payment link on PRODUCTION without --yes") || m.Requests() != before {
		t.Errorf("a simulated link needs a confirmation too: %v", err)
	}
	out, _, err = run(t, "link", "--simulate", "ok", "--send", "email", "--email", "customer@example.com", "--no-wait", "--yes")
	if err != nil || !strings.Contains(out, "Paygate sends it to customer@example.com") || !strings.Contains(out, "does not apply the simulation") {
		t.Errorf("--yes: %v\n%s", err, out)
	}
	recs, _ := history.Load()
	if len(recs) != 1 || recs[0].Simulation != "" {
		t.Errorf("a link must not be recorded as simulated: %+v", recs)
	}
}

// When Paygate asks for 3-D Secure, a direct payment cannot complete it in the
// terminal.
func TestPayDirect3DS(t *testing.T) {
	m := setup(t)
	m.Require3DS = true
	mustInit(t)
	if ok := payJSON(t, "pay", "--card", "visa-ok"); ok["status"] != "OK" {
		t.Errorf("test cards without 3DS must skip it, status = %v", ok["status"])
	}
	out, _, err := run(t, "pay", "--card", "visa-3ds-challenge")
	if err != nil || !strings.Contains(out, "requires 3-D Secure") || !strings.Contains(out, "--skip-3ds") {
		t.Errorf("3DS card must explain the way out: %v\n%s", err, out)
	}
	if ok := payJSON(t, "pay", "--card", "visa-3ds-challenge", "--skip-3ds"); ok["status"] != "OK" {
		t.Errorf("--skip-3ds status = %v", ok["status"])
	}
}

func TestReset(t *testing.T) {
	setup(t)
	mustInit(t)
	payJSON(t, "pay", "--card", "visa-ok")
	dir, _ := config.Dir()

	if _, _, err := run(t, "reset"); err == nil || !strings.Contains(err.Error(), "without --yes") {
		t.Fatalf("reset without --yes must refuse in scripts, got %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("a refused reset must not delete anything: %v", err)
	}
	out, _, err := run(t, "reset", "--yes")
	if err != nil || !strings.Contains(out, "All data deleted") || !strings.Contains(out, "paygate init") {
		t.Fatalf("reset: %v\n%s", err, out)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("config directory still exists: %v", err)
	}
	if _, _, err := run(t, "pay", "--card", "visa-ok"); err == nil || !strings.Contains(err.Error(), "no profile") {
		t.Errorf("after a reset no profile may be left, got %v", err)
	}
}

func TestHistoryCommand(t *testing.T) {
	setup(t)
	mustInit(t)
	out, _, err := run(t, "history")
	if err != nil || !strings.Contains(out, "No payments yet") {
		t.Fatalf("empty history: %v\n%s", err, out)
	}
	payJSON(t, "pay", "--card", "visa-ok", "--amount", "4.20")
	payJSON(t, "pay", "--card", "visa-ok", "--simulate", "ok")
	out, _, err = run(t, "history")
	if err != nil || !strings.Contains(out, "4.20 EUR") || !strings.Contains(out, "◆ simulated") {
		t.Fatalf("history table: %v\n%s", err, out)
	}
	js, _, err := run(t, "history", "-o", "json", "--limit", "1")
	var recs []map[string]any
	if err != nil || json.Unmarshal([]byte(js), &recs) != nil || len(recs) != 1 || recs[0]["simulation"] != "Test:0000" {
		t.Fatalf("history json: %v\n%s", err, js)
	}
}
