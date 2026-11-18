package tui

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/krebs3r/paygate-cli/internal/history"
	"github.com/krebs3r/paygate-cli/internal/i18n"
	"github.com/krebs3r/paygate-cli/internal/tour"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

var (
	right = tea.KeyMsg{Type: tea.KeyRight}
	left  = tea.KeyMsg{Type: tea.KeyLeft}
)

// payOnCheckout stands in for the browser: it opens the demo checkout and
// pays.
func payOnCheckout(t *testing.T) func() {
	old := tour.OpenURL
	tour.OpenURL = func(link string) error {
		go func() {
			resp, err := http.Get(link)
			if err != nil {
				t.Error(err)
				return
			}
			resp.Body.Close()
			u, _ := url.Parse(link)
			if resp, err := http.PostForm(u.Scheme+"://"+u.Host+"/checkout",
				url.Values{"transId": {u.Query().Get("transId")}, "action": {"pay"}, "method": {"CARD"}}); err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
	return func() { tour.OpenURL = old }
}

// tourStep runs the current step with enter, unless it ran or waits by
// itself, and lets a waiting step look again until it is decided.
func tourStep(t *testing.T, m *model) {
	t.Helper()
	s := m.current().(*tourScreen)
	if !s.run.Done(s.run.Step) && !s.busy {
		send(m, enter)
	}
	deadline := time.Now().Add(5 * time.Second)
	for s.busy {
		if time.Now().After(deadline) {
			t.Fatalf("step %d still waits: %s", s.run.Step+1, s.waiting)
		}
		time.Sleep(50 * time.Millisecond)
		send(m, tourAgainMsg{run: s.run})
	}
}

func TestHostedTourInTheApp(t *testing.T) {
	m := setup(t, nil, "")
	defer payOnCheckout(t)()
	send(m, enter) // the demo, from the welcome screen
	i18n.Set(i18n.EN)
	send(m, keys("t"))
	for _, want := range []string{"Integration tour", "Hosted Payment Page", "Check webhooks", "RUNS AGAINST", "DEMO"} {
		if !strings.Contains(m.View(), want) {
			t.Fatalf("tour list misses %q:\n%s", want, m.View())
		}
	}

	send(m, enter)
	s := m.current().(*tourScreen)
	if s.run == nil || !m.layout().focus {
		t.Fatalf("enter starts the tour in focus mode:\n%s", m.View())
	}
	v := ansi.Strip(m.View())
	for _, want := range []string{"step 1 of 7", "Create a checkout session", "POST /payments/sessions", "Server", "more lines"} {
		if !strings.Contains(v, want) {
			t.Errorf("step view misses %q:\n%s", want, v)
		}
	}
	send(m, tea.KeyMsg{Type: tea.KeyPgDown})
	if v := ansi.Strip(m.View()); !strings.Contains(v, "required · your unique ID") || !strings.Contains(v, "step 1 of 7") {
		t.Errorf("pgdn scrolls to the request; title and step bar stay:\n%s", v)
	}
	if strings.Contains(v, "New payment") {
		t.Errorf("focus mode hides the menu:\n%s", v)
	}

	tourStep(t, m) // session
	if v := ansi.Strip(m.View()); !strings.Contains(v, "202 Accepted") || !strings.Contains(v, "_links") {
		t.Fatalf("after the session request the answer shows:\n%s", v)
	}
	for s.run != nil && !s.run.Finished() {
		send(m, enter) // to the next step
		tourStep(t, m)
	}
	if s.run == nil {
		t.Fatal("the tour ended too early")
	}
	for i := range s.run.Tour.Steps {
		if o := s.run.Outcome(i); o == nil || o.Err != nil || o.Skipped != "" {
			t.Errorf("step %d: %+v", i+1, o)
		}
	}
	if w := s.run.Webhook; w == nil || !w.Check(s.run.HMACKey()).Valid {
		t.Error("the tour should have received a signed webhook")
	}
	send(m, enter)
	if s.form == nil || !strings.Contains(m.View(), "Tour finished. Save its log?") || !strings.Contains(m.View(), "Don't save") {
		t.Fatalf("enter on the last step offers the log first:\n%s", m.View())
	}
	send(m, esc)
	if s.run == nil || s.form != nil {
		t.Fatal("esc goes back to the tour")
	}
	send(m, enter, down, down, enter) // don't save
	if s.run != nil || m.layout().focus || !strings.Contains(m.View(), "Tour finished") {
		t.Fatalf("without a log the tour ends:\n%s", m.View())
	}
	recs, _ := history.Load()
	captured := false
	for _, r := range recs {
		for _, e := range r.Events {
			captured = captured || e.Kind == "capture"
		}
	}
	if len(recs) != 1 || recs[0].Status != "OK" || !captured {
		t.Errorf("the tour's payment and its capture are in the history: %+v", recs)
	}
}

func TestTourKeysAndLeaving(t *testing.T) {
	m := setup(t, nil, "")
	send(m, enter)
	i18n.Set(i18n.EN)
	send(m, keys("t"), down, down, down, enter) // capture, refund, reversal
	s := m.current().(*tourScreen)
	if s.run == nil || s.run.Tour.ID != "operations" {
		t.Fatalf("the fourth tour should run, got %+v", s.run)
	}
	send(m, right)
	if s.run.Step != 0 {
		t.Error("a step after one that has not run must not open")
	}
	tourStep(t, m)
	send(m, right, keys("v"), keys("v"))
	if s.run.Step != 1 || s.pane != paneCurl || !strings.Contains(m.View(), "curl --request POST") {
		t.Fatalf("→ moves on, v switches to curl:\n%s", m.View())
	}
	send(m, left)
	if s.run.Step != 0 || s.pane != paneResponse {
		t.Errorf("← goes back to the step that ran and shows its answer")
	}
	send(m, keys("q"))
	if m.quitAsk != nil || s.run == nil {
		t.Error("q must not quit or leave while a tour runs")
	}
	send(m, esc)
	if s.run != nil || m.layout().focus {
		t.Error("esc leaves the tour")
	}
}

func TestTourRefusesProduction(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-prod")
	i18n.Set(i18n.EN)
	send(m, keys("t"), enter)
	if s := m.current().(*tourScreen); s.run != nil || !strings.Contains(m.View(), "Tours do not run on production") {
		t.Fatalf("tours must not start on production:\n%s", m.View())
	}
}

func TestDemoHomeOffersTheTourFirst(t *testing.T) {
	m := setup(t, nil, "")
	send(m, enter)
	i18n.Set(i18n.EN)
	send(m, keys("h"), enter)
	if m.current().ID() != "tour" {
		t.Fatalf("the first quick action of the demo is the tour, got %s", m.current().ID())
	}
}

func TestTourOpensTheDocumentation(t *testing.T) {
	m := setup(t, nil, "")
	send(m, enter)
	i18n.Set(i18n.EN)
	var opened []string
	old := tour.OpenURL
	tour.OpenURL = func(u string) error { opened = append(opened, u); return nil }
	defer func() { tour.OpenURL = old }()
	send(m, keys("t"), down, down, down, enter) // capture, refund, reversal
	if !strings.Contains(ansi.Strip(m.View()), "API reference: Create payment ↗") {
		t.Fatalf("the step names its documentation:\n%s", ansi.Strip(m.View()))
	}
	send(m, keys("d"))
	want := "https://paygate-nexigroup.redocly.app/payments/createpayment"
	if len(opened) != 1 || opened[0] != want {
		t.Fatalf("d opens %s, got %v", want, opened)
	}

	// A click on the link opens it too.
	for y, line := range strings.Split(ansi.Strip(m.View()), "\n") {
		if i := strings.Index(line, "API reference: Create payment"); i >= 0 {
			send(m, tea.MouseMsg{X: len([]rune(line[:i])) + 3, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		}
	}
	if len(opened) != 2 || opened[1] != want {
		t.Fatalf("a click on the link opens it, got %v", opened)
	}

}

func TestTourFocusModeInANarrowWindow(t *testing.T) {
	m := setup(t, nil, "")
	send(m, enter, tea.WindowSizeMsg{Width: 90, Height: 40})
	i18n.Set(i18n.EN)
	send(m, keys("t"))
	if !strings.Contains(m.View(), "New payment") {
		t.Fatalf("the tour list keeps the tabs:\n%s", m.View())
	}
	send(m, enter)
	v := ansi.Strip(m.View())
	top := strings.SplitN(v, "\n", 2)[0]
	if strings.Contains(v, "New payment") || !strings.Contains(top, "Tour") || !m.layout().focus {
		t.Fatalf("a running tour hides the tabs and names itself in the frame:\n%s", v)
	}
}

func TestTourWaitsByItselfAndCopies(t *testing.T) {
	m := setup(t, nil, "")
	defer payOnCheckout(t)()
	var copied []string
	oldCopy := copyText
	copyText = func(s string) error { copied = append(copied, s); return nil }
	defer func() { copyText = oldCopy }()
	send(m, enter)
	i18n.Set(i18n.EN)
	send(m, keys("t"), enter)
	s := m.current().(*tourScreen)
	send(m, keys("y"))
	if len(copied) != 1 || !strings.Contains(copied[0], `"transId"`) || strings.Contains(copied[0], "required") {
		t.Fatalf("y copies the request body without the notes: %q", copied)
	}
	tourStep(t, m) // session
	send(m, enter) // to the redirect
	tourStep(t, m) // opens the page; the tour moves on and waits by itself
	if s.run.Step != 2 || !s.run.Done(2) {
		t.Fatalf("after the page opened, the pay step should wait and see the payment, step %d done=%v", s.run.Step+1, s.run.Done(2))
	}
	if o := s.run.Outcome(2); o.Exchange == nil || !strings.Contains(string(o.Exchange.Response), `"OK"`) {
		t.Fatalf("the pay step shows the status lookup: %+v", o)
	}
	send(m, keys("y"))
	if len(copied) != 2 || !strings.Contains(copied[1], `"status": "OK"`) {
		t.Fatalf("y copies the answer: %q", copied)
	}
	send(m, enter, enter) // return
	send(m, enter)        // to the webhook: it waits by itself
	deadline := time.Now().Add(5 * time.Second)
	for s.busy && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		send(m, tourAgainMsg{run: s.run})
	}
	if !s.run.Done(4) {
		t.Fatal("the webhook step should wait by itself and receive the webhook")
	}
}

func TestMouseCanBeTurnedOffToSelectText(t *testing.T) {
	m := setup(t, nil, "")
	send(m, enter)
	i18n.Set(i18n.EN)
	send(m, keys("m"))
	if !m.mouseOff || !strings.Contains(m.View(), "TEXT SELECTION") {
		t.Fatalf("m turns the mouse off for text selection:\n%s", m.View())
	}
	send(m, keys("t"), enter, keys("m"))
	if m.mouseOff {
		t.Fatal("m in a running tour turns the clicks back on")
	}
}

func TestNewTourDropsTheFinishedToast(t *testing.T) {
	m := setup(t, nil, "")
	send(m, enter)
	i18n.Set(i18n.EN)
	send(m, toastMsg{text: "Tour finished", tone: ui.ToneSuccess})
	if !strings.Contains(m.View(), "Tour finished") {
		t.Fatal("the toast should show first")
	}
	send(m, keys("t"), enter)
	if strings.Contains(m.View(), "Tour finished") {
		t.Fatalf("a new tour must not show the toast of the last one:\n%s", m.View())
	}
}

func TestDemoScenariosInSettings(t *testing.T) {
	m := setup(t, nil, "")
	send(m, enter)
	i18n.Set(i18n.EN)
	send(m, keys("h"), esc, keys("z")) // from the menu, where the keyboard starts
	if m.current().ID() != "settings" || !strings.Contains(m.View(), "DEMO SCENARIOS") || !strings.Contains(m.View(), "asks in the browser") {
		t.Fatalf("z opens the demo scenarios:\n%s", m.View())
	}
	send(m, enter, down, enter) // the checkout: pays by itself
	if got := m.ctx.Demo().Scenarios().Get("checkout"); got != "pay" {
		t.Fatalf("checkout = %q", got)
	}
	if !strings.Contains(m.View(), "pays by itself") {
		t.Errorf("the setting shows the new value:\n%s", m.View())
	}
}

func TestTourSavesTheLog(t *testing.T) {
	m := setup(t, nil, "")
	send(m, enter)
	i18n.Set(i18n.EN)
	dir := t.TempDir()
	t.Chdir(dir)
	send(m, keys("t"), down, down, down, enter) // capture, refund, reversal
	send(m, keys("x"))
	if !strings.Contains(m.View(), "Run a step first") {
		t.Fatalf("before a step ran there is nothing to log:\n%s", m.View())
	}
	tourStep(t, m)
	send(m, keys("x"))
	s := m.current().(*tourScreen)
	if s.form == nil || !strings.Contains(m.View(), "Save the log of this tour") || !strings.Contains(m.View(), "Markdown") {
		t.Fatalf("x asks for the format:\n%s", m.View())
	}
	send(m, down, enter)
	if s.form != nil || !strings.Contains(m.View(), "Log saved") {
		t.Fatalf("enter saves the log:\n%s", m.View())
	}
	files, _ := filepath.Glob(filepath.Join(dir, "paygate-tour-operations-*.md"))
	if len(files) != 1 {
		t.Fatalf("one Markdown log in the working directory, got %v", files)
	}
	b, _ := os.ReadFile(files[0])
	if !strings.Contains(string(b), "## 1 · Authorize the amount") || !strings.Contains(string(b), "4 not run") {
		t.Errorf("the log has the step that ran and the rest as not run:\n%s", b)
	}
	send(m, keys("x"), enter)
	if files, _ := filepath.Glob(filepath.Join(dir, "paygate-tour-operations-*.html")); len(files) != 1 {
		t.Errorf("HTML is the first choice, got %v", files)
	}
	if s.run == nil {
		t.Error("saving the log keeps the tour open")
	}
}

func TestFinishedTourSavesTheLog(t *testing.T) {
	m := setup(t, nil, "")
	send(m, enter)
	i18n.Set(i18n.EN)
	dir := t.TempDir()
	t.Chdir(dir)
	send(m, keys("t"), down, down, down, enter) // capture, refund, reversal
	s := m.current().(*tourScreen)
	for !s.run.Finished() {
		tourStep(t, m)
		if !s.run.Finished() {
			send(m, enter)
		}
	}
	send(m, enter, enter) // the tour is done: save as HTML
	if s.run != nil || !strings.Contains(m.View(), "Log saved") {
		t.Fatalf("saving the log ends the tour:\n%s", m.View())
	}
	files, _ := filepath.Glob(filepath.Join(dir, "paygate-tour-operations-*.html"))
	if len(files) != 1 {
		t.Fatalf("one HTML log, got %v", files)
	}
	if b, _ := os.ReadFile(files[0]); !strings.Contains(string(b), "5 steps, all run") {
		t.Error("the log of a finished tour has every step")
	}
}
