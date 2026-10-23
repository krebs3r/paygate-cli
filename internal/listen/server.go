package listen

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/krebs3r/paygate-cli/internal/webhooks"
)

// maxBody limits a webhook body; Paygate's are a few hundred bytes.
const maxBody = 64 << 10

// selftestHeader marks the request paygate sends itself through the tunnel.
const selftestHeader = "X-Paygate-Cli-Selftest"

// Options configure a Server.
type Options struct {
	Profile   string
	Key       string      // HMAC key; empty skips the signature comparison
	Port      int         // 0 picks a free port
	ForwardTo string      // also post every webhook here, unchanged
	Respond   int         // status for Paygate; 0: 200, or the forward's status
	OnEvent   func(Event) // called for every webhook once it is answered
}

// Server is the local listener. It accepts POST requests on a random path
// only; everything else gets 404 or 405, because a tunnel address is public
// and found by scanners within minutes.
type Server struct {
	opts     Options
	ln       net.Listener
	srv      *http.Server
	path     string
	selftest string
	selfOK   chan struct{}
	client   *http.Client

	mu    sync.Mutex
	first map[string]time.Time
	count map[string]int
}

// NewServer starts listening on 127.0.0.1.
func NewServer(opts Options) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(opts.Port))
	if err != nil {
		return nil, fmt.Errorf("listen on port %d: %w", opts.Port, err)
	}
	s := &Server{
		opts:     opts,
		ln:       ln,
		path:     "/paygate/wh/" + randHex(6),
		selftest: randHex(16),
		selfOK:   make(chan struct{}, 1),
		client: &http.Client{
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		first: map[string]time.Time{},
		count: map[string]int{},
	}
	s.srv = &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

// Path is the random path webhooks have to use.
func (s *Server) Path() string { return s.path }

// LocalURL is the listener's own address, without the path.
func (s *Server) LocalURL() string { return "http://" + s.ln.Addr().String() }

// Port is the port the listener got.
func (s *Server) Port() int { return s.ln.Addr().(*net.TCPAddr).Port }

// Close stops the listener.
func (s *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.srv.Shutdown(ctx)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != s.path {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	if r.Header.Get(selftestHeader) == s.selftest {
		select {
		case s.selfOK <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	now := time.Now()
	ev := Event{Time: now, Profile: s.opts.Profile, Target: r.URL.RequestURI(), Header: r.Header.Clone(), Body: body, RemoteIP: remoteIP(r)}
	ev.Header.Del("Authorization")
	check := webhooks.Verify(r.Header, body, s.opts.Key, now)
	ev.Kind = check.Kind
	switch {
	case !check.Signed:
		ev.Signature = "missing"
	case !check.Checked:
		ev.Signature = "unchecked"
	case check.Valid:
		ev.Signature = "valid"
	default:
		ev.Signature = "invalid"
	}
	describe(&ev, body)
	s.noteRepeat(&ev, body)

	ev.Answer = http.StatusOK
	if s.opts.ForwardTo != "" {
		ev.Forward = s.forward(r.Header, body)
		if ev.Forward.Error != "" {
			ev.Answer = http.StatusBadGateway
		} else {
			ev.Answer = ev.Forward.Status
		}
	}
	if s.opts.Respond != 0 {
		ev.Answer = s.opts.Respond
	}
	w.WriteHeader(ev.Answer)

	_ = Append(&ev)
	if s.opts.OnEvent != nil {
		s.opts.OnEvent(ev)
	}
}

// noteRepeat counts deliveries of the same webhook: same payId, or the
// same body when there is none.
func (s *Server) noteRepeat(ev *Event, body []byte) {
	key := ev.PayID
	if key == "" {
		sum := sha256.Sum256(body)
		key = hex.EncodeToString(sum[:])
	}
	if ev.Status != "" {
		key += "/" + ev.Status
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.first[key]; ok {
		s.count[key]++
		ev.Repeat, ev.SinceFirst = s.count[key], ev.Time.Sub(t)
		return
	}
	s.first[key] = ev.Time
}

// hopHeaders are not passed on when forwarding.
var hopHeaders = map[string]bool{
	"Connection": true, "Keep-Alive": true, "Proxy-Authenticate": true, "Proxy-Authorization": true,
	"Te": true, "Trailer": true, "Transfer-Encoding": true, "Upgrade": true,
	"Content-Length": true, "Accept-Encoding": true, "Authorization": true,
}

func (s *Server) forward(h http.Header, body []byte) *Forward {
	f := &Forward{URL: s.opts.ForwardTo}
	req, err := http.NewRequest(http.MethodPost, s.opts.ForwardTo, bytes.NewReader(body))
	if err != nil {
		f.Error = err.Error()
		return f
	}
	for k, vs := range h {
		if hopHeaders[http.CanonicalHeaderKey(k)] {
			continue
		}
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	start := time.Now()
	resp, err := s.client.Do(req)
	f.Duration = time.Since(start)
	if err != nil {
		f.Error = err.Error()
		return f
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
	resp.Body.Close()
	f.Status = resp.StatusCode
	return f
}

// SelfTest posts through the public URL until the listener sees the
// request. A new quick tunnel takes a few seconds to become reachable.
func (s *Server) SelfTest(ctx context.Context, publicURL string, limit time.Duration) error {
	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(limit)
	var last error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, publicURL, strings.NewReader("{}"))
		if err != nil {
			return err
		}
		req.Header.Set(selftestHeader, s.selftest)
		if resp, err := client.Do(req); err != nil {
			last = err
		} else {
			resp.Body.Close()
			last = fmt.Errorf("answered %s", resp.Status)
		}
		select {
		case <-s.selfOK:
			return nil
		default:
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not reach the listener within %s (%v)", publicURL, limit, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.selfOK:
			return nil
		case <-time.After(time.Second):
		}
	}
}

// describe pulls the payment fields out of a body, accepting payId and
// PayID.
func describe(ev *Event, body []byte) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(body, &obj) != nil {
		return
	}
	str := func(names ...string) string {
		for _, n := range names {
			var v string
			if raw, ok := obj[n]; ok && json.Unmarshal(raw, &v) == nil {
				return v
			}
		}
		return ""
	}
	ev.PayID = str("payId", "PayID")
	ev.TransID = str("transId")
	ev.Status = str("status")
	var a webhooks.Amount
	if raw, ok := obj["amount"]; ok && json.Unmarshal(raw, &a) == nil && a.Currency != "" {
		ev.Amount = &a
	}
	// An array of methods or a single object.
	var methods []webhooks.Method
	var method webhooks.Method
	if raw, ok := obj["paymentMethods"]; ok {
		if json.Unmarshal(raw, &methods) == nil && len(methods) > 0 {
			ev.PaymentMethod = methods[0].Type
		} else if json.Unmarshal(raw, &method) == nil {
			ev.PaymentMethod = method.Type
		}
	}
}

// remoteIP prefers the address Cloudflare saw over the tunnel's own.
func remoteIP(r *http.Request) string {
	if ip := r.Header.Get("Cf-Connecting-Ip"); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(errors.New("no randomness: " + err.Error()))
	}
	return hex.EncodeToString(b)
}
