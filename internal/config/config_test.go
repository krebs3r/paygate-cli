package config

import (
	"os"
	"testing"
)

func TestSaveLoadResolve(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PAYGATE_NO_KEYCHAIN", "1")
	t.Setenv("PAYGATE_PROFILE", "")
	t.Setenv("PAYGATE_API_KEY", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := cfg.Resolve(""); err != ErrNoProfile {
		t.Fatalf("empty config should report ErrNoProfile, got %v", err)
	}

	p := &Profile{Brand: "nexi", Env: "test", MerchantID: "shop", Auth: "basic"}
	if store, _ := StoreAPIKey("shop-test", p, "secret"); store != StoreFile {
		t.Fatalf("store = %s", store)
	}
	cfg.Profiles["shop-test"] = p
	cfg.ActiveProfile = "shop-test"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(cfg.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config permissions = %v, want 0600", info.Mode().Perm())
	}

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	name, lp, err := loaded.Resolve("")
	if err != nil || name != "shop-test" || lp.MerchantID != "shop" {
		t.Fatalf("resolve = %s %+v %v", name, lp, err)
	}
	if key, err := APIKey(name, lp); err != nil || key != "secret" {
		t.Fatalf("api key = %q %v", key, err)
	}
	t.Setenv("PAYGATE_API_KEY", "override")
	if key, _ := APIKey(name, lp); key != "override" {
		t.Errorf("PAYGATE_API_KEY should win, got %q", key)
	}
	if lp.Return() != DefaultReturnURL || lp.DefaultCurrency() != "EUR" {
		t.Errorf("defaults: %s %s", lp.Return(), lp.DefaultCurrency())
	}
	if _, _, err := loaded.Resolve("missing"); err == nil {
		t.Error("unknown profile should fail")
	}
}

func TestLegacyWebhookURLDropped(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Profiles["old"] = &Profile{Brand: "nexi", Env: "test", MerchantID: "shop", WebhookURL: legacyWebhookURL + "/"}
	cfg.Profiles["own"] = &Profile{Brand: "nexi", Env: "test", MerchantID: "shop", WebhookURL: "https://shop.example.com/paygate/webhook"}
	cfg.Profiles["none"] = &Profile{Brand: "nexi", Env: "test", MerchantID: "shop"}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if p := loaded.Profiles["old"]; p.WebhookURL != "" || p.Webhook() != DefaultWebhookURL {
		t.Errorf("old default kept: %q → %s", p.WebhookURL, p.Webhook())
	}
	if p := loaded.Profiles["own"]; p.Webhook() != "https://shop.example.com/paygate/webhook" {
		t.Errorf("own webhook URL lost: %s", p.Webhook())
	}
	if p := loaded.Profiles["none"]; p.Webhook() != "https://webhook.invalid/paygate-cli" {
		t.Errorf("default webhook URL = %s", p.Webhook())
	}
}
