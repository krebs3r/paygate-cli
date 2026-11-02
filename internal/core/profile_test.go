package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/history"
	"github.com/krebs3r/paygate-cli/internal/listen"
	"github.com/krebs3r/paygate-cli/internal/reqlog"
)

func TestRenameProfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("PAYGATE_NO_KEYCHAIN", "1")
	t.Setenv("PAYGATE_NO_HISTORY", "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"shop-test", "other"} {
		p := &config.Profile{Brand: "nexi", Env: "test", MerchantID: "demo-shop", Auth: "basic"}
		if _, err := config.StoreAPIKey(n, p, "key-of-"+n); err != nil {
			t.Fatal(err)
		}
		cfg.Profiles[n] = p
	}
	cfg.ActiveProfile = "shop-test"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	for _, r := range []history.Record{{Profile: "shop-test", TransID: "TFIRST"}, {Profile: "shop-test", TransID: "TSECOND"}, {Profile: "other", TransID: "TOTHER"}} {
		if err := history.Upsert(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := listen.Append(&listen.Event{Profile: "shop-test", PayID: "p1"}); err != nil {
		t.Fatal(err)
	}
	if err := reqlog.Append(reqlog.Entry{Profile: "shop-test", Method: "GET", URL: "https://x/1"}); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{"other", "no spaces", ""} {
		if _, err := RenameProfile(cfg, "shop-test", bad); err == nil {
			t.Errorf("rename to %q must fail", bad)
		}
	}
	res, err := RenameProfile(cfg, "shop-test", "nexi-test")
	if err != nil {
		t.Fatal(err)
	}
	if res != (RenameResult{Payments: 2, Webhooks: 1, Calls: 1}) {
		t.Errorf("moved = %+v", res)
	}
	reloaded, _ := config.Load()
	if _, ok := reloaded.Profiles["shop-test"]; ok || reloaded.ActiveProfile != "nexi-test" {
		t.Fatalf("config after rename: active %q, profiles %v", reloaded.ActiveProfile, reloaded.Names())
	}
	if key, err := config.APIKey("nexi-test", reloaded.Profiles["nexi-test"]); err != nil || key != "key-of-shop-test" {
		t.Errorf("key after rename = %q, %v", key, err)
	}
	if r, ok := history.Find("nexi-test", "TSECOND"); !ok || r.Profile != "nexi-test" {
		t.Errorf("payment not moved: %+v", r)
	}
	if _, ok := history.Find("other", "TOTHER"); !ok {
		t.Error("the other profile's payment must stay")
	}
	events, _ := listen.Load()
	entries, _ := reqlog.Load()
	if len(events) != 1 || events[0].Profile != "nexi-test" || len(entries) != 1 || entries[0].Profile != "nexi-test" {
		t.Errorf("webhooks %+v, calls %+v", events, entries)
	}

	// A running listener keeps its profile name.
	st, _ := json.Marshal(listen.State{Profile: "nexi-test", URL: "https://example.trycloudflare.com/x", PID: os.Getpid()})
	if err := os.WriteFile(filepath.Join(dir, "paygate-cli", "listen.json"), st, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RenameProfile(cfg, "nexi-test", "again"); err == nil || !strings.Contains(err.Error(), "stop it first") {
		t.Errorf("rename while listening: %v", err)
	}
	if _, ok := cfg.Profiles["nexi-test"]; !ok {
		t.Error("a refused rename must leave the profile")
	}
}
