package paygate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// AuthMode selects how requests are authenticated.
type AuthMode string

const (
	AuthBasic  AuthMode = "basic"
	AuthOAuth2 AuthMode = "oauth2"
)

const apiPrefix = "/api/v2"

// Client talks to one Paygate origin with one set of credentials.
type Client struct {
	Origin     string // e.g. https://test.paygate.nexigroup.com
	MerchantID string
	APIKey     string
	Auth       AuthMode
	HTTP       *http.Client
	UserAgent  string
	// IntegrationID is sent as externalIntegrationId with every new
	// payment, session and link, so Paygate can tell they come from here.
	IntegrationID string

	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

// New creates a client. An empty auth mode defaults to Basic Auth.
func New(origin, merchantID, apiKey string, auth AuthMode) *Client {
	if auth == "" {
		auth = AuthBasic
	}
	return &Client{
		Origin:        strings.TrimRight(origin, "/"),
		MerchantID:    merchantID,
		APIKey:        apiKey,
		Auth:          auth,
		HTTP:          &http.Client{Timeout: 60 * time.Second},
		UserAgent:     "paygate-cli",
		IntegrationID: "paygate-cli",
	}
}

// Prepared is a fully built request that has not been sent yet. It backs both
// sending and the --curl export.
type Prepared struct {
	Method         string
	URL            string
	Body           []byte // pretty-printed JSON, nil for GET
	IdempotencyKey string
}

// APIURL returns the absolute URL for an API path such as "/payments".
func (c *Client) APIURL(path string) string {
	return c.Origin + apiPrefix + path
}

// TokenURL returns the OAuth2 token endpoint (outside /api/v2).
func (c *Client) TokenURL() string {
	return c.Origin + "/authorization/oauth/token"
}

// Prepare builds a request without sending it.
func (c *Client) Prepare(method, path string, body any, idempotencyKey string) (*Prepared, error) {
	p := &Prepared{Method: method, URL: c.APIURL(path), IdempotencyKey: idempotencyKey}
	if body != nil {
		b, err := json.MarshalIndent(body, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
		p.Body = b
	}
	return p, nil
}

// Response is the raw HTTP answer plus timing and tracing metadata.
type Response struct {
	StatusCode    int
	Header        http.Header
	Body          []byte
	CorrelationID string
	Duration      time.Duration
}

// Send executes a prepared request. Non-2xx answers are returned as *APIError
// together with the response.
func (c *Client) Send(ctx context.Context, p *Prepared) (*Response, error) {
	var body io.Reader
	if p.Body != nil {
		body = bytes.NewReader(p.Body)
	}
	req, err := http.NewRequestWithContext(ctx, p.Method, p.URL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)
	if p.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.IdempotencyKey != "" {
		req.Header.Set("Idempotency-Key", p.IdempotencyKey)
	}
	if err := c.authorize(ctx, req); err != nil {
		return nil, err
	}

	start := time.Now()
	httpResp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request to %s failed: %w", req.URL.Host, err)
	}
	defer httpResp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(httpResp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	resp := &Response{
		StatusCode:    httpResp.StatusCode,
		Header:        httpResp.Header,
		Body:          raw,
		CorrelationID: httpResp.Header.Get("X-Correlation-ID"),
		Duration:      time.Since(start),
	}
	if resp.StatusCode == http.StatusUnauthorized {
		c.mu.Lock()
		c.token = ""
		c.mu.Unlock()
		return resp, ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp, newAPIError(resp)
	}
	return resp, nil
}

func (c *Client) authorize(ctx context.Context, req *http.Request) error {
	switch c.Auth {
	case AuthOAuth2:
		tok, err := c.AccessToken(ctx)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	default:
		req.SetBasicAuth(c.MerchantID, c.APIKey)
	}
	return nil
}

// AccessToken fetches (and caches) an OAuth2 client-credentials token.
func (c *Client) AccessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Until(c.tokenExp) > 30*time.Second {
		return c.token, nil
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.MerchantID},
		"client_secret": {c.APIKey},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var tr struct {
		AccessToken      string `json:"access_token"`
		ExpiresIn        int    `json:"expires_in"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	_ = json.Unmarshal(raw, &tr)
	if resp.StatusCode != http.StatusOK || tr.AccessToken == "" {
		if resp.StatusCode == http.StatusUnauthorized || tr.Error == "invalid_client" {
			return "", fmt.Errorf("%w — OAuth2 token request was refused", ErrUnauthorized)
		}
		msg := tr.ErrorDescription
		if msg == "" {
			msg = tr.Error
		}
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return "", fmt.Errorf("OAuth2 token request failed (%d): %s", resp.StatusCode, msg)
	}
	if tr.ExpiresIn <= 0 {
		tr.ExpiresIn = 3600
	}
	c.token = tr.AccessToken
	c.tokenExp = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	return c.token, nil
}

// Verify checks the credentials without creating a payment: OAuth2 profiles
// request a token, Basic Auth profiles look up a non-existent transaction and
// accept "not found" as proof that authentication worked.
func (c *Client) Verify(ctx context.Context) error {
	if c.Auth == AuthOAuth2 {
		_, err := c.AccessToken(ctx)
		return err
	}
	_, err := c.GetByTransID(ctx, "PGCLIVERIFY"+fmt.Sprint(time.Now().Unix()))
	if err == nil || IsNotFound(err) {
		return nil
	}
	if e, ok := AsAPIError(err); ok && e.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w — the API key has no access to this merchant (403)", ErrUnauthorized)
	}
	return err
}
