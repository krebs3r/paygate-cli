package tour

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/demo"
	"github.com/krebs3r/paygate-cli/internal/i18n"
	"github.com/krebs3r/paygate-cli/internal/paygate"
)

// demoSession starts the demo in a fresh folder.
func demoSession(t *testing.T) *core.Session {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PAYGATE_BASE_URL", "")
	t.Cleanup(demo.Stop)
	if _, err := demo.Start(); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	s, err := core.OpenSession(cfg, "", core.ClientOptions{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// customer answers the demo checkout like a customer would.
func customer(t *testing.T, action string) func(string) error {
	return func(link string) error {
		go func() {
			resp, err := http.Get(link)
			if err != nil {
				t.Error(err)
				return
			}
			resp.Body.Close()
			u, _ := url.Parse(link)
			form := url.Values{"transId": {u.Query().Get("transId")}, "action": {action}, "method": {"CARD"}}
			if resp, err := http.PostForm(u.Scheme+"://"+u.Host+"/checkout", form); err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
}

// walk runs every step of a tour, waiting steps until they are decided.
func walk(t *testing.T, r *Run) {
	t.Helper()
	ctx := context.Background()
	for i := range r.Tour.Steps {
		if !r.Go(i) {
			t.Fatalf("step %d cannot be reached", i+1)
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			o := r.Exec(ctx)
			if o == nil {
				t.Fatalf("step %d: no outcome", i+1)
			}
			if o.Err != nil {
				t.Fatalf("step %d (%s): %v", i+1, r.Current().Title, o.Err)
			}
			if !o.Pending {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("step %d (%s) still pending: %s", i+1, r.Current().Title, o.Status)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	if !r.Finished() {
		t.Fatal("tour not finished")
	}
}

func TestHostedTour(t *testing.T) {
	s := demoSession(t)
	OpenURL = customer(t, "pay")
	defer func() { OpenURL = nil }()
	r, err := Start(Find("hosted"), s, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	walk(t, r)
	if r.Webhook == nil || !r.Webhook.Check(r.HMACKey()).Valid {
		t.Fatal("the hosted tour should receive a signed webhook")
	}
	if c := r.Webhook.Check(r.HMACKey()); c.Received != strings.ToLower(c.Received) {
		t.Errorf("the demo signs as the documentation shows, in lower-case hex: %s", c.Received)
	}
	last := r.Outcomes[len(r.Outcomes)-1]
	if last.Skipped != "" || last.Exchange == nil || last.Exchange.Status != 200 {
		t.Fatalf("capture = %+v", last)
	}
	back := r.Outcomes[3]
	if !strings.Contains(back.Status, "Paid") || !strings.Contains(strings.Join(back.Lines, "\n"), "→ urls.return") ||
		!strings.Contains(strings.Join(back.Lines, "\n"), "does not really forward to paygate.paytest.dev") {
		t.Errorf("after paying, the return step names urls.return: %+v", back)
	}
	if x := r.Outcomes[0].Exchange; x == nil || x.Notes["urls.webhook"].EN == "" || !strings.Contains(x.Curl, "/payments/sessions") {
		t.Errorf("the session step shows the request with notes and curl: %+v", x)
	}
}

func TestHostedTourDeclined(t *testing.T) {
	s := demoSession(t)
	OpenURL = customer(t, "decline")
	defer func() { OpenURL = nil }()
	r, err := Start(Find("hosted"), s, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	walk(t, r)
	if r.Outcomes[len(r.Outcomes)-1].Skipped == "" {
		t.Error("a declined payment has nothing to capture")
	}
	if back := r.Outcomes[3]; !strings.Contains(back.Status, "Declined") || !strings.Contains(strings.Join(back.Lines, "\n"), "→ urls.return") {
		t.Errorf("after a decline, the return step says so: %+v", back)
	}
}

func TestHostedTourCancelled(t *testing.T) {
	s := demoSession(t)
	OpenURL = customer(t, "cancel")
	defer func() { OpenURL = nil }()
	r, err := Start(Find("hosted"), s, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	walk(t, r)
	if back := r.Outcomes[3]; !strings.Contains(back.Status, "Cancelled") || !strings.Contains(strings.Join(back.Lines, "\n"), "→ urls.cancel") {
		t.Errorf("after cancelling, the return step names urls.cancel: %+v", back)
	}
}

func TestDirectTour(t *testing.T) {
	r, err := Start(Find("direct"), demoSession(t), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	walk(t, r)
	first := r.Outcomes[0]
	if first.Exchange == nil || !strings.Contains(string(first.Exchange.Response), "threeDSMethodUrl") {
		t.Fatalf("the 3DS payment answers with versioningData: %+v", first.Exchange)
	}
	method := strings.Join(r.Outcomes[1].Lines, "\n")
	if !strings.Contains(method, "threeDSMethodData") || !strings.Contains(method, "/acs/3ds-method") {
		t.Errorf("the 3DS method step shows the form with the real values:\n%s", method)
	}
	if d := r.Outcomes[4]; d.Exchange == nil || d.Exchange.Status < 400 {
		t.Errorf("the declined card answers with an error status: %+v", d.Exchange)
	}
	if st := r.Outcomes[5]; st.Exchange == nil || !strings.Contains(string(st.Exchange.Response), `"OK"`) {
		t.Errorf("the status of the payment without 3DS is OK: %+v", st.Exchange)
	}
}

func TestWebhooksTour(t *testing.T) {
	s := demoSession(t)
	OpenURL = customer(t, "pay")
	defer func() { OpenURL = nil }()
	r, err := Start(Find("webhooks"), s, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	walk(t, r)
	sig := strings.Join(r.Outcomes[3].Lines, "\n")
	if !strings.Contains(sig, "✓") || r.Webhook == nil {
		t.Fatalf("signature step:\n%s", sig)
	}
}

func TestOperationsTour(t *testing.T) {
	r, err := Start(Find("operations"), demoSession(t), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	walk(t, r)
	for i, o := range r.Outcomes {
		if o.Skipped != "" || o.Exchange == nil || o.Exchange.Status >= 300 {
			t.Errorf("step %d: %+v", i+1, o)
		}
	}
	res, err := r.Session.Client.GetByPayID(context.Background(), r.PayID)
	if err != nil {
		t.Fatal(err)
	}
	if a := res.Payment.Amount; a.CapturedValue != 3000 || a.RefundedValue != 1000 {
		t.Errorf("captured %d, refunded %d", a.CapturedValue, a.RefundedValue)
	}
}

func TestLinkTour(t *testing.T) {
	s := demoSession(t)
	OpenURL = customer(t, "pay")
	defer func() { OpenURL = nil }()
	r, err := Start(Find("link"), s, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	walk(t, r)
	if x := r.Outcomes[0].Exchange; x == nil || x.Status != 202 || !strings.Contains(x.URL, "/payments/links") || !strings.Contains(string(x.Body), "expirationTime") {
		t.Fatalf("the link step creates a link with an expiry: %+v", x)
	}
	share := strings.Join(r.Outcomes[1].Lines, "\n")
	for _, want := range []string{"serviceType", "EMAIL", "SMS", "externalTemplate", "customer@example.com"} {
		if !strings.Contains(share, want) {
			t.Errorf("the share step shows how Paygate sends the link, missing %q:\n%s", want, share)
		}
	}
	if r.Webhook == nil || !r.Webhook.Check(r.HMACKey()).Valid {
		t.Error("the link tour receives the signed webhook of the payment")
	}
	if last := r.Outcomes[4]; last.Exchange == nil || !strings.Contains(last.Status, "OK") {
		t.Errorf("the status of the paid link is OK: %+v", last)
	}
}

func TestFormsTour(t *testing.T) {
	s := demoSession(t)
	OpenURL = customer(t, "pay")
	defer func() { OpenURL = nil }()
	r, err := Start(Find("forms"), s, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	walk(t, r)
	if x := r.Outcomes[0].Exchange; x == nil || x.Status != 202 || !strings.Contains(compact(x.Body), `"integrationType":"HOSTED"`) {
		t.Fatalf("the payment step creates a hosted payment for one method: %+v", x)
	}
	if back := strings.Join(r.Outcomes[3].Lines, "\n"); r.PayID == "" || !strings.Contains(back, r.PayID) {
		t.Errorf("the return step shows the payId:\n%s", back)
	}
	last := r.Outcomes[4]
	if last.Exchange == nil || !strings.Contains(last.Exchange.URL, "/payments/getByPayId/"+r.PayID) || !strings.Contains(last.Status, "OK") {
		t.Errorf("the details step looks the payment up by payId: %+v", last)
	}
}

func TestDCCTour(t *testing.T) {
	r, err := Start(Find("dcc"), demoSession(t), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if !r.Tour.Preview {
		t.Error("the eDCC tour is a preview")
	}
	walk(t, r)
	if !strings.Contains(r.Outcomes[0].Status, "USA") {
		t.Errorf("the BIN check names the card's country: %s", r.Outcomes[0].Status)
	}
	if !strings.Contains(r.Outcomes[1].Status, "USD") || r.dcc == nil {
		t.Fatalf("the quote makes an offer in USD: %s", r.Outcomes[1].Status)
	}
	offer := strings.Join(r.Outcomes[2].Lines, "\n")
	if !strings.Contains(offer, "49.90 EUR") || !strings.Contains(offer, "USD") || strings.Contains(offer, "●") {
		t.Errorf("the offer shows both amounts, nothing preselected:\n%s", offer)
	}
	if x := r.Outcomes[3].Exchange; x == nil || !strings.Contains(string(x.Body), r.dcc.DCCRequestID) || !strings.Contains(compact(x.Body), `"selected":true`) {
		t.Errorf("the payment carries the customer's choice: %+v", x)
	}
	if got := strings.Join(r.Outcomes[4].Lines, "\n"); !strings.Contains(got, "USD") {
		t.Errorf("the details show the eDCC outcome:\n%s", got)
	}
}

func TestDCCTourWithoutEDCC(t *testing.T) {
	s := demoSession(t)
	sc := demo.Scenarios{}
	_ = sc.Set("edcc", "off")
	if err := demo.SaveScenarios(sc); err != nil {
		t.Fatal(err)
	}
	r, err := Start(Find("dcc"), s, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	walk(t, r)
	if x := r.Outcomes[0].Exchange; x == nil || x.Status != 422 {
		t.Errorf("without eDCC the BIN check is refused: %+v", x)
	}
	if r.Outcomes[2].Skipped == "" {
		t.Error("without an offer there is nothing to show")
	}
	if x := r.Outcomes[3].Exchange; x == nil || strings.Contains(string(x.Body), `"dcc"`) || !strings.Contains(r.Outcomes[3].Status, "OK") {
		t.Errorf("without an offer the payment runs as usual: %+v", x)
	}
	if got := strings.Join(r.Outcomes[4].Lines, "\n"); !strings.Contains(got, "No eDCC") {
		t.Errorf("the details say there is no eDCC:\n%s", got)
	}
}

// compact removes the indentation of a JSON body.
func compact(b []byte) string {
	var buf bytes.Buffer
	if json.Compact(&buf, b) != nil {
		return string(b)
	}
	return buf.String()
}

func TestToursRefuseProduction(t *testing.T) {
	s := &core.Session{Name: "live", Profile: &config.Profile{Env: "prod"}}
	if _, err := Start(Find("operations"), s, "test"); err != ErrProduction {
		t.Errorf("production must be refused: %v", err)
	}
}

func TestStepsCannotBeSkipped(t *testing.T) {
	r, err := Start(Find("operations"), demoSession(t), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.Go(2) {
		t.Error("a step after an unfinished one must not be reachable")
	}
}

func TestEveryStepHasTextsInBothLanguages(t *testing.T) {
	for _, tour := range All() {
		for i, st := range tour.Steps {
			for _, txt := range []T{st.Short, st.Title, st.Explain} {
				if txt.EN == "" || txt.DE == "" {
					t.Errorf("%s step %d: text missing: %+v", tour.ID, i+1, txt)
				}
			}
			if len(st.Arrows) == 0 {
				t.Errorf("%s step %d has no row in the diagram", tour.ID, i+1)
			}
			if (st.Kind == Request) != (st.Plan != nil) || (st.Kind != Request && st.Do == nil) {
				t.Errorf("%s step %d: kind and functions do not fit", tour.ID, i+1)
			}
		}
	}
}

func TestDiagramAndAnnotate(t *testing.T) {
	i18n.Set(i18n.DE)
	defer i18n.Set(i18n.EN)
	d := Diagram(Find("hosted"), 0, 70)
	for _, want := range []string{"Server", "Paygate", "Browser", "POST /payments/sessions", "▶"} {
		if !strings.Contains(d, want) {
			t.Errorf("diagram misses %q:\n%s", want, d)
		}
	}
	lines := Annotate([]byte(`{"transId":"T1","amount":{"value":4990,"currency":"EUR"}}`), map[string]T{"amount.value": noteValue})
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Pflicht") || !strings.Contains(joined, "4990") || len(lines) != 7 {
		t.Errorf("annotated:\n%s", joined)
	}
	_ = paygate.OpCapture
}

func TestEveryStepLinksToTheDocumentation(t *testing.T) {
	for _, tour := range All() {
		for i, st := range tour.Steps {
			nexi, computop := st.Doc.URL("nexi"), st.Doc.URL("computop")
			if !strings.HasPrefix(nexi, "https://paygate-nexigroup.redocly.app/") && !strings.HasPrefix(nexi, "https://docs.paygate.nexigroup.com/") {
				t.Errorf("%s step %d: Nexi link %q", tour.ID, i+1, nexi)
			}
			if !strings.HasPrefix(computop, "https://computop-docs.redocly.app/") {
				t.Errorf("%s step %d: Computop link %q", tour.ID, i+1, computop)
			}
			if st.Doc.Label("nexi") == "" || st.Doc.Label("computop") == "" {
				t.Errorf("%s step %d has no link label", tour.ID, i+1)
			}
		}
	}
}

func TestHostedTourWithoutWebhooks(t *testing.T) {
	s := demoSession(t)
	sc := demo.Scenarios{}
	_ = sc.Set("webhook", "off")
	_ = sc.Set("checkout", "pay")
	if err := demo.SaveScenarios(sc); err != nil {
		t.Fatal(err)
	}
	OpenURL = func(string) error { return nil } // the checkout pays by itself
	defer func() { OpenURL = nil }()
	r, err := Start(Find("hosted"), s, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	walk(t, r)
	if o := r.Outcomes[4]; !strings.Contains(o.Skipped, "webhooks off") {
		t.Errorf("the webhook step is skipped with the reason: %+v", o)
	}
	if o := r.Outcomes[6]; o.Skipped != "" || o.Exchange == nil {
		t.Errorf("the payment that paid by itself can be captured: %+v", o)
	}
}

func TestExportKeepsCredentialsAndLocalAddressesOut(t *testing.T) {
	s := demoSession(t)
	OpenURL = customer(t, "pay")
	defer func() { OpenURL = nil }()
	r, err := Start(Find("hosted"), s, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	walk(t, r)
	at := time.Date(2026, 11, 18, 14, 5, 0, 0, time.Local)
	tags := regexp.MustCompile(`<[^>]+>`) // the HTML colours its text with tags
	for _, f := range []Format{HTML, Markdown} {
		out := string(Export(r, f, at))
		if dir := os.Getenv("PAYGATE_EXPORT_DIR"); dir != "" {
			_ = os.WriteFile(filepath.Join(dir, FileName(r, f, at)), []byte(out), 0o600)
		}
		for _, leak := range []string{demo.APIKey, demo.HMACKey, demo.MerchantID, "127.0.0.1", "localhost", "dem…"} {
			if strings.Contains(out, leak) {
				t.Errorf("%s: the protocol contains %q", f, leak)
			}
		}
		for _, want := range []string{
			"https://test.paygate.nexigroup.com/api/v2/payments/sessions",
			`--user "$PAYGATE_MERCHANT_ID:$PAYGATE_API_KEY"`,
			"https://shop.example/webhook",
			"Create a checkout session", "your unique ID", "simulated",
			"X-Paygate-Signature", "7 steps, all run", "Capture payment",
		} {
			text := tags.ReplaceAllString(out, "")
			if !strings.Contains(text, want) && !strings.Contains(text, html.EscapeString(want)) {
				t.Errorf("%s: missing %q", f, want)
			}
		}
	}
	page := string(Export(r, HTML, at))
	if !strings.HasPrefix(page, "<!doctype html>") {
		t.Error("HTML protocol should be a page of its own")
	}
	for _, want := range []string{`<span class="j-k">&#34;transId&#34;</span>`, `<span class="j-n">4990</span>`, `<span class="h-m">POST</span>`, `<b>required</b> · your unique ID`, `title="Set this variable before you run the command">$PAYGATE_MERCHANT_ID</span>:<span class="v"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the HTML protocol colours its JSON: missing %s", want)
		}
	}
	if got := FileName(r, Markdown, at); got != "paygate-tour-hosted-20261118-1405.md" {
		t.Errorf("FileName = %q", got)
	}
}

func TestExportOfAnUnfinishedRun(t *testing.T) {
	s := demoSession(t)
	r, err := Start(Find("operations"), s, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.Ran() {
		t.Fatal("nothing ran yet")
	}
	r.Go(0)
	if o := r.Exec(context.Background()); o.Err != nil {
		t.Fatal(o.Err)
	}
	out := string(Export(r, Markdown, time.Now()))
	for _, want := range []string{"5 steps, 4 not run", "Not run: the tour ended before this step.", "```bash"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestFormatOf(t *testing.T) {
	for name, want := range map[string]Format{"a.html": HTML, "b.HTM": HTML, "c.md": Markdown, "d.markdown": Markdown} {
		if f, err := FormatOf(name); err != nil || f != want {
			t.Errorf("FormatOf(%q) = %q, %v", name, f, err)
		}
	}
	if _, err := FormatOf("protocol.pdf"); err == nil {
		t.Error("pdf is no protocol format")
	}
}
