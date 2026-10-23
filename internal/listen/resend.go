package listen

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// ResendHeaders are the headers a received webhook is sent on with:
// Content-Type and the X-Paygate-* headers, in a stable order.
func (e Event) ResendHeaders() [][2]string {
	var out [][2]string
	for n, vs := range e.Header {
		if strings.EqualFold(n, "Content-Type") || strings.HasPrefix(strings.ToLower(n), "x-paygate-") {
			out = append(out, [2]string{n, strings.Join(vs, ", ")})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// Answer is a handler's reply to a webhook sent again.
type Answer struct {
	Code     int
	Status   string
	Duration time.Duration
}

// Accepted reports a 2xx answer, the only one Paygate counts as delivered.
func (a Answer) Accepted() bool { return a.Code >= 200 && a.Code <= 299 }

// Resend posts a received webhook to another URL, body and headers as they
// came. Redirects are reported, not followed, like Paygate does.
func Resend(ctx context.Context, to string, e Event) (Answer, error) {
	client := &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, to, bytes.NewReader(e.Body))
	if err != nil {
		return Answer{}, err
	}
	for _, h := range e.ResendHeaders() {
		req.Header.Set(h[0], h[1])
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return Answer{}, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
	resp.Body.Close()
	return Answer{Code: resp.StatusCode, Status: resp.Status, Duration: time.Since(start)}, nil
}
