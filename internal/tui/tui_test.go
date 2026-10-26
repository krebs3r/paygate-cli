package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/history"
	"github.com/krebs3r/paygate-cli/internal/i18n"
	"github.com/krebs3r/paygate-cli/internal/listen"
	"github.com/krebs3r/paygate-cli/internal/mockpaygate"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/reqlog"
	"github.com/krebs3r/paygate-cli/internal/ui"
	"github.com/krebs3r/paygate-cli/internal/webhooks"
)

// setup isolates config and keychain and points the client to a mock.
func setup(t *testing.T, profiles map[string]*config.Profile, active string) *model {
	t.Helper()
	mdl, _ := setupMock(t, profiles, active)
	return mdl
}

// setupMock is setup that also returns the mock Paygate.
func setupMock(t *testing.T, profiles map[string]*config.Profile, active string) (*model, *mockpaygate.Server) {
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
	if !t.Failed() && !strings.HasPrefix(t.Name(), "TestSplash") {
		t.Setenv("PAYGATE_NO_SPLASH", "1")
	} else {
		t.Setenv("PAYGATE_NO_SPLASH", "")
	}

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
	return mdl, m
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
	enter    = tea.KeyMsg{Type: tea.KeyEnter}
	esc      = tea.KeyMsg{Type: tea.KeyEsc}
	down     = tea.KeyMsg{Type: tea.KeyDown}
	shiftTab = tea.KeyMsg{Type: tea.KeyShiftTab}
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

func TestTopBorderShowsCommit(t *testing.T) {
	m := setup(t, nil, "")
	m.ctx.Commit = "abc1234"
	if top := strings.SplitN(m.View(), "\n", 2)[0]; !strings.Contains(top, "test · abc1234") {
		t.Errorf("top border misses version and commit: %q", top)
	}
	m.ctx.Commit = "unknown"
	if top := strings.SplitN(m.View(), "\n", 2)[0]; strings.Contains(top, "unknown") {
		t.Errorf("an unknown commit should be left out: %q", top)
	}
}

func TestNavigationAndShortcuts(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	if !strings.Contains(m.View(), "TEST") || !strings.Contains(m.View(), "ACTIVE PROFILE") {
		t.Fatalf("home view:\n%s", m.View())
	}
	send(m, keys("l"))
	if m.current().ID() != "payments" || !strings.Contains(m.View(), "Payments") {
		t.Fatalf("l should open the payments:\n%s", m.View())
	}
	send(m, esc, down)
	if m.focus != focusMenu || m.current().ID() != "webhooks" {
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
	if !strings.Contains(m.View(), "HMAC key for webhooks") {
		t.Fatalf("the form should offer the HMAC key; view:\n%s", m.View())
	}
	send(m, keys("y"), keys("demo-hmac-key"), enter) // yes, then the key
	if m.ctx.Session == nil || m.ctx.Session.Name != "defaultx" {
		t.Fatalf("profile not created; view:\n%s", m.View())
	}
	key, err := config.APIKey("defaultx", m.ctx.Session.Profile)
	if err != nil || key != "demo-key" {
		t.Errorf("stored key = %q, %v", key, err)
	}
	if hmacKey, err := config.HMACKey("defaultx", m.ctx.Session.Profile); err != nil || hmacKey != "demo-hmac-key" {
		t.Errorf("stored HMAC key = %q, %v", hmacKey, err)
	}
}

func TestAddSecondProfileChoosesPlatform(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	// With a profile the new payment keeps a form while it is not in front.
	// The steps of the profile form must not move it, nor its answers this one.
	send(m, keys("p"), keys("a"), keys("shop-cp"), enter)
	send(m, down, enter, enter) // Computop, test system
	send(m, keys("demo-shop"), enter, keys("demo-key"), enter, enter)
	send(m, enter) // no HMAC key for now
	if p := m.ctx.Cfg.Profiles["shop-cp"]; p == nil || p.Brand != "computop" || p.Env != "test" || p.HMACStore != "" {
		t.Fatalf("profile = %+v; view:\n%s", p, m.View())
	}
}

func TestProfileFormShowsWhereCallsGo(t *testing.T) {
	m := setup(t, nil, "")
	i18n.Set(i18n.EN)
	t.Setenv("PAYGATE_BASE_URL", "") // the real hosts; the form sends nothing before it is done
	expect := func(want string) {
		t.Helper()
		if v := ansi.Strip(m.View()); !strings.Contains(v, want) {
			t.Fatalf("expected %q:\n%s", want, v)
		}
	}
	send(m, enter, keys("x"), enter) // open the form, name; on to the platform
	expect("API https://test.paygate.nexigroup.com/api/v2")
	send(m, down) // Computop
	expect("API https://test.computop-paygate.com/api/v2")
	send(m, enter, down) // production
	expect("API https://www.computop-paygate.com/api/v2")
	if v := ansi.Strip(m.View()); strings.Index(v, "API https") < strings.Index(v, "Production system") {
		t.Fatalf("the API URL belongs below the options:\n%s", v)
	}
	send(m, enter, keys("demo-shop"), enter, keys("demo-key"), enter)
	expect("Merchant ID and API key with every request")
	send(m, down) // OAuth2
	expect("Token from https://www.computop-paygate.com/authorization/oauth/token")
}

func TestPaymentFormShowsEndpoint(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	send(m, keys("n"))
	payments := "POST " + m.ctx.Session.Client.APIURL("/payments")
	if v := ansi.Strip(m.View()); !strings.Contains(v, payments+"\n") && !strings.Contains(v, payments+" ") {
		t.Fatalf("a direct card payment goes to %q:\n%s", payments, v)
	}
	send(m, down) // hosted payment page
	if v := ansi.Strip(m.View()); !strings.Contains(v, payments+"/sessions") {
		t.Fatalf("a hosted payment goes to %q:\n%s", payments+"/sessions", v)
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

func TestHMACKeySetting(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	t.Setenv("PAYGATE_HMAC_KEY", "")
	send(m, keys("s"))
	s := m.current().(*settingsScreen)
	for i, st := range s.visible(m.ctx) {
		if st.id == "hmac_key" {
			s.sel = i
		}
	}
	if v := m.View(); !strings.Contains(v, "Webhook HMAC key") || !strings.Contains(v, "not set") {
		t.Fatalf("settings should show the HMAC key row:\n%s", v)
	}
	stored := func() string {
		k, err := config.HMACKey("shop-test", m.ctx.Session.Profile)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}

	send(m, enter, keys("demo-hmac-key"), enter) // no key yet: straight to the input
	if stored() != "demo-hmac-key" || !strings.Contains(m.View(), "set · in the config file") || strings.Contains(m.View(), "demo-hmac-key") {
		t.Fatalf("set: key %q, view:\n%s", stored(), m.View())
	}
	if cfg, _ := config.Load(); cfg.Profiles["shop-test"].HMACStore != config.StoreFile {
		t.Error("the key must be saved with the profile")
	}

	send(m, enter, enter, keys("new-key"), enter) // replace (preselected), new key
	if stored() != "new-key" {
		t.Errorf("replace: key %q", stored())
	}
	send(m, enter, down, enter) // remove
	if stored() != "" || !strings.Contains(m.View(), "not set") {
		t.Errorf("remove: key %q, view:\n%s", stored(), m.View())
	}
	send(m, enter, enter) // an empty entry changes nothing
	if stored() != "" || s.form != nil {
		t.Errorf("empty entry: key %q", stored())
	}
}

func TestThemeSetting(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	defer ui.SetTheme("")
	send(m, keys("s"), down) // second entry: colour theme
	if !strings.Contains(m.View(), "Colour theme") || !strings.Contains(m.View(), "Blue") {
		t.Fatalf("settings must offer the colour theme:\n%s", m.View())
	}
	send(m, enter, down, enter) // Blue → Blurple
	if ui.ThemeID() != "blurple" || m.ctx.Cfg.Theme != "blurple" {
		t.Fatalf("theme = %s, config %q", ui.ThemeID(), m.ctx.Cfg.Theme)
	}
	if cfg, _ := config.Load(); cfg.Theme != "blurple" {
		t.Errorf("theme not saved: %q", cfg.Theme)
	}
	m.ctx.Reload()
	if ui.ThemeID() != "blurple" {
		t.Error("the saved theme must apply when the app starts")
	}
	// Back to blue from the command palette; the default is stored empty.
	send(m, tea.KeyMsg{Type: tea.KeyCtrlK}, keys("blue"), enter)
	if ui.ThemeID() != "blue" || m.ctx.Cfg.Theme != "" {
		t.Errorf("palette switch: theme %s, config %q", ui.ThemeID(), m.ctx.Cfg.Theme)
	}
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
	if !strings.Contains(m.View(), "PAYMENT APPROVED") || !strings.Contains(m.View(), "VISA •••• 1111") {
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

	// The payment is done: esc leaves to the menu and the receipt stays;
	// shift+tab still leads back to the review.
	if v := ansi.Strip(m.View()); !strings.Contains(v, "esc menu") {
		t.Errorf("the result should offer the way to the menu:\n%s", v)
	}
	transID := s.req.TransID
	send(m, esc)
	if m.focus != focusMenu || s.stage != stageResult || !strings.Contains(m.View(), "PAYMENT APPROVED") {
		t.Fatalf("esc on a successful result should move to the menu, focus %v stage %d", m.focus, s.stage)
	}
	send(m, enter, shiftTab)
	if s.stage != stageReview || s.req.TransID == transID {
		t.Errorf("shift+tab on the result should open the review with a new trans ID, stage %d", s.stage)
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

func TestNewPaymentCardListFitsOneLinePerCard(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	// German in the narrowest wide window: the description wraps, which
	// must neither wrap the rows nor push the last option out of view.
	i18n.Set(i18n.DE)
	defer i18n.Set(i18n.EN)
	send(m, tea.WindowSizeMsg{Width: wideMinW, Height: 30})
	send(m, keys("n"), enter) // flow: direct card
	view := m.View()
	for _, want := range []string{
		"Visa        4111 1111 1111 1111  erfolgreich",
		"Mastercard  5105 1051 0510 5100  abgelehnt",
		"Amex        3714 496353 98431    abgelehnt",
		"Kartendaten eingeben",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("card list misses the line %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "4000 0128 9268 8323") {
		t.Errorf("3DS cards cannot complete a direct payment and must not be offered:\n%s", view)
	}
}

// Even when Paygate asks for 3-D Secure on every card, the test cards
// without 3DS go through.
func TestNewPaymentTestCardWithout3DS(t *testing.T) {
	m, mock := setupMock(t, twoProfiles(), "shop-prod")
	mock.Require3DS = true
	i18n.Set(i18n.EN)
	send(m, keys("n"))
	s := m.current().(*newPaymentScreen)
	send(m, enter, enter, enter, enter, enter, enter, enter)
	if s.stage != stageReview || s.req.PaymentMethods.Card.ThreeDsPolicy == nil {
		t.Fatalf("visa-ok must skip 3-D Secure, stage %d:\n%s", s.stage, m.View())
	}
	if !strings.Contains(m.View(), "skipped") {
		t.Errorf("review must show that 3-D Secure is skipped:\n%s", m.View())
	}
	send(m, enter)
	if s.result == nil || s.result.Status != "AUTHORIZED" {
		t.Fatalf("expected an authorization, got %+v\n%s", s.result, m.View())
	}
}

// When Paygate asks for 3-D Secure the result explains it, and esc or
// shift+tab lead back to the review and the form, which shows at once.
func TestNewPayment3DSRequiredGoesBack(t *testing.T) {
	m, mock := setupMock(t, twoProfiles(), "shop-prod")
	mock.Require3DS = true
	i18n.Set(i18n.EN)
	send(m, keys("n"))
	s := m.current().(*newPaymentScreen)
	s.cardChoice, s.cardNumber, s.threeDS = manualCardChoice, "4111111111111111", "request"
	s.form = nil
	send(m, enter) // rebuilds the form with the manual card preset
	// flow, card, number, expiry, CVC, 3-D Secure, amount, currency, description, simulation, capture
	send(m, enter, enter, enter, enter, enter, enter, enter, enter, enter, enter, enter)
	if s.stage != stageReview || !strings.Contains(m.View(), "not possible in the terminal") {
		t.Fatalf("review must warn about 3-D Secure, stage %d:\n%s", s.stage, m.View())
	}
	send(m, enter)
	if s.stage != stageResult || s.result == nil || s.result.ResponseCode != "70000000" {
		t.Fatalf("expected 3-D Secure to be required, got stage %d %+v", s.stage, s.result)
	}
	if !strings.Contains(m.View(), "skip 3-D Secure") || !strings.Contains(m.View(), "esc back") {
		t.Errorf("result must explain the way out:\n%s", m.View())
	}
	transID := s.req.TransID
	send(m, esc)
	if s.stage != stageReview || s.req.TransID == transID || m.focus != focusContent {
		t.Fatalf("esc on the result should return to the review with a new trans ID, stage %d", s.stage)
	}
	send(m, shiftTab)
	if s.stage != stageForm || s.form == nil || !strings.Contains(m.View(), "Payment flow") {
		t.Fatalf("shift+tab on the review should show the form right away, stage %d:\n%s", s.stage, m.View())
	}
	send(m, enter, enter, enter, enter, enter, enter, enter, enter, enter, enter, enter, esc)
	if s.stage != stageForm || s.form == nil || !strings.Contains(m.View(), "Payment flow") {
		t.Fatalf("esc on the review should show the form right away, stage %d:\n%s", s.stage, m.View())
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
	firstPoll = time.Hour // keep waiting until the loop below polls
	send(m, enter)
	if s.stage != stageWaiting || !strings.Contains(m.View(), "4111 1111 1111 1111") || !strings.Contains(m.View(), "OTP 1234") {
		t.Errorf("the wait screen should list test cards for the payment page, stage %d:\n%s", s.stage, m.View())
	}
	for i := 0; i < 20 && s.stage == stageWaiting; i++ {
		send(m, run(s.poll(m.ctx, time.Millisecond)))
	}
	if s.stage != stageResult || s.result == nil || s.result.Status != "OK" {
		t.Fatalf("hosted payment did not complete: stage %d %+v", s.stage, s.result)
	}
	if !strings.Contains(m.View(), "PAYMENT APPROVED") || !strings.Contains(m.View(), "PAYPAL") {
		t.Errorf("result view:\n%s", m.View())
	}
	send(m, keys("d"))
	if !strings.Contains(m.View(), "Paid with PayPal") || !strings.Contains(m.View(), "on the payment page") {
		t.Errorf("detail card:\n%s", m.View())
	}
	// The duration stops with the final status and does not keep counting.
	s.waitStart = s.waitStart.Add(-time.Hour)
	if strings.Contains(m.View(), "1h") {
		t.Errorf("the duration must not grow after the payment completed:\n%s", m.View())
	}
}

// enterLinkForm opens a new payment link sent by e-mail and valid for 24
// hours and fills it up to the review.
func enterLinkForm(t *testing.T, m *model) *newPaymentScreen {
	t.Helper()
	send(m, keys("n"))
	s := m.current().(*newPaymentScreen)
	send(m, down, down, down)
	if v := ansi.Strip(m.View()); !strings.Contains(v, "/api/v2/payments/links") {
		t.Errorf("the form should show where the link request goes:\n%s", v)
	}
	send(m, enter)                                                                 // payment link
	send(m, enter)                                                                 // methods: all
	send(m, down, enter)                                                           // delivery: e-mail
	send(m, tea.KeyMsg{Type: tea.KeyRight}, tea.KeyMsg{Type: tea.KeyRight}, enter) // valid for 24 hours
	send(m, keys("nope"), enter)
	if s.stage != stageForm || !strings.Contains(ansi.Strip(m.View()), "enter an e-mail address") {
		t.Fatalf("an invalid address must stay in the form:\n%s", m.View())
	}
	for range 4 {
		send(m, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	send(m, keys("kunde@example.com"), enter, keys("Max Muster"), enter)
	send(m, enter)                             // template: Nexi's standard
	send(m, enter, enter, enter, enter, enter) // amount, currency, description, simulation, capture
	if s.stage != stageReview {
		t.Fatalf("expected review, got stage %d:\n%s", s.stage, m.View())
	}
	return s
}

func TestNewPaymentLink(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	s := enterLinkForm(t, m)
	view := ansi.Strip(m.View())
	for _, want := range []string{"Payment link", "e-mail to Max Muster · kunde@example.com", "Nexi standard", "Valid until"} {
		if !strings.Contains(view, want) {
			t.Errorf("review misses %q:\n%s", want, view)
		}
	}
	if s.req.ServiceType != "EMAIL" || s.req.ExpirationTime == "" || s.req.CustomerInfo.LastName != "Muster" {
		t.Errorf("link request = %+v %+v", s.req, s.req.CustomerInfo)
	}
	send(m, keys("j"))
	if v := ansi.Strip(m.View()); !strings.Contains(v, "POST") || !strings.Contains(v, "/payments/links") {
		t.Errorf("the request JSON should go to the link endpoint:\n%s", v)
	}

	firstPoll = time.Hour // keep waiting until the loop below polls
	defer func() { firstPoll = 500 * time.Millisecond }()
	send(m, enter)
	view = ansi.Strip(m.View())
	for _, want := range []string{"Link on its way to kunde@example.com", "valid until", "Link  c copies", "c copy link"} {
		if !strings.Contains(view, want) {
			t.Errorf("wait screen misses %q:\n%s", want, view)
		}
	}
	for i := 0; i < 20 && s.stage == stageWaiting; i++ {
		send(m, run(s.poll(m.ctx, time.Millisecond)))
	}
	if s.stage != stageResult || s.result == nil || s.result.Status != "OK" || s.result.Method().Type == "" {
		t.Fatalf("link payment did not end with the paid attempt: stage %d %+v", s.stage, s.result)
	}

	// Stopped waiting, the link stays valid and the list shows it as a link.
	send(m, keys("r"), enter)
	if s.stage != stageWaiting {
		t.Fatalf("expected waiting, got stage %d", s.stage)
	}
	send(m, esc)
	if !strings.Contains(s.note, "link stays valid") {
		t.Errorf("note = %q", s.note)
	}
	recs, _ := history.Load()
	if len(recs) != 2 || recs[0].Flow != "link" || recs[1].Flow != "link" {
		t.Fatalf("history = %+v", recs)
	}
	send(m, keys("l"))
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Payment link") {
		t.Errorf("the payments list should name the open link:\n%s", v)
	}
}

// An SMS needs the name of its template; the form asks for it.
func TestNewPaymentLinkSMSNeedsTemplate(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	send(m, keys("n"))
	s := m.current().(*newPaymentScreen)
	send(m, down, down, down, enter) // payment link
	send(m, enter)                   // methods: all
	send(m, down, down, enter)       // delivery: SMS
	send(m, enter)                   // valid for: Paygate default
	send(m, keys("+49 170 1234567"), enter, enter)
	send(m, enter) // no template
	if s.stage != stageForm || !strings.Contains(ansi.Strip(m.View()), "enter the template name") {
		t.Fatalf("an SMS without template must stay in the form:\n%s", m.View())
	}
	send(m, keys("shop_sms"), enter)
	send(m, enter, enter, enter, enter, enter)
	if s.stage != stageReview || s.req.ServiceType != "SMS" || s.req.ExternalTemplate != "shop_sms" {
		t.Fatalf("expected an SMS link with template in the review, got stage %d %+v", s.stage, s.req)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "SMS to +49 1701234567") || !strings.Contains(v, "shop_sms") {
		t.Errorf("review:\n%s", v)
	}
}

// On production the review of a link warns that real money moves, though
// the simulation is on by default there, and that the e-mail reaches a real
// person.
func TestNewPaymentLinkOnProduction(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-prod")
	i18n.Set(i18n.EN)
	s := enterLinkForm(t, m)
	view := ansi.Strip(m.View())
	for _, want := range []string{"real money will move", "does not apply the simulation to payment links", "Paygate sends a real message to kunde@example.com"} {
		if !strings.Contains(view, want) {
			t.Errorf("production review misses %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "no money moves") || s.simulated() {
		t.Errorf("a link must not be shown as simulated:\n%s", view)
	}
}

func TestPaletteStartsPaymentLink(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	send(m, tea.KeyMsg{Type: tea.KeyCtrlK}, keys("payment link"))
	if len(m.pal.matches) == 0 || m.pal.matches[0].label != "New payment link" {
		t.Fatalf("matches = %+v", m.pal.matches)
	}
	send(m, enter)
	s, ok := m.current().(*newPaymentScreen)
	if !ok || s.flow != "link" || s.stage != stageForm || s.form == nil {
		t.Fatalf("the palette should open the form for a payment link: %+v", m.current())
	}
}

// A click on the checkout URL opens it in the browser, in the wide and the
// narrow layout; a click elsewhere does not.
func TestClickOpensCheckoutURL(t *testing.T) {
	defer func() { firstPoll, openURL = 500*time.Millisecond, func(string) error { return nil } }()
	for _, size := range []tea.WindowSizeMsg{{Width: 120, Height: 34}, {Width: 90, Height: 44}} {
		t.Run(fmt.Sprintf("%dx%d", size.Width, size.Height), func(t *testing.T) {
			firstPoll = time.Hour
			m := setup(t, twoProfiles(), "shop-test")
			i18n.Set(i18n.EN)
			send(m, size)
			var opened []string
			openURL = func(url string) error { opened = append(opened, url); return nil }
			send(m, keys("n"))
			s := m.current().(*newPaymentScreen)
			send(m, down, enter, enter, enter, enter, enter, enter, enter, enter)
			if s.stage != stageWaiting {
				t.Fatalf("expected the wait screen, got stage %d", s.stage)
			}
			click := func(x, y int) {
				send(m, tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
			}
			// Click the first and the last line of the wrapped URL.
			lines := strings.Split(ansi.Strip(m.View()), "\n") // rendering places the URL
			chunks := strings.Split(hardWrap(s.checkout, s.urlArea.w), "\n")
			hits := 0
			for _, chunk := range []string{chunks[0], chunks[len(chunks)-1]} {
				for y, l := range lines {
					if i := strings.Index(l, chunk); i >= 0 {
						click(len([]rune(l[:i])), y)
						hits++
						break
					}
				}
			}
			if hits != 2 || len(opened) != 2 || opened[0] != s.checkout {
				t.Fatalf("clicks on the URL (%d) opened %v\n%s", hits, opened, m.View())
			}
			click(size.Width-3, 2) // the step header, not the URL
			if len(opened) != 2 {
				t.Error("a click beside the URL must not open it")
			}
		})
	}
}

func TestClickOpensAboutLinks(t *testing.T) {
	defer func() { openURL = func(string) error { return nil } }()
	for _, size := range []tea.WindowSizeMsg{{Width: 120, Height: 34}, {Width: 90, Height: 44}} {
		t.Run(fmt.Sprintf("%dx%d", size.Width, size.Height), func(t *testing.T) {
			m := setup(t, twoProfiles(), "shop-test")
			i18n.Set(i18n.EN)
			send(m, size)
			var opened []string
			openURL = func(url string) error { opened = append(opened, url); return nil }
			send(m, tea.KeyMsg{Type: tea.KeyUp}) // from Start around to Info; the menu keeps the keyboard
			if m.current().ID() != "about" || m.focus != focusMenu {
				t.Fatalf("expected Info with the menu focused, got %s", m.current().ID())
			}
			click := func(x, y int) {
				send(m, tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
			}
			lines := strings.Split(ansi.Strip(m.View()), "\n")
			for _, url := range []string{mollieCLIURL, paytestURL} {
				for y, l := range lines {
					if i := strings.Index(l, url); i >= 0 {
						x := len([]rune(l[:i]))
						click(x+len(url)+2, y) // beside the link
						click(x+len(url)-1, y)
						break
					}
				}
			}
			if len(opened) != 2 || opened[0] != mollieCLIURL || opened[1] != paytestURL {
				t.Fatalf("clicks on the links opened %v\n%s", opened, m.View())
			}
		})
	}
}

func TestSplashScreen(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	if !m.splash || !strings.Contains(m.View(), "██████╗") || !strings.Contains(m.View(), "verifying credentials") {
		t.Fatalf("expected the splash screen:\n%s", m.View())
	}
	send(m, run(verifySession(m.ctx)))
	if !m.ctx.Verified || !strings.Contains(m.View(), "credentials verified") {
		t.Errorf("verification not shown:\n%s", m.View())
	}
	send(m, keys("x"))
	if m.splash || m.current().ID() != "home" {
		t.Fatal("any key should close the splash without acting")
	}
	if !strings.Contains(m.View(), "█▀█ ▄▀█") || !strings.Contains(m.View(), "Credentials verified") {
		t.Errorf("home should show the small logo and the verification:\n%s", m.View())
	}
}

func TestResultReceipt(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-prod")
	i18n.Set(i18n.EN)
	send(m, keys("n"))
	s := m.current().(*newPaymentScreen)
	send(m, enter, enter, enter, enter, enter, enter, enter, enter)
	view := m.View()
	for _, want := range []string{"P A Y G A T E", "PAYMENT APPROVED", "0.01 EUR", "SIMULATED · Test:0000", "╲╱╲╱"} {
		if !strings.Contains(view, want) {
			t.Errorf("receipt misses %q:\n%s", want, view)
		}
	}
	send(m, keys("d"))
	if !s.details || !strings.Contains(m.View(), "Correlation") {
		t.Errorf("d should show the detail card:\n%s", m.View())
	}
}

// createPayment sends a payment directly and records it like the app does.
func createPayment(t *testing.T, m *model, in core.PaymentInput) *paygate.Payment {
	t.Helper()
	s := m.ctx.Session
	req, label, err := core.BuildPaymentRequest(in, s.Profile)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Client.CreatePayment(context.Background(), req)
	core.RecordPayment(s.Name, s.Profile, in.Flow, req, label, res, err)
	if err != nil {
		t.Fatal(err)
	}
	return res.Payment
}

func TestPaymentsListDetailAndCapture(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	p := createPayment(t, m, core.PaymentInput{Flow: core.FlowDirect, Card: "visa-ok", Capture: "manual", Amount: "20"})

	send(m, keys("l"))
	s := m.current().(*paymentsScreen)
	if !strings.Contains(m.View(), "20.00 EUR") || !strings.Contains(m.View(), "AUTHORIZED") {
		t.Fatalf("list view:\n%s", m.View())
	}
	send(m, enter)
	if s.mode != paysDetail || s.payment == nil || s.payment.PayID != p.PayID {
		t.Fatalf("detail not loaded: mode %d payment %+v", s.mode, s.payment)
	}
	if !s.can(paygate.OpCapture) || !s.can(paygate.OpReversal) || s.can(paygate.OpRefund) {
		t.Errorf("authorized payment: capture/cancel expected, no refund")
	}
	send(m, keys("c"), enter, enter) // amount (prefilled 20.00), confirm
	if s.payment.Amount.CapturedValue != 2000 {
		t.Fatalf("capture not applied: %+v (message %q)", s.payment.Amount, s.message)
	}
	if !s.can(paygate.OpRefund) || s.can(paygate.OpCapture) {
		t.Error("captured payment: refund expected, no capture")
	}
	bs := tea.KeyMsg{Type: tea.KeyBackspace}
	send(m, keys("f"), bs, bs, bs, bs, bs, keys("5"), enter, enter) // replace 20.00 with 5
	if s.payment.Amount.RefundedValue != 500 {
		t.Fatalf("partial refund not applied: %+v", s.payment.Amount)
	}
	rec, _ := history.Find("shop-test", p.PayID)
	kinds := ""
	for _, ev := range rec.Events {
		kinds += ev.Kind + " "
	}
	if !strings.Contains(kinds, "create") || !strings.Contains(kinds, "capture") || !strings.Contains(kinds, "refund") {
		t.Errorf("timeline = %s", kinds)
	}
	send(m, esc)
	if s.mode != paysList {
		t.Error("esc should return to the list")
	}
}

func TestPaymentsLookupAndFilter(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	t.Setenv("PAYGATE_NO_HISTORY", "1")
	p := createPayment(t, m, core.PaymentInput{Flow: core.FlowDirect, Card: "mc-ok", Amount: "9"})
	t.Setenv("PAYGATE_NO_HISTORY", "")

	send(m, keys("l"))
	s := m.current().(*paymentsScreen)
	if len(s.records) != 0 {
		t.Fatalf("history should be empty, got %d", len(s.records))
	}
	send(m, keys("g"), keys(p.PayID), enter)
	if s.mode != paysDetail || s.payment == nil || s.payment.TransID != p.TransID {
		t.Fatalf("lookup failed: %q mode %d", s.message, s.mode)
	}
	send(m, esc)
	if len(s.records) != 1 {
		t.Errorf("looked-up payment should join the history")
	}
	send(m, keys("/"), keys("nothing-like-this"), enter)
	if !strings.Contains(m.View(), "No payment matches") {
		t.Errorf("filter view:\n%s", m.View())
	}
	send(m, esc)
	if s.filter != "" {
		t.Error("esc should clear the filter")
	}
	send(m, keys("g"), keys("UNKNOWN123"), enter)
	if !strings.Contains(s.message, "not found") {
		t.Errorf("unknown ID message = %q", s.message)
	}
}

func TestFuzzyRanking(t *testing.T) {
	if _, ok := fuzzy("refu", "Sprache auf English umstellen"); ok {
		t.Error("scattered letters must not match")
	}
	a, _ := fuzzy("refu", "refund")
	b, ok := fuzzy("zahl", "Neue Zahlung")
	if a <= b || !ok {
		t.Errorf("word-prefix match should beat a later word match: %d vs %d", a, b)
	}
}

func TestCommandPalette(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	send(m, tea.KeyMsg{Type: tea.KeyCtrlK})
	if !m.pal.open || !strings.Contains(m.View(), "New payment") {
		t.Fatalf("palette not shown:\n%s", m.View())
	}
	send(m, keys("shop-prod"))
	if len(m.pal.matches) == 0 || !strings.Contains(m.pal.matches[0].label, "shop-prod") {
		t.Fatalf("matches = %+v", m.pal.matches)
	}
	send(m, enter)
	if m.pal.open || m.ctx.Session == nil || m.ctx.Session.Name != "shop-prod" {
		t.Fatalf("switch via palette failed: %+v", m.ctx.Session)
	}
	// Typing must not change the commands themselves.
	send(m, tea.KeyMsg{Type: tea.KeyCtrlK})
	before := len(m.pal.items)
	labels := map[string]int{}
	send(m, keys("z"), keys("a"), keys("h"), keys("l"))
	for _, c := range m.pal.items {
		if labels[c.label]++; labels[c.label] > 1 {
			t.Errorf("filtering changed the commands: %q twice", c.label)
		}
	}
	if len(m.pal.items) != before {
		t.Errorf("filtering changed the number of commands: %d → %d", before, len(m.pal.items))
	}
	send(m, esc)
	send(m, tea.KeyMsg{Type: tea.KeyCtrlK}, keys("3ds challenge"))
	if len(m.pal.matches) == 0 || !strings.Contains(m.pal.matches[0].label, "4000 0128 9268 8323") {
		t.Errorf("the palette should offer to copy the 3DS challenge card, got %+v", m.pal.matches)
	}
	send(m, esc)
	send(m, tea.KeyMsg{Type: tea.KeyCtrlP}, keys("look"), enter)
	if m.current().ID() != "payments" || m.current().(*paymentsScreen).form == nil {
		t.Error("look up should open the lookup form")
	}
}

func TestFactoryReset(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	createPayment(t, m, core.PaymentInput{Flow: core.FlowDirect, Card: "visa-ok"})
	dir, _ := config.Dir()

	send(m, keys("s"), keys("k")) // settings, up wraps to the last entry
	s := m.current().(*settingsScreen)
	if !strings.Contains(m.View(), "Delete all data") {
		t.Fatalf("settings must offer the factory reset:\n%s", m.View())
	}
	send(m, enter)
	if s.form == nil || !strings.Contains(m.View(), "Delete all paygate data?") {
		t.Fatalf("expected the confirmation:\n%s", m.View())
	}
	send(m, enter) // "No" is preselected
	if _, err := os.Stat(dir); err != nil || m.ctx.Session == nil {
		t.Fatalf("declining must not delete anything: %v", err)
	}

	// From the command palette, then confirm.
	send(m, tea.KeyMsg{Type: tea.KeyCtrlK}, keys("factory reset"), enter)
	if m.current().ID() != "settings" || s.form == nil || s.editing != "reset" {
		t.Fatalf("the palette should open the reset confirmation, got %s", m.current().ID())
	}
	send(m, tea.KeyMsg{Type: tea.KeyLeft}, enter)
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("config directory still exists: %v", err)
	}
	if m.ctx.Session != nil || len(m.ctx.Cfg.Profiles) != 0 || m.current().ID() != "home" {
		t.Fatalf("expected a fresh start on the home screen, got %s session %+v", m.current().ID(), m.ctx.Session)
	}
	if !strings.Contains(m.View(), "Welcome to paygate") {
		t.Errorf("home should welcome a new user:\n%s", m.View())
	}
	if recs, _ := history.Load(); len(recs) != 0 {
		t.Errorf("history must be empty, got %d records", len(recs))
	}
}

func TestInspectorShowsRequests(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	createPayment(t, m, core.PaymentInput{Flow: core.FlowDirect, Card: "visa-ok"})
	send(m, keys("i"))
	view := m.View()
	if !strings.Contains(view, "POST") || !strings.Contains(view, "/payments") || !strings.Contains(view, "201") {
		t.Fatalf("inspector view:\n%s", view)
	}
	send(m, keys("v")) // request view
	s := m.current().(*inspectorScreen)
	for i := 0; i < 5; i++ {
		send(m, tea.KeyMsg{Type: tea.KeyPgDown})
	}
	if strings.Contains(m.View(), "4111111111111111") {
		t.Error("the inspector must not show full card numbers")
	}
	if s.response {
		t.Error("v should switch to the request")
	}
	send(m, keys("x"), tea.KeyMsg{Type: tea.KeyLeft}, enter) // confirm "Yes"
	if len(s.entries) != 0 {
		t.Errorf("clear left %d entries", len(s.entries))
	}
}

// The payment detail lists every API call of the payment; enter shows a
// call with its request and response, [ and ] step through them.
func TestPaymentDetailShowsCalls(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	createPayment(t, m, core.PaymentInput{Flow: core.FlowDirect, Card: "visa-ok", Capture: "manual", Amount: "20"})
	send(m, keys("l"), enter)
	s := m.current().(*paymentsScreen)
	view := ansi.Strip(m.View())
	for _, want := range []string{"TIMELINE", "2 API calls", "POST", "payment AUTHORIZED", "getByPayId"} {
		if !strings.Contains(view, want) {
			t.Errorf("detail misses %q:\n%s", want, view)
		}
	}
	if e := s.calls[s.callSel].entry; e == nil || !strings.Contains(e.URL, "getByPayId") {
		t.Errorf("the newest call should be selected, got %+v", s.calls[s.callSel])
	}
	send(m, keys("c"), enter, enter) // capture 20.00
	if v := ansi.Strip(m.View()); !strings.Contains(v, "capture 20.00 EUR") {
		t.Errorf("the capture should be a call of its own:\n%s", v)
	}

	for range s.callSel { // up to the first call; one more would wrap around
		send(m, tea.KeyMsg{Type: tea.KeyUp})
	}
	send(m, enter)
	view = ansi.Strip(m.View())
	if !s.callOpen || !strings.Contains(view, "call 1 of 4") || !strings.Contains(view, "RESPONSE") || !strings.Contains(view, `"responseCode": "00000000"`) {
		t.Fatalf("the creation should open with its response:\n%s", view)
	}
	if hints := s.Hints(m.ctx); hints[0].Key != "[ ]" {
		t.Errorf("hints = %+v", hints)
	}
	send(m, keys("v"))
	if view = ansi.Strip(m.View()); !strings.Contains(view, `"transId"`) {
		t.Errorf("v should show the request:\n%s", view)
	}
	pgdn := tea.KeyMsg{Type: tea.KeyPgDown}
	send(m, pgdn, pgdn)
	if view = ansi.Strip(m.View()); !strings.Contains(view, "••••••1111") || strings.Contains(view, "4111111111111111") {
		t.Errorf("further down, the card number shows masked:\n%s", view)
	}
	send(m, keys("]"))
	if v := ansi.Strip(m.View()); !strings.Contains(v, "call 2 of") || !strings.Contains(v, "getByPayId") {
		t.Errorf("] should step to the lookup:\n%s", v)
	}
	send(m, esc)
	if s.callOpen || s.mode != paysDetail {
		t.Fatalf("esc should close the call: open %v mode %d", s.callOpen, s.mode)
	}
	send(m, esc)
	if s.mode != paysList {
		t.Error("a second esc should return to the list")
	}
}

// Calls that left the request log still show from the payment's history.
func TestPaymentDetailCallsLeftTheLog(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	createPayment(t, m, core.PaymentInput{Flow: core.FlowDirect, Card: "visa-ok"})
	if err := reqlog.Clear(); err != nil {
		t.Fatal(err)
	}
	send(m, keys("l"), enter)
	s := m.current().(*paymentsScreen)
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "not in the log") || !strings.Contains(view, "Older calls have left the request log") {
		t.Fatalf("the creation should come from the history:\n%s", view)
	}
	s.callSel = 0
	send(m, enter)
	if s.callOpen || !strings.Contains(m.toast, "no longer in the request log") {
		t.Errorf("a call that left the log cannot open: open %v toast %q", s.callOpen, m.toast)
	}
}

// Webhooks and history rows use the time layout of the calls, so the
// columns of a payment's timeline line up.
func TestTimelineRowsShareTheTimeLayout(t *testing.T) {
	i18n.Set(i18n.EN)
	at := time.Date(2026, 10, 26, 17, 46, 46, 0, time.Local)
	entry := reqlog.Entry{Time: at, Method: "GET", URL: "https://x/api/v2/payments/getByPayId/ABC", Status: 200}
	for _, r := range []callRow{
		{entry: &entry},
		{hook: &listen.Event{Time: at, Kind: "enhanced"}},
		{event: &history.Event{Time: at, Kind: "create"}},
	} {
		if line := ansi.Strip(callLine(r, "", "EUR", "15:04:05", 120)); !strings.HasPrefix(line, "17:46:46  ") {
			t.Errorf("row starts %q", line)
		}
	}
}

// A webhook in a payment's timeline opens in the Webhooks screen.
func TestWebhooksScreenOpensWebhookFromPayment(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	send(m, switchTo("webhooks", listen.Event{ID: 7, Profile: "shop-test", TransID: "TID1", Kind: "enhanced", Time: time.Now()})())
	w, ok := m.current().(*webhooksScreen)
	if !ok || w.mode != hooksDetail || w.cur.ID != 7 {
		t.Fatalf("the webhook should open in its detail: %+v", m.current())
	}
}

// The request JSON in the review scrolls like a body in the Inspector and
// stops at its end.
func TestReviewJSONScrolls(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	send(m, keys("n"))
	// flow (direct), card (visa-ok), amount, currency, description, simulation, capture
	send(m, enter, enter, enter, enter, enter, enter, enter)
	s := m.current().(*newPaymentScreen)
	if s.stage != stageReview {
		t.Fatalf("expected review, got stage %d", s.stage)
	}
	if strings.Contains(ansi.Strip(m.View()), "↑↓ scroll") {
		t.Error("the scroll hint belongs to the open JSON")
	}
	send(m, keys("j"))
	view := ansi.Strip(m.View())
	if !strings.Contains(view, `"transId"`) || !strings.Contains(view, "more lines (pgdn)") || !strings.Contains(view, "↑↓ scroll") {
		t.Fatalf("the JSON should start at the top and count the hidden lines:\n%s", view)
	}
	send(m, tea.KeyMsg{Type: tea.KeyPgDown})
	if s.jsonScroll != 10 || strings.Contains(ansi.Strip(m.View()), `"transId"`) {
		t.Errorf("pgdn should scroll 10 lines, at line %d", s.jsonScroll)
	}
	for i := 0; i < 10; i++ {
		send(m, tea.KeyMsg{Type: tea.KeyPgDown})
	}
	if view := ansi.Strip(m.View()); strings.Contains(view, "more lines") {
		t.Errorf("the end of the JSON should be visible:\n%s", view)
	}
	end := s.jsonScroll
	send(m, down)
	_ = m.View()
	if s.jsonScroll != end {
		t.Errorf("scrolling should stop at the end (line %d), got %d", end, s.jsonScroll)
	}
	send(m, tea.KeyMsg{Type: tea.KeyUp})
	if s.jsonScroll != end-1 {
		t.Errorf("up should scroll one line back, at line %d", s.jsonScroll)
	}
	for i := 0; i < 10; i++ {
		send(m, tea.KeyMsg{Type: tea.KeyPgUp})
	}
	if s.jsonScroll != 0 || !strings.Contains(ansi.Strip(m.View()), `"transId"`) {
		t.Errorf("pgup should return to the top, at line %d", s.jsonScroll)
	}
}

// The QR code must survive every window width: no line may be wider than the
// content area, otherwise the frame wraps it and splits the code.
func TestWaitViewKeepsQRIntact(t *testing.T) {
	i18n.Set(i18n.DE) // German has the longest hint line
	defer i18n.Set(i18n.EN)
	url := "https://paygate.nexigroup.com/paymentpage.aspx?token=b4ac9e31bb06466d828b4b7a657f1d9b"
	s := newNewPaymentScreen()
	s.flow, s.checkout, s.waitStart = string(core.FlowHosted), url, time.Now()
	s.req = &paygate.PaymentRequest{TransID: "TIDMVGQ1UW516PPDI", Amount: paygate.Amount{Value: 1, Currency: "EUR"}}
	qrLines := strings.Split(ui.QR(url), "\n")
	ctx := &Context{} // test system: the test card hint takes room as well
	for w := 60; w <= 140; w++ {
		for _, h := range []int{18, 22, 26, 30} {
			view := fit(s.waitView(ctx, w, h), w, h)
			lines := strings.Split(view, "\n")
			if !strings.Contains(view, qrLines[0]) {
				continue // too small: the z hint is shown instead
			}
			for i, ql := range qrLines {
				if i >= len(lines) || !strings.Contains(lines[i], ql) {
					t.Fatalf("w=%d h=%d: QR row %d broken:\n%s", w, h, i, view)
				}
			}
		}
	}
	if view := s.waitView(ctx, 120, 26); !strings.Contains(view, qrLines[0]) {
		t.Error("a 120 x 30 window must show the QR code next to the text")
	}
}

// startListening opens the app's listener without the spinner in between.
// The mock server runs on this computer, so no tunnel is needed.
func startListening(t *testing.T, m *model) *webhooksScreen {
	t.Helper()
	send(m, keys("w"))
	s := m.current().(*webhooksScreen)
	open, err := s.start(m.ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeListen(m.ctx) })
	send(m, open())
	if m.ctx.Listen == nil {
		t.Fatalf("listener not open: %s", s.message)
	}
	return s
}

// hookFor is the Enhanced webhook payload of a payment of the mock.
func hookFor(p *paygate.Payment, value int64) webhooks.Payload {
	pl := webhooks.FromPayment(p, time.Now())
	pl.Amount = webhooks.Amount{Value: value, Currency: "EUR"}
	return pl
}

// postHook posts a signed Enhanced webhook to the app's listener and passes
// the stored event to the app, as the running program does.
func postHook(t *testing.T, m *model, s *webhooksScreen, pl webhooks.Payload, key string) listen.Event {
	t.Helper()
	before, _ := listen.Load()
	msg, err := webhooks.NewEnhanced(key, pl, time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, m.ctx.Listen.URL, bytes.NewReader(msg.Body))
	for _, h := range msg.Headers() {
		req.Header.Set(h[0], h[1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("listener answered %d", resp.StatusCode)
	}
	// The listener stores the webhook before it reports it.
	all, _ := listen.Load()
	if len(all) != len(before)+1 {
		t.Fatalf("stored webhooks: %d, want %d", len(all), len(before)+1)
	}
	send(m, webhookMsg{ev: all[0], ch: s.incoming})
	return all[0]
}

func TestWebhooksListenReceiveAndDetail(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	t.Setenv("PAYGATE_HMAC_KEY", "demo-hmac-key")
	p := createPayment(t, m, core.PaymentInput{Flow: core.FlowDirect, Card: "visa-ok", Amount: "12.50"})

	send(m, keys("w"))
	if v := ansi.Strip(m.View()); !strings.Contains(v, "not listening · space starts a listener for the mock server") || !strings.Contains(v, "No webhooks yet") {
		t.Fatalf("webhooks before listening:\n%s", v)
	}
	s := startListening(t, m)
	v := ansi.Strip(m.View())
	for _, want := range []string{"listening in this app · this computer only", "URL  http://127.0.0.1:", "/paygate/wh/", "● w"} {
		if !strings.Contains(v, want) {
			t.Errorf("listening view misses %q:\n%s", want, v)
		}
	}
	if st, ok := listen.For("shop-test"); !ok || st.URL != m.ctx.Listen.URL {
		t.Errorf("other commands should find the listener: %+v", st)
	}

	ev := postHook(t, m, s, hookFor(p, 1250), "demo-hmac-key")
	if ev.Signature != "valid" || ev.TransID != p.TransID {
		t.Fatalf("stored event: %+v", ev)
	}
	v = ansi.Strip(m.View())
	for _, want := range []string{"1 received", "ENHANCED", "✓ valid", "12.50 EUR", p.TransID[:12]} {
		if !strings.Contains(v, want) {
			t.Errorf("list misses %q:\n%s", want, v)
		}
	}
	if !strings.Contains(m.toast, "Webhook received: ENHANCED · ✓ valid") {
		t.Errorf("toast = %q", m.toast)
	}

	send(m, enter)
	v = ansi.Strip(m.View())
	for _, want := range []string{"← Webhooks / #1", "the payment result, signed with HMAC-SHA256", p.PayID, "CHECKS", "HEADERS", "X-Paygate-Signature: v1=", "BODY · "} {
		if !strings.Contains(v, want) {
			t.Errorf("detail misses %q:\n%s", want, v)
		}
	}
	if !s.check.Valid {
		t.Errorf("detail check: %+v", s.check)
	}

	// r sends it again, unchanged, to another endpoint.
	var got http.Header
	var gotBody []byte
	handler := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, gotBody = r.Header.Clone(), must(io.ReadAll(r.Body))
		w.WriteHeader(http.StatusAccepted)
	}))
	defer handler.Close()
	s.target = handler.URL + "/paygate/webhook"
	send(m, keys("r"), enter)
	if !strings.Contains(s.message, "202 Accepted · your endpoint accepted it") {
		t.Fatalf("resend message %q:\n%s", s.message, ansi.Strip(m.View()))
	}
	if got.Get(webhooks.HeaderSignature) != ev.Header.Get(webhooks.HeaderSignature) || !bytes.Equal(gotBody, ev.Body) {
		t.Errorf("resent webhook differs: %v %s", got, gotBody)
	}

	// g opens the payment, whose detail lists the webhook.
	send(m, keys("g"))
	ps, ok := m.current().(*paymentsScreen)
	if !ok || ps.mode != paysDetail || ps.rec.TransID != p.TransID {
		t.Fatalf("g should open the payment, got %s", m.current().ID())
	}
	v = ansi.Strip(m.View())
	if !strings.Contains(v, "1 · ENHANCED · ✓ valid") || !strings.Contains(v, "webhook") {
		t.Errorf("payment detail misses the webhook:\n%s", v)
	}

	// A webhook stored elsewhere, e.g. by paygate listen in another
	// terminal, shows up with the next tick.
	if err := listen.Append(&listen.Event{Time: time.Now(), Profile: "shop-test", Kind: webhooks.Basic, PayID: p.PayID, Signature: "missing", Answer: 200}); err != nil {
		t.Fatal(err)
	}
	send(m, listenTickMsg{})
	if v = ansi.Strip(m.View()); !strings.Contains(v, "2 · BASIC · – none") {
		t.Errorf("payment detail after a second webhook:\n%s", v)
	}

	send(m, keys("w"), esc, keys(" "))
	if m.ctx.Listen != nil || m.ctx.Listening != nil {
		t.Fatal("space should stop listening")
	}
	if _, ok := listen.Running(); ok {
		t.Error("the listener state should be gone")
	}
	if v = ansi.Strip(m.View()); !strings.Contains(v, "not listening") || strings.Contains(v, "● w") {
		t.Errorf("view after stopping:\n%s", v)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestWebhooksOtherTerminalAndNoCloudflared(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	send(m, keys("w"))
	s := m.current().(*webhooksScreen)

	// Without the mock, listening needs a tunnel.
	t.Setenv("PAYGATE_BASE_URL", "https://paygate.example.invalid")
	t.Setenv("PAYGATE_CLOUDFLARED", filepath.Join(t.TempDir(), "missing"))
	if v := ansi.Strip(m.View()); !strings.Contains(v, "space opens a cloudflared tunnel") {
		t.Fatalf("view:\n%s", v)
	}
	send(m, keys(" "))
	if m.ctx.Listen != nil || !strings.Contains(ansi.Strip(m.View()), "cloudflared is not installed") || !strings.Contains(ansi.Strip(m.View()), listen.InstallHint()) {
		t.Fatalf("missing cloudflared should show how to install it:\n%s", ansi.Strip(m.View()))
	}

	// A listener in another terminal, here the test runner's parent.
	dir, _ := config.Dir()
	st, _ := json.Marshal(listen.State{Profile: "shop-test", URL: "https://demo.trycloudflare.com/paygate/wh/0123456789ab", PID: os.Getppid(), Started: time.Now()})
	if err := os.WriteFile(filepath.Join(dir, "listen.json"), st, 0o600); err != nil {
		t.Fatal(err)
	}
	send(m, listenTickMsg{})
	v := ansi.Strip(m.View())
	for _, want := range []string{fmt.Sprintf("listening in another terminal (process %d)", os.Getppid()), "https://demo.trycloudflare.com/paygate/wh/0123456789ab", "● w"} {
		if !strings.Contains(v, want) {
			t.Errorf("view misses %q:\n%s", want, v)
		}
	}
	send(m, keys(" "))
	if m.ctx.Listen != nil || !strings.Contains(s.message, "already runs for shop-test in another terminal") {
		t.Errorf("a second listener must not start: %q", s.message)
	}

	// The dot belongs to the active profile's listener only.
	send(m, keys("p"))
	ps := m.current().(*profilesScreen)
	for i := 0; i < 3 && m.ctx.Cfg.Names()[ps.sel] != "shop-prod"; i++ {
		send(m, keys("j"))
	}
	send(m, enter, listenTickMsg{})
	if m.ctx.Session.Name != "shop-prod" || m.ctx.Listening != nil || strings.Contains(ansi.Strip(m.View()), "● w") {
		t.Errorf("after switching to shop-prod: listening %+v", m.ctx.Listening)
	}
}

func TestWebhooksPaletteCommands(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	t.Setenv("PAYGATE_HMAC_KEY", "")
	p := createPayment(t, m, core.PaymentInput{Flow: core.FlowDirect, Card: "visa-ok", Amount: "5"})
	s := startListening(t, m)
	postHook(t, m, s, hookFor(p, 500), "some-key")
	if ev, _ := listen.Find(1); ev.Signature != "unchecked" {
		t.Errorf("without a key the signature stays unchecked: %+v", ev)
	}
	send(m, esc, tea.KeyMsg{Type: tea.KeyCtrlK}, keys("stop listening"), enter)
	if m.ctx.Listen != nil {
		t.Fatal("the palette should stop listening")
	}
	send(m, tea.KeyMsg{Type: tea.KeyCtrlK}, keys("delete the received"), enter)
	if all, _ := listen.Load(); len(all) != 0 || !strings.Contains(m.toast, "Deleted 1 received webhooks") {
		t.Errorf("clear via palette: %d left, toast %q", len(all), m.toast)
	}
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// q quits at once, unless quitting would end a listener or the wait for a
// payment; then it asks first.
func TestQuitAsksWhileSomethingRuns(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	if !isQuit(m.handleKey(keys("q"))) {
		t.Fatal("with nothing running q should quit at once")
	}

	startListening(t, m)
	if isQuit(m.handleKey(keys("q"))) || m.quitAsk == nil {
		t.Fatal("while listening q should ask first")
	}
	v := ansi.Strip(m.View())
	for _, want := range []string{"Quit paygate?", "listening for webhooks", "enter quits · esc stays"} {
		if !strings.Contains(v, want) {
			t.Errorf("question misses %q:\n%s", want, v)
		}
	}
	send(m, esc)
	if m.quitAsk != nil || m.ctx.Listen == nil {
		t.Fatal("esc should keep the app and the listener")
	}
	m.handleKey(keys("q"))
	if !isQuit(m.handleKey(enter)) {
		t.Error("enter should quit after the question")
	}

	// Waiting for a hosted payment counts too; the palette asks as well.
	send(m, esc)
	m.current().(*webhooksScreen).stop(m.ctx)
	m.screens[m.indexOf("new")].(*newPaymentScreen).stage = stageWaiting
	send(m, tea.KeyMsg{Type: tea.KeyCtrlK}, keys("quit paygate"), enter)
	if m.quitAsk == nil || !strings.Contains(ansi.Strip(m.View()), "waiting for a payment") {
		t.Fatalf("the palette should ask while waiting for a payment:\n%s", ansi.Strip(m.View()))
	}
	if !isQuit(m.handleKey(keys("q"))) {
		t.Error("a second q should quit")
	}
}

// The list columns are as wide as their longest value, so a long status,
// amount or an older date does not push the IDs out of line.
func TestWebhooksListColumnsLineUp(t *testing.T) {
	m := setup(t, twoProfiles(), "shop-test")
	i18n.Set(i18n.EN)
	now := time.Now()
	for i, e := range []listen.Event{
		{Time: now.Add(-26 * time.Hour), Kind: webhooks.Enhanced, Status: "OK", Amount: &webhooks.Amount{Value: 1, Currency: "EUR"}, TransID: "TIDOLDER0000000001"},
		{Time: now.Add(-time.Minute), Kind: webhooks.Enhanced, Status: "AUTHORIZED", Amount: &webhooks.Amount{Value: 123456789, Currency: "EUR"}, TransID: "TIDAUTHORIZED00002"},
		{Time: now, Kind: webhooks.Basic, PayID: "0123456789abcdef0123456789abcdef"},
	} {
		e.Profile, e.Signature, e.Answer = "shop-test", []string{"valid", "valid", "missing"}[i], 200
		if err := listen.Append(&e); err != nil {
			t.Fatal(err)
		}
	}
	send(m, keys("w"))
	column := func(line, s string) int {
		i := strings.Index(line, s)
		if i < 0 {
			return -1
		}
		return lipgloss.Width(line[:i])
	}
	v := ansi.Strip(m.View())
	var want int
	ids := []string{"TIDOLDER0000000001", "TIDAUTHORIZED00002", "0123456789abcdef"}
	for _, line := range strings.Split(v, "\n") {
		if c := column(line, "TRANS / PAY ID"); c >= 0 {
			want = c
		}
		for _, id := range ids {
			if c := column(line, id); c >= 0 && c != want {
				t.Errorf("%s starts at column %d, the heading at %d:\n%s", id, c, want, v)
			}
		}
	}
	if want == 0 || !strings.Contains(v, "AUTHORIZED  1,234,567.89 EUR") && !strings.Contains(v, "AUTHORIZED  1234567.89 EUR") {
		t.Errorf("status and amount need a gap:\n%s", v)
	}
}
