// Package demo runs paygate without credentials: a demo Paygate (the mock
// of internal/mockpaygate) inside this process, on a free port of
// 127.0.0.1, and a demo profile that talks to it. Profiles, history, request
// log and webhooks of the demo live in their own folder; the keychain is
// never touched and no request leaves this computer.
package demo

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/i18n"
	"github.com/krebs3r/paygate-cli/internal/mockpaygate"
)

// The demo merchant. The values are made up and only valid for the demo
// Paygate.
const (
	ProfileName = "demo"
	MerchantID  = "demo-shop"
	APIKey      = "demo-key"
	HMACKey     = "demo-hmac-key"
)

// stateFile keeps the demo Paygate's payments between paygate processes, so
// `paygate --demo pay` and a later `paygate --demo capture` fit together.
const stateFile = "paygate-state.json"

// Paygate is a running demo Paygate.
type Paygate struct {
	URL  string
	mock *mockpaygate.Server
	srv  *http.Server

	mu    sync.Mutex
	path  string
	saved []byte
}

var (
	runMu   sync.Mutex
	running *Paygate
)

// Running returns the demo Paygate of this process, if one runs.
func Running() *Paygate {
	runMu.Lock()
	defer runMu.Unlock()
	return running
}

// Start switches paygate into demo mode, makes sure the demo profile exists
// and is active, and starts the demo Paygate. A second Start returns the
// running one.
func Start() (*Paygate, error) {
	runMu.Lock()
	defer runMu.Unlock()
	if running != nil {
		return running, nil
	}
	config.UseDemo(true)
	p, err := start()
	if err != nil {
		config.UseDemo(false)
		return nil, err
	}
	running = p
	return p, nil
}

func start() (*Paygate, error) {
	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := EnsureProfile(); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start the demo Paygate: %w", err)
	}
	p := &Paygate{URL: "http://" + ln.Addr().String(), path: filepath.Join(dir, stateFile)}
	m := mockpaygate.New(MerchantID, APIKey)
	m.BaseURL = p.URL
	m.Checkout = mockpaygate.CheckoutAsk
	m.Webhook, m.HMACKey = "enhanced", HMACKey
	m.LocalWebhooksOnly = true
	m.Lang = pageLanguage // the checkout page speaks the app's language
	p.mock = m
	if b, err := os.ReadFile(p.path); err == nil {
		// A broken file only loses old demo payments.
		if m.Restore(b) == nil {
			p.saved = b
		}
	}
	p.srv = &http.Server{Handler: p.handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = p.srv.Serve(ln) }()
	core.SetDemoOrigin(p.URL)
	return p, nil
}

// pageLanguage is the language set for the app ("de" or "en"), read from
// the configuration at most every two seconds, so a change in the app
// reaches the checkout page soon.
func pageLanguage() string {
	langMu.Lock()
	defer langMu.Unlock()
	if time.Since(langAt) > 2*time.Second {
		setting := ""
		if cfg, err := config.Load(); err == nil {
			setting = cfg.UILanguage
		}
		lang, langAt = string(i18n.Detect(setting)), time.Now()
	}
	return lang
}

var (
	langMu sync.Mutex
	langAt time.Time
	lang   string
)

// handler serves the mock and saves its payments after every request.
func (p *Paygate) handler() http.Handler {
	h := p.mock.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r)
		p.save()
	})
}

// save writes the payments when they changed.
func (p *Paygate) save() {
	b, err := p.mock.Snapshot()
	if err != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if bytes.Equal(b, p.saved) {
		return
	}
	if _, err := os.Stat(filepath.Dir(p.path)); err != nil {
		return // the demo data was deleted meanwhile
	}
	tmp := p.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil && os.Rename(tmp, p.path) == nil {
		p.saved = b
	}
}

// Stop ends the demo Paygate and leaves demo mode.
func Stop() {
	runMu.Lock()
	defer runMu.Unlock()
	if running == nil {
		return
	}
	running.save()
	_ = running.srv.Close()
	running = nil
	core.SetDemoOrigin("")
	config.UseDemo(false)
}

// Host is the demo Paygate's address without scheme, e.g. 127.0.0.1:53817.
func (p *Paygate) Host() string { return p.URL[len("http://"):] }

// EnsureProfile creates the demo profile in the demo folder if it is
// missing and makes it the active one. Settings the user changed in it
// (currency, URLs) stay; its credentials always match the demo Paygate.
func EnsureProfile() error {
	if !config.Demo() {
		return errors.New("not in demo mode")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	prof, ok := cfg.Profiles[ProfileName]
	if !ok {
		prof = &config.Profile{}
		cfg.Profiles[ProfileName] = prof
	}
	prof.Brand, prof.Env, prof.MerchantID, prof.Auth = "nexi", config.EnvDemo, MerchantID, "basic"
	prof.SecretStore, prof.APIKey = config.StoreFile, APIKey
	prof.HMACStore, prof.HMACKey = config.StoreFile, HMACKey
	cfg.ActiveProfile = ProfileName
	return cfg.Save()
}

// Reset deletes the demo's payments, request log and webhooks and sets up
// the demo profile again. It works in and outside demo mode and never
// touches the real profiles.
func Reset() error {
	base, err := config.BaseDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(base, config.DemoDirName)
	if p := Running(); p != nil {
		p.mock.Clear()
		p.mu.Lock()
		p.saved = nil
		p.mu.Unlock()
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if config.Demo() {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		return EnsureProfile()
	}
	return nil
}

// Dir is the demo folder, in and outside demo mode.
func Dir() (string, error) {
	base, err := config.BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, config.DemoDirName), nil
}
