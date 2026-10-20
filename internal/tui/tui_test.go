package tui

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/history"
	"github.com/krebs3r/paygate-cli/internal/i18n"
	"github.com/krebs3r/paygate-cli/internal/mockpaygate"
)

// setup isolates config and keychain and points the client to a mock.
func setup(t *testing.T, profiles map[string]*config.Profile, active string) *model {
	t.Helper()
	m := mockpaygate.New("demo-shop", "demo-key")
	srv := httptest.NewServer(m.Handler())
	t.Cleanup(srv.Close)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PAYGATE_NO_KEYCHAIN", "1")
	t.Setenv("PAYGATE_BASE_URL", srv.URL)
	t.Setenv("PAYGATE_PROFILE", "")
	t.Setenv("PAYGATE_API_KEY", "")
	t.Setenv("LANG", "en_US.UTF-8")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	for name, p := range profiles {
		if _, err := config.StoreAPIKey(name, p, "demo-key"); err != nil {
			t.Fatal(err)
		}
		cfg.Profiles[name] = p
	}
	cfg.ActiveProfile = active
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	ctx := &Context{Version: "test"}
	ctx.Reload()
	mdl := newModel(ctx)
	send(mdl, tea.WindowSizeMsg{Width: 120, Height: 34})
	return mdl
}

// send feeds messages one by one to the model. Each message is processed
// completely, including the commands it triggers, before the next one, like
// a user typing. Slow commands (timers) are skipped.
func send(m *model, msgs ...tea.Msg) {
	for _, msg := range msgs {
		drain(m, msg)
	}
}

func drain(m *model, first tea.Msg) {
	queue := []tea.Msg{first}
	// Animations (spinner ticks, cursor blinks) re-arm themselves forever,
	// so stop after a generous number of follow-up messages.
	for steps := 0; len(queue) > 0 && steps < 60; steps++ {
		msg := queue[0]
		queue = queue[1:]
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				if r := run(c); r != nil {
					queue = append(queue, r)
				}
			}
			continue
		}
		_, cmd := m.Update(msg)
		if r := run(cmd); r != nil {
			queue = append(queue, r)
		}
	}
}

func run(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(80 * time.Millisecond):
		return nil
	}
}

func keys(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

var (
	enter = tea.KeyMsg{Type: tea.KeyEnter}
	esc   = tea.KeyMsg{Type: tea.KeyEsc}
	down  = tea.KeyMsg{Type: tea.KeyDown}
)

func twoProfiles() map[string]*config.Profile {
	return map[string]*config.Profile{
		"shop-test": {Brand: "nexi", Env: "test", MerchantID: "demo-shop", Auth: "basic"},
		"shop-prod": {Brand: "nexi", Env: "prod", MerchantID: "demo-shop", Auth: "basic"},
	}
}

func TestWelcomeWithoutProfile(t *testing.T) {
	m := setup(t, nil, "")
	i18n.Set(i18n.EN)
	view := m.View()
	for _, want := range []string{"Welcome to paygate", "Add profile", "no profile"} {
		if !strings.Contains(view, want) {
			t.Errorf("view misses %q:\n%s", want, view)
		}
	}
	send(m, enter)
	if m.current().ID() != "profiles" || !m.current().Capturing() {
		t.Fatalf("enter on the welcome screen should open the add-profile form, got %s", m.current().ID())
	}
}

func TestNavigationAndShortcuts(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	if !strings.Contains(m.View(), "TEST") || !strings.Contains(m.View(), "ACTIVE PROFILE") {
		t.Fatalf("home view:\n%s", m.View())
	}
	send(m, keys("t"))
	if m.current().ID() != "cards" || !strings.Contains(m.View(), "visa-ok") {
		t.Fatalf("t should open the test cards:\n%s", m.View())
	}
	send(m, esc, down)
	if m.focus != focusMenu || m.current().ID() != "profiles" {
		t.Fatalf("esc + down should move in the menu, got %s focus=%v", m.current().ID(), m.focus)
	}
	send(m, keys("?"))
	if !strings.Contains(m.View(), "Keyboard shortcuts") {
		t.Error("? should show the help")
	}
	send(m, esc)
	if m.help {
		t.Error("esc should close the help")
	}
}

func TestSwitchProfileToProduction(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	send(m, keys("p"))
	// Profiles are sorted: shop-prod, shop-test. Select the first and switch.
	send(m, keys("k"), keys("k"))
	for i := 0; i < 3 && m.ctx.Cfg.Names()[m.screens[m.active].(*profilesScreen).sel] != "shop-prod"; i++ {
		send(m, keys("j"))
	}
	send(m, enter)
	if m.ctx.Session == nil || m.ctx.Session.Name != "shop-prod" || !m.ctx.IsProd() {
		t.Fatalf("session after switch = %+v", m.ctx.Session)
	}
	reloaded, _ := config.Load()
	if reloaded.ActiveProfile != "shop-prod" {
		t.Errorf("active profile not saved: %s", reloaded.ActiveProfile)
	}
	if !strings.Contains(m.View(), "PROD") {
		t.Error("header should show PROD")
	}
}

func TestAddProfileThroughForm(t *testing.T) {
	m := setup(t, nil, "")
	i18n.Set(i18n.EN)
	send(m, enter)                                                      // open form from the welcome screen
	send(m, keys("x"), enter, enter, enter)                             // name "defaultx", platform, environment
	send(m, keys("demo-shop"), enter, keys(" demo-key "), enter, enter) // merchant, padded key, auth
	if m.ctx.Session == nil || m.ctx.Session.Name != "defaultx" {
		t.Fatalf("profile not created; view:\n%s", m.View())
	}
	key, err := config.APIKey("defaultx", m.ctx.Session.Profile)
	if err != nil || key != "demo-key" {
		t.Errorf("stored key = %q, %v", key, err)
	}
}

func TestLanguageSwitch(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	send(m, keys("s"), enter)  // edit interface language
	send(m, down, down, enter) // auto → English → Deutsch
	if i18n.Current() != i18n.DE || !strings.Contains(m.View(), "Einstellungen") {
		t.Fatalf("language not switched:\n%s", m.View())
	}
	cfg, _ := config.Load()
	if cfg.UILanguage != "de" {
		t.Errorf("ui_language = %q", cfg.UILanguage)
	}
	i18n.Set(i18n.EN)
}

func TestTooSmall(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	send(m, tea.WindowSizeMsg{Width: 50, Height: 12})
	if !strings.Contains(m.View(), "larger") {
		t.Errorf("expected a resize hint, got:\n%s", m.View())
	}
}

func TestNewPaymentDirectCard(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	send(m, keys("n"))
	if m.current().ID() != "new" || !m.current().Capturing() {
		t.Fatalf("n should open the payment form, got %s", m.current().ID())
	}
	s := m.current().(*newPaymentScreen)
	if s.amount != core.DefaultAmount || s.simulate != "off" {
		t.Errorf("test profile defaults: amount %s simulate %s", s.amount, s.simulate)
	}
	// flow (direct), card (visa-ok), amount, currency, description, simulation, capture
	send(m, enter, enter, enter, enter, enter, enter, enter)
	if s.stage != stageReview || !strings.Contains(m.View(), "VISA •••• 1111") {
		t.Fatalf("expected review, got stage %d:\n%s", s.stage, m.View())
	}
	send(m, keys("j"))
	if !strings.Contains(m.View(), "•••• 1111") || strings.Contains(m.View(), "4111111111111111") {
		t.Error("the JSON preview must mask the card number")
	}
	send(m, enter)
	if s.stage != stageResult || s.result == nil || s.result.Status != "OK" {
		t.Fatalf("expected a successful result, got stage %d result %+v", s.stage, s.result)
	}
	if !strings.Contains(m.View(), "Payment successful") {
		t.Errorf("result view:\n%s", m.View())
	}
	recs, _ := history.Load()
	if len(recs) != 1 || recs[0].Status != "OK" || recs[0].Profile != "shop-test" {
		t.Errorf("history = %+v", recs)
	}
	// Refund the captured payment right from the result.
	send(m, keys("f"), enter)
	if s.opKind != "refund" {
		t.Errorf("refund not executed: note %q", s.note)
	}
}

func TestNewPaymentSimulatedOnProduction(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-prod")
	i18n.Set(i18n.EN)
	send(m, keys("n"))
	s := m.current().(*newPaymentScreen)
	if s.amount != core.ProdDefaultAmount || s.simulate != "ok" || s.capture != "manual" {
		t.Fatalf("production defaults: amount %s simulate %s capture %s", s.amount, s.simulate, s.capture)
	}
	send(m, enter, enter, enter, enter, enter, enter, enter)
	if s.req.Order.Description != core.SimSuccess {
		t.Fatalf("description = %q, want %s", s.req.Order.Description, core.SimSuccess)
	}
	if !strings.Contains(m.View(), "simulated with Test:0000") {
		t.Errorf("review must explain the simulation:\n%s", m.View())
	}
	send(m, enter)
	if s.result == nil || s.result.Status != "AUTHORIZED" || !s.canRelease() {
		t.Fatalf("expected an authorization to release, got %+v", s.result)
	}
	send(m, keys("x"), enter)
	if s.opKind != "cancel" {
		t.Errorf("release not executed")
	}
}

func TestNewPaymentSimulatedDecline(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	send(m, keys("n"))
	s := m.current().(*newPaymentScreen)
	s.simulate = "decline"
	s.form = nil
	send(m, enter) // rebuilds the form with the decline preset
	send(m, enter, enter, enter, enter, enter, enter, enter, enter)
	if s.result == nil || s.result.Status != "FAILED" {
		t.Fatalf("expected a simulated decline, got stage %d %+v\n%s", s.stage, s.result, m.View())
	}
}

func TestNewPaymentHostedWaitsForCustomer(t *testing.T) {
	firstPoll, pollInterval = 5*time.Millisecond, 5*time.Millisecond
	defer func() { firstPoll, pollInterval = 500*time.Millisecond, 2*time.Second }()
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	send(m, keys("n"))
	s := m.current().(*newPaymentScreen)
	send(m, down, enter) // hosted payment page
	send(m, enter)       // methods: all
	send(m, enter, enter, enter, enter, enter)
	if s.stage != stageReview {
		t.Fatalf("expected review, got %d:\n%s", s.stage, m.View())
	}
	send(m, enter)
	for i := 0; i < 20 && s.stage == stageWaiting; i++ {
		send(m, run(s.poll(m.ctx, time.Millisecond)))
	}
	if s.stage != stageResult || s.result == nil || s.result.Status != "OK" {
		t.Fatalf("hosted payment did not complete: stage %d %+v", s.stage, s.result)
	}
	if !strings.Contains(m.View(), "Paid with PayPal") {
		t.Errorf("result view:\n%s", m.View())
	}
}
