package core

import (
	"net/http"
	"os"
	"strings"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/paygate"
)

// Origin returns the API origin of a profile. PAYGATE_BASE_URL overrides it
// (mock server, proxies).
func Origin(p *config.Profile) (string, error) {
	if o := os.Getenv("PAYGATE_BASE_URL"); o != "" {
		return strings.TrimRight(o, "/"), nil
	}
	return paygate.Origin(paygate.Brand(p.Brand), paygate.Env(p.Env))
}

// ClientOptions tune how clients are built.
type ClientOptions struct {
	Version   string
	Transport http.RoundTripper // optional, e.g. request logging
}

// NewClient builds an API client for a profile and key.
func NewClient(p *config.Profile, apiKey string, opts ClientOptions) (*paygate.Client, error) {
	origin, err := Origin(p)
	if err != nil {
		return nil, err
	}
	c := paygate.New(origin, p.MerchantID, apiKey, paygate.AuthMode(p.Auth))
	if opts.Version != "" {
		c.UserAgent = "paygate-cli/" + opts.Version
	}
	if opts.Transport != nil {
		c.HTTP.Transport = opts.Transport
	}
	return c, nil
}

// Session is an opened profile with a ready client.
type Session struct {
	Name    string
	Profile *config.Profile
	Client  *paygate.Client
}

// Host returns the API host without scheme.
func (s *Session) Host() string {
	return strings.TrimPrefix(strings.TrimPrefix(s.Client.Origin, "https://"), "http://")
}

// OpenSession resolves a profile (explicit name, PAYGATE_PROFILE, active) and
// loads its API key.
func OpenSession(cfg *config.Config, name string, opts ClientOptions) (*Session, error) {
	n, p, err := cfg.Resolve(name)
	if err != nil {
		return nil, err
	}
	key, err := config.APIKey(n, p)
	if err != nil {
		return nil, err
	}
	c, err := NewClient(p, key, opts)
	if err != nil {
		return nil, err
	}
	return &Session{Name: n, Profile: p, Client: c}, nil
}
