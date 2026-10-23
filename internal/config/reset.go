package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"
)

// Reset deletes everything paygate stores on this computer: the API keys in
// the keychain and the configuration directory with profiles, settings,
// payment history and request log. The keys go first; if they cannot be
// removed, nothing else is deleted. It returns the removed directory.
func Reset() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	// Dir always ends in paygate-cli; never delete anything else.
	if filepath.Base(dir) != "paygate-cli" {
		return "", fmt.Errorf("refusing to delete %s", dir)
	}
	cfg, err := Load()
	if err != nil {
		cfg = &Config{} // an unreadable config file is deleted as well
	}
	if err := deleteAllAPIKeys(cfg.Profiles); err != nil {
		return dir, err
	}
	return dir, os.RemoveAll(dir)
}

// deleteAllAPIKeys removes every key paygate put into the keychain, API and
// webhook HMAC keys, also those of profiles removed from the config file by
// hand.
func deleteAllAPIKeys(profiles map[string]*Profile) error {
	if os.Getenv("PAYGATE_NO_KEYCHAIN") != "" {
		return nil
	}
	for _, service := range []string{keyringService, hmacService} {
		err := keyring.DeleteAll(service)
		if err == nil || errors.Is(err, keyring.ErrNotFound) {
			continue
		}
		// Without a usable keychain the keys sit in the config file, which is
		// deleted anyway. Only keys known to be in the keychain matter.
		for _, p := range profiles {
			if service == keyringService && p.SecretStore == StoreKeychain {
				return fmt.Errorf("remove the API keys from the keychain: %w", err)
			}
			if service == hmacService && p.HMACStore == StoreKeychain {
				return fmt.Errorf("remove the HMAC keys from the keychain: %w", err)
			}
		}
	}
	return nil
}

// Tilde shortens a path in the home directory to ~/… for display.
func Tilde(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
