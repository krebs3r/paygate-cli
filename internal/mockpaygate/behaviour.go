package mockpaygate

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/krebs3r/paygate-cli/internal/webhooks"
)

// CheckoutDecline completes hosted payments by themselves, like
// CheckoutAuto, but declines them.
const CheckoutDecline = "decline"

// Behaviour is what can change while the mock runs: the demo scenarios.
type Behaviour struct {
	Checkout   string          // CheckoutAuto, CheckoutAsk or CheckoutDecline
	Webhook    string          // "basic", "enhanced" or "" for none
	Tamper     webhooks.Tamper // how Enhanced webhooks are broken on purpose
	Require3DS bool
	DCCOff     bool
	// FailNext answers the next API call with this HTTP status (400, 401,
	// 429 or 503) instead of handling it; 0 for none.
	FailNext int
}

// Behaviour returns the current behaviour.
func (s *Server) Behaviour() Behaviour {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return Behaviour{Checkout: s.Checkout, Webhook: s.Webhook, Tamper: s.Tamper, Require3DS: s.Require3DS, DCCOff: s.DCCOff, FailNext: s.FailNext}
}

// SetBehaviour changes the behaviour, also while requests are served.
func (s *Server) SetBehaviour(b Behaviour) {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	s.Checkout, s.Webhook, s.Tamper, s.Require3DS, s.DCCOff, s.FailNext = b.Checkout, b.Webhook, b.Tamper, b.Require3DS, b.DCCOff, b.FailNext
}

// takeFault returns the status the next API call fails with, once.
func (s *Server) takeFault() int {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	code := s.FailNext
	s.FailNext = 0
	return code
}

// fault answers an API call with a made-up error, as a demo scenario asks.
func fault(w http.ResponseWriter, code int) {
	switch code {
	case http.StatusUnauthorized:
		// Like a wrong key: no body.
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(code)
	case http.StatusBadRequest:
		problem(w, map[string][]string{"transId": {"The transId field is required."}})
	case http.StatusTooManyRequests:
		w.Header().Set("Retry-After", "5")
		writeJSON(w, code, map[string]any{"title": http.StatusText(code), "status": code})
	default:
		writeJSON(w, code, map[string]any{"title": http.StatusText(code), "status": code})
	}
}

// faults wraps the API with the error a scenario asks for.
func (s *Server) faults(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v2/") {
			if code := s.takeFault(); code != 0 {
				w.Header().Set("X-Demo-Scenario", "fail-next="+strconv.Itoa(code))
				fault(w, code)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
