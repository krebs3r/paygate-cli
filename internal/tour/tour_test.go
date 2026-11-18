package tour

import (
	"context"
	"net/http"
	"net/url"
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
