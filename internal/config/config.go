// Package config stores named Paygate profiles in a TOML file and their API
// keys in the operating system's keychain.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Default callback URLs. Paygate only accepts HTTPS URLs on port 443, so
// localhost cannot be used; the CLI learns the outcome by polling instead.
const (
	DefaultReturnURL  = "https://paygate.paytest.dev/cli/return"
	DefaultCancelURL  = "https://paygate.paytest.dev/cli/cancel"
	DefaultWebhookURL = "https://paygate.paytest.dev/cli/webhook"
)

// Secret storage backends.
const (
	StoreKeychain = "keychain"
	StoreFile     = "file"
)

// Profile is one merchant / environment combination.
type Profile struct {
	Brand       string `toml:"brand"`
	Env         string `toml:"env"`
	MerchantID  string `toml:"merchant_id"`
	Auth        string `toml:"auth"`
	SecretStore string `toml:"secret_store"`
	APIKey      string `toml:"api_key,omitempty"` // only when the keychain is unavailable
	Currency    string `toml:"currency,omitempty"`
	Language    string `toml:"language,omitempty"`
	ReturnURL   string `toml:"return_url,omitempty"`
	CancelURL   string `toml:"cancel_url,omitempty"`
	WebhookURL  string `toml:"webhook_url,omitempty"`
	CustomerIP  string `toml:"customer_ip,omitempty"` // browserInfo.ipAddress; empty = this computer
}

// Return / Cancel / Webhook fall back to the defaults.
func (p *Profile) Return() string  { return orDefault(p.ReturnURL, DefaultReturnURL) }
func (p *Profile) Cancel() string  { return orDefault(p.CancelURL, DefaultCancelURL) }
func (p *Profile) Webhook() string { return orDefault(p.WebhookURL, DefaultWebhookURL) }

// DefaultCurrency falls back to EUR.
func (p *Profile) DefaultCurrency() string { return strings.ToUpper(orDefault(p.Currency, "EUR")) }

// DefaultLanguage falls back to English.
func (p *Profile) DefaultLanguage() string { return orDefault(p.Language, "en") }

// IsProd reports whether the profile targets production.
func (p *Profile) IsProd() bool { return strings.EqualFold(p.Env, "prod") }

func orDefault(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return v
}

// Config is the whole configuration file.
type Config struct {
	ActiveProfile string              `toml:"active_profile"`
	UILanguage    string              `toml:"ui_language,omitempty"` // auto | en | de
	Theme         string              `toml:"theme,omitempty"`       // blue (default) | blurple
	NoSplash      bool                `toml:"no_splash,omitempty"`
	Profiles      map[string]*Profile `toml:"profiles"`

	path string
}

// ErrNoProfile means no profile has been set up yet.
var ErrNoProfile = errors.New("no profile configured — run `paygate init` first")

// Dir returns the configuration directory: $XDG_CONFIG_HOME/paygate-cli,
// %AppData%\paygate-cli on Windows, otherwise ~/.config/paygate-cli.
func Dir() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "paygate-cli"), nil
	}
	if runtime.GOOS == "windows" {
		appData, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(appData, "paygate-cli"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "paygate-cli"), nil
}

// Path returns the path of the configuration file.
func Path() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.toml"), nil
}

// Load reads the configuration; a missing file yields an empty config.
func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	cfg := &Config{Profiles: map[string]*Profile{}, path: p}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	if err := toml.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]*Profile{}
	}
	cfg.path = p
	return cfg, nil
}

// Save writes the configuration with owner-only permissions.
func (c *Config) Save() error {
	if c.path == "" {
		p, err := Path()
		if err != nil {
			return err
		}
		c.path = p
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	b, err := toml.Marshal(c)
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// FilePath returns where the config lives.
func (c *Config) FilePath() string { return c.path }

// Names returns profile names sorted alphabetically.
func (c *Config) Names() []string {
	names := make([]string, 0, len(c.Profiles))
	for n := range c.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Resolve picks the profile: explicit name > PAYGATE_PROFILE > active profile.
func (c *Config) Resolve(name string) (string, *Profile, error) {
	if name == "" {
		name = os.Getenv("PAYGATE_PROFILE")
	}
	if name == "" {
		name = c.ActiveProfile
	}
	if name == "" {
		if len(c.Profiles) == 1 {
			for n, p := range c.Profiles {
				return n, p, nil
			}
		}
		return "", nil, ErrNoProfile
	}
	p, ok := c.Profiles[name]
	if !ok {
		if len(c.Profiles) == 0 {
			return "", nil, ErrNoProfile
		}
		return "", nil, fmt.Errorf("profile %q not found (available: %s)", name, strings.Join(c.Names(), ", "))
	}
	return name, p, nil
}
