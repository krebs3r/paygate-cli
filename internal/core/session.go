package core

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/reqlog"
)

// demoOrigin is the address of the demo Paygate this process runs.
var (
	demoMu     sync.RWMutex
	demoOrigin string
)

// SetDemoOrigin tells where the demo Paygate of this process listens; ""
// when it stopped.
func SetDemoOrigin(origin string) {
	demoMu.Lock()
	defer demoMu.Unlock()
	demoOrigin = strings.TrimRight(origin, "/")
}

// ErrDemoNotRunning means a demo profile was used without the demo Paygate.
var ErrDemoNotRunning = errors.New("the demo Paygate is not running — use `paygate demo` or `paygate --demo …`")

// Origin returns the API origin of a profile. PAYGATE_BASE_URL overrides it
// (mock server, proxies). The demo profile always talks to the demo Paygate
// of this process, and in demo mode no other profile is reachable, so the
// demo never reaches a real host.
func Origin(p *config.Profile) (string, error) {
	if p.IsDemo() {
		demoMu.RLock()
		defer demoMu.RUnlock()
		if demoOrigin == "" {
			return "", ErrDemoNotRunning
		}
		return demoOrigin, nil
	}
	if config.Demo() {
		return "", errors.New("demo mode only works with the demo profile")
	}
	if o := os.Getenv("PAYGATE_BASE_URL"); o != "" {
		return strings.TrimRight(o, "/"), nil
	}
	return paygate.Origin(paygate.Brand(p.Brand), paygate.Env(p.Env))
}

// LocalOrigin reports an API on this computer (the mock server). It
// reaches a webhook listener directly, so no tunnel is needed.
func LocalOrigin(p *config.Profile) bool {
	origin, err := Origin(p)
	if err != nil {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Endpoints returns where the calls of a profile go: the base URL of the API
// and the OAuth2 token endpoint.
func Endpoints(p *config.Profile) (api, token string, err error) {
	origin, err := Origin(p)
	if err != nil {
		return "", "", err
	}
	c := paygate.New(origin, "", "", paygate.AuthMode(p.Auth))
	return c.APIURL(""), c.TokenURL(), nil
}

// ClientOptions tune how clients are built.
type ClientOptions struct {
	Version   string
	Profile   string            // recorded with each request in the Inspector log
	Transport http.RoundTripper // optional, e.g. verbose logging to stderr
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
		c.IntegrationID = c.UserAgent
	}
	c.HTTP.Transport = &reqlog.Transport{Base: opts.Transport, Profile: opts.Profile}
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
	opts.Profile = n
	c, err := NewClient(p, key, opts)
	if err != nil {
		return nil, err
	}
	return &Session{Name: n, Profile: p, Client: c}, nil
}
