package listen

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/krebs3r/paygate-cli/internal/webhooks"
)

const testKey = "demo-hmac-key"

var enhancedBody = []byte(`{"payId":"p1","transId":"TID1","refNr":null,"status":"OK","responseCode":"00000000",` +
	`"responseDescription":"success","amount":{"value":4990,"currency":"EUR"},"paymentMethods":[{"type":"CARD"}],` +
	`"creationDate":"2026-10-23T09:41:29Z"}`)

func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PAYGATE_NO_HISTORY", "")
}

// collect starts a server whose events end up in the returned func.
func collect(t *testing.T, opts Options) (*Server, func() []Event) {
	t.Helper()
	var mu sync.Mutex
	var events []Event
	opts.OnEvent = func(e Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}
	s, err := NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, func() []Event {
		mu.Lock()
		defer mu.Unlock()
		return append([]Event{}, events...)
	}
}

func post(t *testing.T, url string, h [][2]string, body []byte) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(string(body)))
	for _, kv := range h {
		req.Header.Set(kv[0], kv[1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestServerAcceptsOnlyPostOnItsPath(t *testing.T) {
	isolate(t)
	s, events := collect(t, Options{Profile: "shop"})
	if resp, _ := http.Get(s.LocalURL() + "/"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET / = %d", resp.StatusCode)
	}
	if resp, _ := http.Get(s.LocalURL() + s.Path()); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET on the path = %d", resp.StatusCode)
	}
	if code := post(t, s.LocalURL()+"/paygate/wh/guess", nil, []byte("{}")); code != http.StatusNotFound {
		t.Errorf("POST elsewhere = %d", code)
	}
	if code := post(t, s.LocalURL()+s.Path(), nil, make([]byte, maxBody+1)); code != http.StatusRequestEntityTooLarge {
		t.Errorf("huge body = %d", code)
	}
	if len(events()) != 0 {
		t.Errorf("rejected requests must not count: %+v", events())
	}
}

func TestServerChecksAndStoresWebhooks(t *testing.T) {
	isolate(t)
	s, events := collect(t, Options{Profile: "shop", Key: testKey})
	msg, _ := webhooks.SignBody(testKey, enhancedBody, time.Now(), webhooks.TamperNone)
	if code := post(t, s.LocalURL()+s.Path(), msg.Headers(), msg.Body); code != http.StatusOK {
		t.Fatalf("answer = %d", code)
	}
	basic := webhooks.NewBasic("5e0f1c2a9b8d4e7f8a6b3c2d1e0f9a8b")
	post(t, s.LocalURL()+s.Path(), basic.Headers(), basic.Body)
	post(t, s.LocalURL()+s.Path(), basic.Headers(), basic.Body)

	evs := events()
	if len(evs) != 3 {
		t.Fatalf("events: %+v", evs)
	}
	e := evs[0]
	if e.Kind != webhooks.Enhanced || e.Signature != "valid" || e.TransID != "TID1" || e.Status != "OK" ||
		e.Amount == nil || e.Amount.Value != 4990 || e.PaymentMethod != "CARD" || e.Answer != 200 {
		t.Errorf("enhanced event: %+v", e)
	}
	if b := evs[1]; b.Kind != webhooks.Basic || b.PayID != "5e0f1c2a9b8d4e7f8a6b3c2d1e0f9a8b" || b.Signature != "missing" || b.Repeat != 0 {
		t.Errorf("basic event: %+v", b)
	}
	if r := evs[2]; r.Repeat != 1 || r.SinceFirst <= 0 {
		t.Errorf("the second delivery should count as a repeat: %+v", r)
	}

	stored, err := Load()
	if err != nil || len(stored) != 3 || stored[0].ID != 3 || stored[2].ID != 1 || string(stored[2].Body) != string(msg.Body) {
		t.Fatalf("stored: %v %+v", err, stored)
	}
	if e, ok := Find(1); !ok || e.Signature != "valid" {
		t.Errorf("Find(1) = %+v %v", e, ok)
	}
	if n, err := Clear(); n != 3 || err != nil {
		t.Errorf("Clear = %d %v", n, err)
	}
	if all, _ := Load(); len(all) != 0 {
		t.Errorf("after Clear: %d", len(all))
	}
}

func TestServerForwardsUnchanged(t *testing.T) {
	isolate(t)
	t.Setenv("PAYGATE_NO_HISTORY", "1")
	var got http.Header
	var gotBody []byte
	handler := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, gotBody = r.Header.Clone(), mustRead(r.Body)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer handler.Close()

	s, events := collect(t, Options{Profile: "shop", Key: testKey, ForwardTo: handler.URL})
	msg, _ := webhooks.SignBody(testKey, enhancedBody, time.Now(), webhooks.TamperNone)
	if code := post(t, s.LocalURL()+s.Path(), msg.Headers(), msg.Body); code != http.StatusInternalServerError {
		t.Errorf("Paygate should get the handler's status, got %d", code)
	}
	if got.Get(webhooks.HeaderSignature) != msg.Signature || string(gotBody) != string(msg.Body) {
		t.Errorf("forwarded request changed: %v %s", got, gotBody)
	}
	if e := events()[0]; e.Forward == nil || e.Forward.Status != 500 || e.Answer != 500 {
		t.Errorf("forward event: %+v", e)
	}
	if all, _ := Load(); len(all) != 0 {
		t.Error("PAYGATE_NO_HISTORY must keep webhooks out of the file")
	}

	s2, events2 := collect(t, Options{Profile: "shop", ForwardTo: handler.URL, Respond: 200})
	if code := post(t, s2.LocalURL()+s2.Path(), nil, enhancedBody); code != 200 {
		t.Errorf("--respond 200 should win, got %d", code)
	}
	if e := events2()[0]; e.Answer != 200 || e.Signature != "missing" {
		t.Errorf("respond event: %+v", e)
	}

	s3, events3 := collect(t, Options{Profile: "shop", ForwardTo: "http://127.0.0.1:1/nothing"})
	if code := post(t, s3.LocalURL()+s3.Path(), nil, enhancedBody); code != http.StatusBadGateway {
		t.Errorf("unreachable handler = %d", code)
	}
	if e := events3()[0]; e.Forward == nil || e.Forward.Error == "" {
		t.Errorf("forward error not recorded: %+v", e)
	}
}

func mustRead(r io.Reader) []byte {
	b, _ := io.ReadAll(r)
	return b
}

func TestSessionLocalAndState(t *testing.T) {
	isolate(t)
	if _, ok := Running(); ok {
		t.Fatal("nothing should run yet")
	}
	ctx := context.Background()
	s, err := Open(ctx, SessionOptions{Options: Options{Profile: "shop"}, Local: true})
	if err != nil {
		t.Fatal(err)
	}
	if s.How != ViaLocal || !strings.HasPrefix(s.URL, "http://127.0.0.1:") || !strings.HasSuffix(s.URL, s.Server.Path()) {
		t.Errorf("session: %+v", s)
	}
	if st, ok := For("shop"); !ok || st.URL != s.URL || st.PID != os.Getpid() {
		t.Errorf("state: %+v %v", st, ok)
	}
	if _, ok := For("other"); ok {
		t.Error("the listener belongs to shop only")
	}
	if _, err := Open(ctx, SessionOptions{Options: Options{Profile: "other"}, Local: true}); err == nil || !strings.Contains(err.Error(), "already runs") {
		t.Errorf("a second listener must be refused: %v", err)
	}
	s.Close()
	if _, ok := Running(); ok {
		t.Error("state left behind")
	}
	if all, _ := Load(); len(all) != 0 {
		t.Errorf("the self-test must not be stored: %+v", all)
	}
}

func TestSessionPublicURL(t *testing.T) {
	isolate(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	// A reverse proxy or your own tunnel maps the public URL to the listener.
	public := "http://127.0.0.1:" + strconv.Itoa(port) + "/"
	s, err := Open(context.Background(), SessionOptions{Options: Options{Profile: "shop", Port: port}, PublicURL: public})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.How != ViaPublicURL || s.URL != strings.TrimSuffix(public, "/")+s.Server.Path() {
		t.Errorf("session: %+v", s)
	}
}
