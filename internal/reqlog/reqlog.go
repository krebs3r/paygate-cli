// Package reqlog records the HTTP exchanges with Paygate for the Inspector.
// Credentials never reach the log: authorization headers, client secrets and
// access tokens are redacted, card numbers and security codes masked. The
// newest MaxEntries exchanges are kept. PAYGATE_NO_HISTORY=1 disables it.
package reqlog

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/krebs3r/paygate-cli/internal/config"
)

// MaxEntries is how many exchanges are kept.
const MaxEntries = 200

const maxBody = 64 << 10

// Entry is one request and its response.
type Entry struct {
	Time            time.Time         `json:"time"`
	Profile         string            `json:"profile"`
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	Status          int               `json:"status"`
	DurationMs      int64             `json:"durationMs"`
	CorrelationID   string            `json:"correlationId,omitempty"`
	RequestHeaders  map[string]string `json:"requestHeaders,omitempty"`
	RequestBody     string            `json:"requestBody,omitempty"`
	ResponseHeaders map[string]string `json:"responseHeaders,omitempty"`
	ResponseBody    string            `json:"responseBody,omitempty"`
	Error           string            `json:"error,omitempty"`
}

// Path returns the log file.
func Path() (string, error) {
	d, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "requests.jsonl"), nil
}

// Disabled reports whether logging is switched off.
func Disabled() bool { return os.Getenv("PAYGATE_NO_HISTORY") != "" }

type skipKey struct{}

// WithoutLog marks a context so its requests are not recorded, e.g. the
// credential checks while setting up a profile.
func WithoutLog(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipKey{}, true)
}

// Transport records every exchange that passes through it.
type Transport struct {
	Base    http.RoundTripper
	Profile string
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	if Disabled() || req.Context().Value(skipKey{}) != nil {
		return base.RoundTrip(req)
	}
	var reqBody []byte
	if req.Body != nil {
		reqBody, _ = io.ReadAll(req.Body)
		req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}
	e := Entry{
		Time:           time.Now(),
		Profile:        t.Profile,
		Method:         req.Method,
		URL:            req.URL.String(),
		RequestHeaders: headers(req.Header),
		RequestBody:    Mask(redactForm(req.Header.Get("Content-Type"), reqBody)),
	}
	start := time.Now()
	resp, err := base.RoundTrip(req)
	e.DurationMs = time.Since(start).Milliseconds()
	if err != nil {
		e.Error = err.Error()
		_ = Append(e)
		return nil, err
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(respBody))
	e.Status = resp.StatusCode
	e.CorrelationID = resp.Header.Get("X-Correlation-ID")
	e.ResponseHeaders = headers(resp.Header)
	e.ResponseBody = Mask(respBody)
	_ = Append(e)
	return resp, nil
}

var (
	mu        sync.Mutex
	panRe     = regexp.MustCompile(`("number"\s*:\s*")(\d{6})\d{2,9}(\d{4})"`)
	cvcRe     = regexp.MustCompile(`("securityCode"\s*:\s*")[^"]*"`)
	tokenRe   = regexp.MustCompile(`("access_token"\s*:\s*")[^"]+"`)
	secretKey = []string{"authorization", "cookie", "set-cookie"}
)

// Mask hides card numbers (keeping BIN and last four), security codes and
// access tokens, and truncates very large bodies.
func Mask(body []byte) string {
	if len(body) > maxBody {
		body = append(body[:maxBody:maxBody], []byte("…")...)
	}
	b := panRe.ReplaceAll(body, []byte(`${1}${2}••••••${3}"`))
	b = cvcRe.ReplaceAll(b, []byte(`${1}•••"`))
	b = tokenRe.ReplaceAll(b, []byte(`${1}[REDACTED]"`))
	return string(b)
}

func redactForm(contentType string, body []byte) []byte {
	if !strings.HasPrefix(contentType, "application/x-www-form-urlencoded") {
		return body
	}
	vals, err := url.ParseQuery(string(body))
	if err != nil {
		return []byte("[form body]")
	}
	if vals.Has("client_secret") {
		vals.Set("client_secret", "[REDACTED]")
	}
	return []byte(vals.Encode())
}

func headers(h http.Header) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		val := strings.Join(v, ", ")
		for _, s := range secretKey {
			if strings.EqualFold(k, s) {
				if scheme, _, ok := strings.Cut(val, " "); ok && strings.EqualFold(k, "authorization") {
					val = scheme + " [REDACTED]"
				} else {
					val = "[REDACTED]"
				}
			}
		}
		out[k] = val
	}
	return out
}

// Load returns the entries, newest first.
func Load() ([]Entry, error) {
	mu.Lock()
	defer mu.Unlock()
	return load()
}

func load() ([]Entry, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, sc.Err()
}

// Append stores an entry and drops the oldest beyond MaxEntries.
func Append(e Entry) error {
	if Disabled() {
		return nil
	}
	mu.Lock()
	defer mu.Unlock()
	all, err := load()
	if err != nil {
		return err
	}
	all = append([]Entry{e}, all...)
	if len(all) > MaxEntries {
		all = all[:MaxEntries]
	}
	return save(all)
}

// Clear removes all entries.
func Clear() error {
	mu.Lock()
	defer mu.Unlock()
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func save(all []Entry) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	// Oldest first on disk, so the file reads like a log.
	for i := len(all) - 1; i >= 0; i-- {
		if err := enc.Encode(all[i]); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
