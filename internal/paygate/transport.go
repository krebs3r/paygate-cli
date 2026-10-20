package paygate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// LoggingTransport writes requests and responses to Out (stderr). Level 1
// prints method, URL, status and pretty bodies; level 2 also prints headers.
// Credentials are always redacted.
type LoggingTransport struct {
	Base  http.RoundTripper
	Level int
	Out   io.Writer
	Style func(kind, s string) string // optional colouring: kind is "out", "in", "meta"
}

func (t *LoggingTransport) style(kind, s string) string {
	if t.Style == nil {
		return s
	}
	return t.Style(kind, s)
}

func (t *LoggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	var reqBody []byte
	if req.Body != nil {
		reqBody, _ = io.ReadAll(req.Body)
		req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}
	fmt.Fprintln(t.Out, t.style("out", fmt.Sprintf("→ %s %s", req.Method, req.URL.String())))
	if t.Level >= 2 {
		t.writeHeaders("  > ", req.Header)
	}
	if len(reqBody) > 0 {
		fmt.Fprintln(t.Out, indent(prettyBody(redactForm(req.Header.Get("Content-Type"), reqBody)), "  "))
	}

	start := time.Now()
	resp, err := base.RoundTrip(req)
	if err != nil {
		fmt.Fprintln(t.Out, t.style("in", fmt.Sprintf("← error after %s: %v", FormatDuration(time.Since(start)), err)))
		return nil, err
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(respBody))

	meta := fmt.Sprintf("← %s · %s", resp.Status, FormatDuration(time.Since(start)))
	if id := resp.Header.Get("X-Correlation-ID"); id != "" {
		meta += " · correlation " + id
	}
	fmt.Fprintln(t.Out, t.style("in", meta))
	if t.Level >= 2 {
		t.writeHeaders("  < ", resp.Header)
	}
	if len(respBody) > 0 {
		fmt.Fprintln(t.Out, indent(prettyBody(redactToken(respBody)), "  "))
	}
	fmt.Fprintln(t.Out)
	return resp, nil
}

func (t *LoggingTransport) writeHeaders(prefix string, h http.Header) {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := strings.Join(h[k], ", ")
		if strings.EqualFold(k, "Authorization") {
			v = RedactAuthorization(v)
		}
		fmt.Fprintln(t.Out, t.style("meta", prefix+k+": "+v))
	}
}

// FormatDuration renders request timings like "184ms" or "<1ms".
func FormatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return "<1ms"
	}
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(10 * time.Millisecond).String()
}

// RedactAuthorization keeps the scheme but hides the secret.
func RedactAuthorization(v string) string {
	scheme, _, ok := strings.Cut(v, " ")
	if !ok {
		return "[REDACTED]"
	}
	return scheme + " [REDACTED]"
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

var tokenRe = regexp.MustCompile(`("access_token"\s*:\s*")[^"]+(")`)

func redactToken(body []byte) []byte {
	return tokenRe.ReplaceAll(body, []byte(`${1}[REDACTED]${2}`))
}

func prettyBody(b []byte) string {
	var out bytes.Buffer
	if json.Indent(&out, b, "", "  ") == nil {
		return out.String()
	}
	return string(b)
}

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
