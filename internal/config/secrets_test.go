package config

import (
	"testing"

	"github.com/zalando/go-keyring"
)

func TestHMACKey(t *testing.T) {
	keyring.MockInit()
	t.Setenv("PAYGATE_NO_KEYCHAIN", "")
	t.Setenv("PAYGATE_HMAC_KEY", "")

	p := &Profile{}
	if k, err := HMACKey("shop", p); k != "" || err != nil {
		t.Fatalf("no key set: %q %v", k, err)
	}
	if store, err := StoreHMACKey("shop", p, "hmac-secret"); err != nil || store != StoreKeychain || p.HMACKey != "" {
		t.Fatalf("store: %s %v %+v", store, err, p)
	}
	if k, err := HMACKey("shop", p); k != "hmac-secret" || err != nil {
		t.Errorf("from keychain: %q %v", k, err)
	}
	if k, _ := APIKey("shop", &Profile{SecretStore: StoreKeychain}); k == "hmac-secret" {
		t.Error("HMAC key must not share the API key's keychain entry")
	}
	t.Setenv("PAYGATE_HMAC_KEY", " override\n")
	if k, _ := HMACKey("shop", p); k != "override" {
		t.Errorf("PAYGATE_HMAC_KEY should win, got %q", k)
	}
	t.Setenv("PAYGATE_HMAC_KEY", "")
	DeleteHMACKey("shop", p)
	if p.HMACStore != "" {
		t.Errorf("profile still points to a key: %+v", p)
	}
	if k, err := HMACKey("shop", &Profile{HMACStore: StoreKeychain}); k != "" || err == nil {
		t.Errorf("deleted key still readable: %q %v", k, err)
	}

	t.Setenv("PAYGATE_NO_KEYCHAIN", "1")
	if store, _ := StoreHMACKey("shop", p, "in-file"); store != StoreFile || p.HMACKey != "in-file" {
		t.Errorf("file fallback: %s %+v", store, p)
	}
	if k, _ := HMACKey("shop", p); k != "in-file" {
		t.Errorf("from file: %q", k)
	}
}

func TestCleanSecret(t *testing.T) {
	tests := map[string]string{
		"abc":             "abc",
		"  abc  ":         "abc",
		"abc\n":           "abc",
		"abc\r\n":         "abc",
		"\tabc\u00a0":     "abc",
		"\u200babc\ufeff": "abc",
		`"abc"`:           "abc",
		"'abc'":           "abc",
		`"abc`:            `"abc`,
		"a b":             "a b",
	}
	for in, want := range tests {
		if got := CleanSecret(in); got != want {
			t.Errorf("CleanSecret(%q) = %q, want %q", in, got, want)
		}
	}
}
