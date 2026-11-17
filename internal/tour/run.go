package tour

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/export"
	"github.com/krebs3r/paygate-cli/internal/listen"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/webhooks"
)

// ErrProduction: tours create payments, so they do not run on production.
var ErrProduction = errors.New("tours do not run on production profiles: payments there move real money. Use the demo or a test profile")

// ErrNoWebhooks: the tour needs a webhook paygate can receive.
var ErrNoWebhooks = errors.New("this tour needs a webhook paygate can receive: run it in the demo, or start the webhook listener first")

// Run is a tour being walked through with a profile.
type Run struct {
	Tour    *Tour
	Session *core.Session
	Step    int
	// Outcomes holds the final outcome of every step run so far.
	Outcomes []*Outcome

	// What the steps found out, for the steps after them.
	TransID  string
	PayID    string
	Amount   paygate.Amount
	Redirect string
	Payment  *paygate.Payment
	Webhook  *Webhook
	// Second is the second payment of a tour, e.g. the one that is
	// released again.
	SecondTransID, SecondPayID string

	planned map[int]*Planned
	hook    *receiver
	version string
	opened  bool // the payment page was opened

	mu sync.RWMutex // guards Outcomes: a step runs while the app draws
}

// Start begins a tour with an opened profile. In the demo and against a
// local mock it starts a small webhook receiver on this computer.
func Start(t *Tour, s *core.Session, version string) (*Run, error) {
	if s.Profile.IsProd() {
		return nil, ErrProduction
	}
	r := &Run{Tour: t, Session: s, Outcomes: make([]*Outcome, len(t.Steps)), planned: map[int]*Planned{}, version: version}
	if core.LocalOrigin(s.Profile) {
		h, err := newReceiver()
		if err != nil {
			return nil, err
		}
		r.hook = h
	}
	if t.NeedsWebhooks && r.WebhookURL() == "" {
		r.Close()
		return nil, ErrNoWebhooks
	}
	return r, nil
}

// Close stops the webhook receiver.
func (r *Run) Close() {
	if r.hook != nil {
		r.hook.close()
		r.hook = nil
	}
}

// Current is the step the run stands on.
func (r *Run) Current() *Step { return r.Tour.Steps[r.Step] }

// Done reports whether step i has run.
func (r *Run) Done(i int) bool { return r.Outcome(i) != nil }

// Outcome is the final outcome of step i, or nil.
func (r *Run) Outcome(i int) *Outcome {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if i < 0 || i >= len(r.Outcomes) {
		return nil
	}
	return r.Outcomes[i]
}

func (r *Run) keep(i int, o *Outcome) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Outcomes[i] = o
}

// Finished reports whether every step has run.
func (r *Run) Finished() bool { return r.Done(len(r.Outcomes) - 1) }

// Reachable is the last step that can be opened: the first one not run.
func (r *Run) Reachable() int {
	for i := range r.Outcomes {
		if !r.Done(i) {
			return i
		}
	}
	return len(r.Outcomes) - 1
}

// Go moves to step i if it can be reached.
func (r *Run) Go(i int) bool {
	if i < 0 || i >= len(r.Tour.Steps) || i > r.Reachable() {
		return false
	}
	r.Step = i
	return true
}

// Plan returns the request of the current step, built once.
func (r *Run) Plan() (*Planned, error) {
	st := r.Current()
	if st.Plan == nil {
		return nil, nil
	}
	if p, ok := r.planned[r.Step]; ok {
		return p, nil
	}
	p, err := st.Plan(r)
	if err != nil {
		return nil, err
	}
	r.planned[r.Step] = p
	return p, nil
}

// Exec runs the current step once. A final outcome is kept; a pending one
// asks the caller to run the step again later.
func (r *Run) Exec(ctx context.Context) *Outcome {
	st := r.Current()
	var o *Outcome
	if st.Skip != nil {
		if why := st.Skip(r).String(); why != "" {
			o = &Outcome{Skipped: why}
			r.keep(r.Step, o)
			return o
		}
	}
	if st.Kind == Request {
		o = r.send(ctx, st)
	} else {
		o = st.Do(ctx, r)
	}
	if o != nil && !o.Pending {
		r.keep(r.Step, o)
	}
	return o
}

func (r *Run) send(ctx context.Context, st *Step) *Outcome {
	p, err := r.Plan()
	if err != nil {
		return &Outcome{Err: err}
	}
	c, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	res, err := r.Session.Client.Exec(c, p.Prepared)
	if p.Record != nil {
		p.Record(res, err)
	}
	o := &Outcome{Exchange: r.exchange(p, res)}
	var apiErr *paygate.APIError
	if err != nil && !errors.As(err, &apiErr) {
		o.Err = err
		return o
	}
	// An answer like 422 is part of the tour; its payment is in the error.
	if apiErr != nil && res != nil && res.Payment == nil {
		res.Payment = apiErr.Payment
	}
	if st.After != nil && res != nil {
		st.After(r, res, o)
	}
	return o
}

// exchange is what the tour shows of a request and its answer.
func (r *Run) exchange(p *Planned, res *paygate.Result) *Exchange {
	c := r.Session.Client
	x := &Exchange{Method: p.Prepared.Method, URL: p.Prepared.URL, Body: p.Prepared.Body, Notes: p.Notes,
		Curl: export.Curl(p.Prepared, c, false)}
	auth := "Basic ••••••"
	if c.Auth == paygate.AuthOAuth2 {
		auth = "Bearer ••••••"
	}
	x.Headers = [][2]string{{"Authorization", auth}}
	if p.Prepared.Body != nil {
		x.Headers = append(x.Headers, [2]string{"Content-Type", "application/json"})
	}
	if p.Prepared.IdempotencyKey != "" {
		x.Headers = append(x.Headers, [2]string{"Idempotency-Key", p.Prepared.IdempotencyKey})
	}
	if res != nil && res.Response != nil {
		x.Status, x.Duration, x.Response, x.Correlation = res.Response.StatusCode, res.Response.Duration, res.Response.Body, res.Response.CorrelationID
	}
	return x
}

// Demo reports whether the run talks to the demo Paygate.
func (r *Run) Demo() bool { return r.Session.Profile.IsDemo() }

// WebhookURL is where the payments of the tour send their webhook: the
// receiver of the tour, a running listener of the profile, or "" for the
// profile's own URL.
func (r *Run) WebhookURL() string {
	if r.hook != nil {
		return r.hook.url
	}
	if st, ok := listen.For(r.Session.Name); ok {
		return st.URL
	}
	return ""
}

// HMACKey is the key the webhooks of the profile are signed with.
func (r *Run) HMACKey() string {
	k, _ := config.HMACKey(r.Session.Name, r.Session.Profile)
	return k
}

// FindWebhook returns the webhook of a payment once it has arrived, from the
// tour's receiver or the profile's listener.
func (r *Run) FindWebhook(payID string) *Webhook {
	if r.hook != nil {
		return r.hook.find(payID)
	}
	events, _ := listen.Load()
	for _, e := range events {
		if e.Profile == r.Session.Name && strings.EqualFold(e.PayID, payID) {
			return &Webhook{Header: e.Header, Body: e.Body, At: e.Time}
		}
	}
	return nil
}

// Webhook is a webhook paygate received.
type Webhook struct {
	Header http.Header
	Body   []byte
	At     time.Time
}

// PayID reads the payId of a webhook body, Basic or Enhanced.
func (w *Webhook) PayID() string {
	var obj map[string]json.RawMessage
	if json.Unmarshal(w.Body, &obj) != nil {
		return ""
	}
	for k, v := range obj {
		if strings.EqualFold(k, "payId") {
			var s string
			_ = json.Unmarshal(v, &s)
			return s
		}
	}
	return ""
}

// Check verifies the webhook with key, as of when it arrived.
func (w *Webhook) Check(key string) webhooks.Check {
	return webhooks.Verify(w.Header, w.Body, key, w.At)
}

// receiver takes the webhooks of a tour on this computer.
type receiver struct {
	url string
	srv *http.Server
	mu  sync.Mutex
	got []*Webhook
}

func newReceiver() (*receiver, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	h := &receiver{url: "http://" + ln.Addr().String() + "/webhook"}
	h.srv = &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(req.Body, 1<<20))
		h.mu.Lock()
		h.got = append(h.got, &Webhook{Header: req.Header.Clone(), Body: body, At: time.Now()})
		h.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})}
	go func() { _ = h.srv.Serve(ln) }()
	return h, nil
}

func (h *receiver) find(payID string) *Webhook {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, w := range h.got {
		if strings.EqualFold(w.PayID(), payID) {
			return w
		}
	}
	return nil
}

func (h *receiver) close() { _ = h.srv.Close() }
