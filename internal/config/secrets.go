package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/zalando/go-keyring"
)

const keyringService = "paygate-cli"

// invisible are characters that sneak in when copying from web pages, PDFs or
// password managers: zero-width spaces/joiners, word joiner and BOM.
var invisible = strings.NewReplacer("\u200b", "", "\u200c", "", "\u200d", "", "\u2060", "", "\ufeff", "")

// CleanSecret removes surrounding whitespace (including line breaks and
// non-breaking spaces), invisible characters and matching surrounding quotes
// from a pasted merchant ID or API key.
func CleanSecret(s string) string {
	s = strings.TrimSpace(invisible.Replace(s))
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

// StoreAPIKey saves the key in the keychain, falling back to the config file
// (0600) when no keychain is available. It returns the backend used.
func StoreAPIKey(profileName string, p *Profile, apiKey string) (string, error) {
	if os.Getenv("PAYGATE_NO_KEYCHAIN") != "" {
		p.SecretStore = StoreFile
		p.APIKey = apiKey
		return StoreFile, nil
	}
	if err := keyring.Set(keyringService, profileName, apiKey); err == nil {
		p.SecretStore = StoreKeychain
		p.APIKey = ""
		return StoreKeychain, nil
	}
	p.SecretStore = StoreFile
	p.APIKey = apiKey
	return StoreFile, nil
}

// APIKey returns the profile's key: PAYGATE_API_KEY > keychain > file.
func APIKey(profileName string, p *Profile) (string, error) {
	if v := CleanSecret(os.Getenv("PAYGATE_API_KEY")); v != "" {
		return v, nil
	}
	if p.SecretStore == StoreFile || p.APIKey != "" {
		if p.APIKey == "" {
			return "", fmt.Errorf("profile %q has no API key — run `paygate init --name %s` again", profileName, profileName)
		}
		return CleanSecret(p.APIKey), nil
	}
	v, err := keyring.Get(keyringService, profileName)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", fmt.Errorf("API key for profile %q is missing in the keychain — run `paygate init --name %s` again", profileName, profileName)
	}
	if err != nil {
		return "", fmt.Errorf("read API key from keychain: %w", err)
	}
	return CleanSecret(v), nil
}

// DeleteAPIKey removes the key from the keychain (ignoring "not found").
func DeleteAPIKey(profileName string) {
	if os.Getenv("PAYGATE_NO_KEYCHAIN") != "" {
		return
	}
	_ = keyring.Delete(keyringService, profileName)
}

// hmacService keeps the webhook HMAC keys apart from the API keys.
const hmacService = keyringService + ".hmac"

// StoreHMACKey saves the key Paygate signs Enhanced webhooks with, in the
// keychain or, without one, in the config file. It returns the backend used.
func StoreHMACKey(profileName string, p *Profile, key string) (string, error) {
	if os.Getenv("PAYGATE_NO_KEYCHAIN") == "" {
		if err := keyring.Set(hmacService, profileName, key); err == nil {
			p.HMACStore, p.HMACKey = StoreKeychain, ""
			return StoreKeychain, nil
		}
	}
	p.HMACStore, p.HMACKey = StoreFile, key
	return StoreFile, nil
}

// HMACKey returns the profile's webhook HMAC key: PAYGATE_HMAC_KEY >
// keychain > file. An empty key without error means none is set.
func HMACKey(profileName string, p *Profile) (string, error) {
	if v := CleanSecret(os.Getenv("PAYGATE_HMAC_KEY")); v != "" {
		return v, nil
	}
	switch p.HMACStore {
	case "":
		return "", nil
	case StoreFile:
		return CleanSecret(p.HMACKey), nil
	}
	v, err := keyring.Get(hmacService, profileName)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", fmt.Errorf("HMAC key for profile %q is missing in the keychain — run `paygate profile set hmac-key` again", profileName)
	}
	if err != nil {
		return "", fmt.Errorf("read HMAC key from keychain: %w", err)
	}
	return CleanSecret(v), nil
}

// DeleteHMACKey removes the key from the keychain (ignoring "not found") and
// from the profile, if one is given.
func DeleteHMACKey(profileName string, p *Profile) {
	if p != nil {
		p.HMACStore, p.HMACKey = "", ""
	}
	if os.Getenv("PAYGATE_NO_KEYCHAIN") != "" {
		return
	}
	_ = keyring.Delete(hmacService, profileName)
}
