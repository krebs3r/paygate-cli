// Package config stores named Paygate profiles in a TOML file and their API
// keys in the operating system's keychain.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/pelletier/go-toml/v2"
)

// Default callback URLs. Paygate only accepts HTTPS URLs on port 443, so
// localhost cannot be used; the CLI learns the outcome by polling instead.
// Hosted payments require a webhook URL; the .invalid domain never resolves
// (RFC 2606), so by default the webhook reaches no server at all.
const (
	DefaultReturnURL  = "https://paygate.paytest.dev/cli/return"
	DefaultCancelURL  = "https://paygate.paytest.dev/cli/cancel"
	DefaultWebhookURL = "https://webhook.invalid/paygate-cli"
)

// legacyWebhookURL is the earlier default, a sink on paygate.paytest.dev.
// Load drops it from profiles so they use DefaultWebhookURL.
const legacyWebhookURL = "https://paygate.paytest.dev/cli/webhook"

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
	APIKey      string `toml:"api_key,omitempty"`    // only when the keychain is unavailable
	HMACStore   string `toml:"hmac_store,omitempty"` // where the webhook HMAC key is; empty: none set
	HMACKey     string `toml:"hmac_key,omitempty"`   // only when the keychain is unavailable
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

// EnvDemo is the environment of the demo profile: its calls go to the demo
// Paygate built into paygate, never to a real host.
const EnvDemo = "demo"

// IsDemo reports whether the profile is the demo profile.
func (p *Profile) IsDemo() bool { return strings.EqualFold(p.Env, EnvDemo) }

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
	demo bool // read from the demo directory; the app settings live in the main config
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$`)

// ValidName reports whether name can name a profile: letters, digits, dot,
// underscore and dash, starting with a letter or digit, at most 40.
func ValidName(name string) bool { return nameRe.MatchString(name) }

// ErrNoProfile means no profile has been set up yet.
var ErrNoProfile = errors.New("no profile configured — run `paygate init` first")

// demoMode switches Dir to the demo directory; see UseDemo.
var (
	demoMu   sync.RWMutex
	demoMode bool
)

// UseDemo switches paygate into demo mode and back. In demo mode Dir is the
// demo directory below the configuration directory, so profiles, history,
// request log and webhooks of the demo stay apart from the real ones, and
// the keychain is never touched.
func UseDemo(on bool) {
	demoMu.Lock()
	defer demoMu.Unlock()
	demoMode = on
}

// Demo reports whether paygate runs in demo mode.
func Demo() bool {
	demoMu.RLock()
	defer demoMu.RUnlock()
	return demoMode
}

// DemoDirName is the folder of the demo below the configuration directory.
const DemoDirName = "demo"

// Dir returns the directory paygate stores its data in: BaseDir, or its
// demo folder in demo mode.
func Dir() (string, error) {
	d, err := BaseDir()
	if err != nil || !Demo() {
		return d, err
	}
	return filepath.Join(d, DemoDirName), nil
}

// BaseDir returns the configuration directory: $XDG_CONFIG_HOME/paygate-cli,
// %AppData%\paygate-cli on Windows, otherwise ~/.config/paygate-cli.
func BaseDir() (string, error) {
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

// Load reads the configuration; a missing file yields an empty config. In
// demo mode it reads the demo's configuration, with the app settings
// (language, theme, splash) of the main one.
func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	cfg, err := loadFile(p)
	if err != nil || !Demo() {
		return cfg, err
	}
	cfg.demo = true
	if main, err := loadMain(); err == nil {
		cfg.UILanguage, cfg.Theme, cfg.NoSplash = main.UILanguage, main.Theme, main.NoSplash
	}
	return cfg, nil
}

// loadMain reads the main configuration, also in demo mode.
func loadMain() (*Config, error) {
	d, err := BaseDir()
	if err != nil {
		return nil, err
	}
	return loadFile(filepath.Join(d, "config.toml"))
}

func loadFile(p string) (*Config, error) {
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
	for _, prof := range cfg.Profiles {
		if strings.TrimSuffix(strings.TrimSpace(prof.WebhookURL), "/") == legacyWebhookURL {
			prof.WebhookURL = ""
		}
	}
	cfg.path = p
	return cfg, nil
}

// Save writes the configuration with owner-only permissions. A demo
// configuration also writes changed app settings to the main one, so
// language and theme stay the same in and outside the demo.
func (c *Config) Save() error {
	if c.path == "" {
		p, err := Path()
		if err != nil {
			return err
		}
		c.path = p
	}
	if err := c.write(); err != nil {
		return err
	}
	if !c.demo {
		return nil
	}
	main, err := loadMain()
	if err != nil {
		return err
	}
	if main.UILanguage == c.UILanguage && main.Theme == c.Theme && main.NoSplash == c.NoSplash {
		return nil
	}
	main.UILanguage, main.Theme, main.NoSplash = c.UILanguage, c.Theme, c.NoSplash
	return main.write()
}

func (c *Config) write() error {
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
	// PAYGATE_PROFILE names a real profile; the demo has its own.
	if name == "" && !Demo() {
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
