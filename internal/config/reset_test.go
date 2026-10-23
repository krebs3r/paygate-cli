package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestReset(t *testing.T) {
	keyring.MockInit()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PAYGATE_NO_KEYCHAIN", "")
	cfg, _ := Load()
	for _, name := range []string{"shop", "shop-prod"} {
		p := &Profile{Brand: "nexi", Env: "test", MerchantID: "m"}
		if store, err := StoreAPIKey(name, p, "key-"+name); err != nil || store != StoreKeychain {
			t.Fatalf("store key: %s %v", store, err)
		}
		cfg.Profiles[name] = p
	}
	if store, err := StoreHMACKey("shop", cfg.Profiles["shop"], "hmac-shop"); err != nil || store != StoreKeychain {
		t.Fatalf("store HMAC key: %s %v", store, err)
	}
	// A key left behind by a profile removed from the file by hand.
	_ = keyring.Set(keyringService, "orphan", "old-key")
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	dir, _ := Dir()
	if err := os.WriteFile(filepath.Join(dir, "history.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	removed, err := Reset()
	if err != nil || removed != dir {
		t.Fatalf("Reset() = %q, %v", removed, err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("config directory still exists: %v", err)
	}
	for _, name := range []string{"shop", "shop-prod", "orphan"} {
		if _, err := keyring.Get(keyringService, name); !errors.Is(err, keyring.ErrNotFound) {
			t.Errorf("key of %s still in the keychain: %v", name, err)
		}
	}
	if _, err := keyring.Get(hmacService, "shop"); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("HMAC key still in the keychain: %v", err)
	}
	if _, err := Reset(); err != nil {
		t.Errorf("a second reset must succeed: %v", err)
	}
}

func TestResetKeepsDataWhenKeychainFails(t *testing.T) {
	keyring.MockInit()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PAYGATE_NO_KEYCHAIN", "")
	cfg, _ := Load()
	p := &Profile{Brand: "nexi", Env: "prod", MerchantID: "m"}
	if _, err := StoreAPIKey("shop", p, "key"); err != nil {
		t.Fatal(err)
	}
	cfg.Profiles["shop"] = p
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	keyring.MockInitWithError(errors.New("keychain locked"))
	if _, err := Reset(); err == nil {
		t.Fatal("Reset must fail when keys in the keychain cannot be removed")
	}
	if _, err := os.Stat(cfg.FilePath()); err != nil {
		t.Errorf("nothing may be deleted when the keys stay: %v", err)
	}
}
