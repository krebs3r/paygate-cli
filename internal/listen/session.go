package listen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/krebs3r/paygate-cli/internal/config"
)

// State tells other paygate commands that a listener runs and where
// webhooks go. It lives in listen.json while the listener runs.
type State struct {
	Profile string    `json:"profile"`
	URL     string    `json:"url"`
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
}

func statePath() (string, error) {
	d, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "listen.json"), nil
}

// Running returns the listener of this computer, if one runs.
func Running() (*State, bool) {
	p, err := statePath()
	if err != nil {
		return nil, false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	var st State
	if json.Unmarshal(b, &st) != nil || st.URL == "" || !alive(st.PID) {
		return nil, false
	}
	return &st, true
}

// For returns the webhook URL of a listener running for profile.
func For(profile string) (*State, bool) {
	st, ok := Running()
	if !ok || st.Profile != profile {
		return nil, false
	}
	return st, true
}

func writeState(st State) error {
	p, err := statePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(st, "", "  ")
	return os.WriteFile(p, b, 0o600)
}

func removeState(pid int) {
	p, err := statePath()
	if err != nil {
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var st State
	if json.Unmarshal(b, &st) == nil && st.PID == pid {
		_ = os.Remove(p)
	}
}

// How tells how webhooks reach the listener.
type How string

const (
	ViaTunnel    How = "cloudflared quick tunnel"
	ViaPublicURL How = "your public URL"
	ViaLocal     How = "this computer only"
)

// SessionOptions configure Open.
type SessionOptions struct {
	Options
	PublicURL string // use this address instead of a tunnel
	Local     bool   // use the listener's own address (mock server)
}

// Session is a running listener with its public address.
type Session struct {
	Server *Server
	Tunnel *Tunnel // nil without cloudflared
	URL    string  // the webhook URL to give Paygate
	How    How
	Ready  time.Duration // until the address answered
}

// Open starts the listener, the tunnel unless PublicURL or Local is set,
// checks that the address reaches the listener and records the state.
func Open(ctx context.Context, o SessionOptions) (*Session, error) {
	if st, ok := Running(); ok {
		return nil, fmt.Errorf("paygate listen already runs for profile %s (process %d); stop it first", st.Profile, st.PID)
	}
	start := time.Now()
	srv, err := NewServer(o.Options)
	if err != nil {
		return nil, err
	}
	s := &Session{Server: srv}
	switch {
	case o.Local:
		s.URL, s.How = srv.LocalURL()+srv.Path(), ViaLocal
	case o.PublicURL != "":
		s.URL, s.How = strings.TrimRight(o.PublicURL, "/")+srv.Path(), ViaPublicURL
	default:
		bin, err := Cloudflared()
		if err != nil {
			srv.Close()
			return nil, err
		}
		t, err := StartTunnel(ctx, bin, srv.LocalURL(), 45*time.Second)
		if err != nil {
			srv.Close()
			return nil, err
		}
		s.Tunnel, s.URL, s.How = t, t.URL+srv.Path(), ViaTunnel
		t.WaitResolvable(ctx, 30*time.Second)
	}
	if err := srv.SelfTest(ctx, s.URL, 45*time.Second); err != nil {
		s.close()
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, fmt.Errorf("the webhook URL does not reach this computer: %w", err)
	}
	s.Ready = time.Since(start)
	if err := writeState(State{Profile: o.Profile, URL: s.URL, PID: os.Getpid(), Started: time.Now()}); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

// Close ends the tunnel and the listener and removes the state.
func (s *Session) Close() {
	removeState(os.Getpid())
	s.close()
}

func (s *Session) close() {
	if s.Tunnel != nil {
		s.Tunnel.Close()
	}
	_ = s.Server.Close()
}
