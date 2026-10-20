package config

import (
	"errors"
	"fmt"
	"os"

	"github.com/zalando/go-keyring"
)

const keyringService = "paygate-cli"

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
	if v := os.Getenv("PAYGATE_API_KEY"); v != "" {
		return v, nil
	}
	if p.SecretStore == StoreFile || p.APIKey != "" {
		if p.APIKey == "" {
			return "", fmt.Errorf("profile %q has no API key — run `paygate init --name %s` again", profileName, profileName)
		}
		return p.APIKey, nil
	}
	v, err := keyring.Get(keyringService, profileName)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", fmt.Errorf("API key for profile %q is missing in the keychain — run `paygate init --name %s` again", profileName, profileName)
	}
	if err != nil {
		return "", fmt.Errorf("read API key from keychain: %w", err)
	}
	return v, nil
}

// DeleteAPIKey removes the key from the keychain (ignoring "not found").
func DeleteAPIKey(profileName string) {
	if os.Getenv("PAYGATE_NO_KEYCHAIN") != "" {
		return
	}
	_ = keyring.Delete(keyringService, profileName)
}
