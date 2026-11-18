package demo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/paygate"
)

func pay(t *testing.T, transID string) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	s, err := core.OpenSession(cfg, "", core.ClientOptions{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Client.CreatePayment(context.Background(), &paygate.PaymentRequest{
		TransID: transID, Amount: paygate.Amount{Value: 990, Currency: "EUR"}, BrowserInfo: &paygate.BrowserInfo{IPAddress: "127.0.0.1"},
		PaymentMethods: &paygate.PaymentMethods{Type: "CARD", IntegrationType: "DIRECT", Card: &paygate.Card{
			Number: "4111111111111111", Brand: "VISA", ThreeDsPolicy: &paygate.ThreeDsPolicy{Skip: "THIS_TRANSACTION"},
		}},
	}); err != nil {
		t.Fatal(err)
	}
}

func lookup(t *testing.T, transID string) error {
	t.Helper()
	cfg, _ := config.Load()
	s, err := core.OpenSession(cfg, "", core.ClientOptions{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Client.GetByTransID(context.Background(), transID)
	return err
}

func TestStartKeepsPaymentsAndResetForgetsThem(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Setenv("PAYGATE_NO_KEYCHAIN", "")
	t.Setenv("PAYGATE_BASE_URL", "")
	t.Cleanup(Stop)

	p, err := Start()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.URL, "http://127.0.0.1:") || Running() != p || !config.Demo() {
		t.Fatalf("demo Paygate at %s, running=%v, demo=%v", p.URL, Running() != nil, config.Demo())
	}
	cfg, _ := config.Load()
	prof := cfg.Profiles[ProfileName]
	if cfg.ActiveProfile != ProfileName || prof == nil || !prof.IsDemo() || prof.SecretStore != config.StoreFile || prof.HMACStore != config.StoreFile {
		t.Fatalf("demo profile = %+v", prof)
	}
	pay(t, "TDEMO1")

	// Another paygate process finds the payment.
	Stop()
	if config.Demo() {
		t.Fatal("Stop leaves demo mode")
	}
	if _, err := Start(); err != nil {
		t.Fatal(err)
	}
	if err := lookup(t, "TDEMO1"); err != nil {
		t.Fatalf("the demo keeps its payments between processes: %v", err)
	}

	if err := Reset(); err != nil {
		t.Fatal(err)
	}
	if err := lookup(t, "TDEMO1"); err == nil {
		t.Error("reset forgets the demo payments")
	}
	if cfg, _ := config.Load(); cfg.Profiles[ProfileName] == nil {
		t.Error("reset in demo mode sets up the demo profile again")
	}
	Stop()
	if _, err := os.Stat(filepath.Join(base, "paygate-cli", "config.toml")); !os.IsNotExist(err) {
		t.Error("the demo must not create a real configuration")
	}
}

func TestScenariosAreKeptAndApplied(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PAYGATE_BASE_URL", "")
	t.Cleanup(Stop)

	var sc Scenarios
	if err := sc.Set("checkout", "nope"); err == nil {
		t.Error("unknown values are refused")
	}
	if err := sc.Set("fail-next", "401"); err != nil {
		t.Fatal(err)
	}
	if err := SaveScenarios(sc); err != nil {
		t.Fatal(err)
	}
	if _, err := Start(); err != nil {
		t.Fatal(err)
	}
	if err := lookup(t, "TX"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("the saved one-time error applies to the first call: %v", err)
	}
	if got, _ := LoadScenarios(); got.Get("fail-next") != "none" {
		t.Errorf("after the call the error is used up, also on disk: %+v", got)
	}

	// Another paygate process changes a scenario; this one picks it up.
	other := Scenarios{}
	_ = other.Set("webhook", "off")
	time.Sleep(20 * time.Millisecond)
	scnMu.Lock()
	_ = saveScenarios(other)
	scnMu.Unlock()
	_ = lookup(t, "TX")
	if Running().Scenarios().Get("webhook") != "off" {
		t.Error("a change on disk reaches the running demo Paygate")
	}

	if err := Reset(); err != nil {
		t.Fatal(err)
	}
	if Running().Scenarios() != (Scenarios{}) {
		t.Error("reset goes back to the default scenarios")
	}
}
