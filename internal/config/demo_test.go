package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDemoModeKeepsItsOwnFolder(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Setenv("PAYGATE_NO_KEYCHAIN", "") // the demo must stay off the keychain on its own
	t.Setenv("PAYGATE_API_KEY", "real-key")
	t.Setenv("PAYGATE_PROFILE", "real")

	main := &Config{UILanguage: "de", Theme: "blue", Profiles: map[string]*Profile{"real": {Env: "test", SecretStore: StoreFile, APIKey: "k"}}, ActiveProfile: "real"}
	if err := main.Save(); err != nil {
		t.Fatal(err)
	}

	UseDemo(true)
	defer UseDemo(false)
	dir, _ := Dir()
	if want := filepath.Join(base, "paygate-cli", DemoDirName); dir != want {
		t.Fatalf("Dir = %s, want %s", dir, want)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Profiles) != 0 || cfg.UILanguage != "de" {
		t.Fatalf("demo config = %+v; it has no real profiles but the app settings", cfg)
	}
	p := &Profile{Env: EnvDemo}
	if store, err := StoreAPIKey("demo", p, "demo-key"); err != nil || store != StoreFile {
		t.Fatalf("demo keys go to the file, got %s, %v", store, err)
	}
	if store, _ := StoreHMACKey("demo", p, "demo-hmac-key"); store != StoreFile {
		t.Fatalf("demo HMAC keys go to the file, got %s", store)
	}
	if k, _ := APIKey("demo", p); k != "demo-key" {
		t.Errorf("PAYGATE_API_KEY belongs to a real profile; demo key = %q", k)
	}
	cfg.Profiles["demo"] = p
	cfg.ActiveProfile = "demo"
	if n, _, err := cfg.Resolve(""); err != nil || n != "demo" {
		t.Errorf("PAYGATE_PROFILE must not pick a profile in demo mode: %q, %v", n, err)
	}

	cfg.Theme = "blurple"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	UseDemo(false)
	mainCfg, _ := Load()
	if mainCfg.Theme != "blurple" || len(mainCfg.Profiles) != 1 || mainCfg.ActiveProfile != "real" {
		t.Errorf("app settings follow the demo, profiles stay: %+v", mainCfg)
	}

	UseDemo(true)
	if _, err := Reset(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("reset in demo mode deletes the demo folder")
	}
	if _, err := os.Stat(filepath.Join(base, "paygate-cli", "config.toml")); err != nil {
		t.Error("reset in demo mode must keep the real configuration")
	}
}
