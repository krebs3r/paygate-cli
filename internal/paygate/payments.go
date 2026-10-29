package paygate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// Result is a decoded payment answer plus the raw response.
type Result struct {
	Payment  *Payment
	Response *Response
}

func (c *Client) exec(ctx context.Context, p *Prepared) (*Result, error) {
	resp, err := c.Send(ctx, p)
	if err != nil {
		return &Result{Response: resp}, err
	}
	var pay Payment
	if len(resp.Body) > 0 {
		if err := json.Unmarshal(resp.Body, &pay); err != nil {
			return &Result{Response: resp}, fmt.Errorf("decode response: %w", err)
		}
	}
	return &Result{Payment: &pay, Response: resp}, nil
}

// Request builders. They are exported so --curl can render them unsent.

func (c *Client) CreateSessionRequest(r *PaymentRequest) (*Prepared, error) {
	return c.Prepare(http.MethodPost, "/payments/sessions", c.identify(r), r.TransID)
}

func (c *Client) CreatePaymentRequest(r *PaymentRequest) (*Prepared, error) {
	return c.Prepare(http.MethodPost, "/payments", c.identify(r), r.TransID)
}

func (c *Client) CreateLinkRequest(r *PaymentRequest) (*Prepared, error) {
	return c.Prepare(http.MethodPost, "/payments/links", c.identify(r), r.TransID)
}

// identify returns a copy of the request with the client's integration ID,
// unless the request names one of its own. Captures, refunds and reversals
// have no such field.
func (c *Client) identify(r *PaymentRequest) *PaymentRequest {
	if r.ExternalIntegrationID != "" || c.IntegrationID == "" {
		return r
	}
	cp := *r
	cp.ExternalIntegrationID = c.IntegrationID
	return &cp
}

func (c *Client) GetByPayIDRequest(payID string) (*Prepared, error) {
	return c.Prepare(http.MethodGet, "/payments/getByPayId/"+url.PathEscape(payID), nil, "")
}

func (c *Client) GetByTransIDRequest(transID string) (*Prepared, error) {
	return c.Prepare(http.MethodGet, "/payments/getByTransId/"+url.PathEscape(transID), nil, "")
}

// Operation is a follow-up action on an existing payment.
type Operation string

const (
	OpCapture  Operation = "captures"
	OpRefund   Operation = "refunds"
	OpReversal Operation = "reversals"
)

func (c *Client) OperationRequest(op Operation, payID string, r *OperationRequest, idempotencyKey string) (*Prepared, error) {
	return c.Prepare(http.MethodPost, "/payments/"+url.PathEscape(payID)+"/"+string(op), r, idempotencyKey)
}

// High-level calls.

func (c *Client) CreateSession(ctx context.Context, r *PaymentRequest) (*Result, error) {
	p, err := c.CreateSessionRequest(r)
	if err != nil {
		return nil, err
	}
	return c.exec(ctx, p)
}

func (c *Client) CreatePayment(ctx context.Context, r *PaymentRequest) (*Result, error) {
	p, err := c.CreatePaymentRequest(r)
	if err != nil {
		return nil, err
	}
	return c.exec(ctx, p)
}

func (c *Client) CreateLink(ctx context.Context, r *PaymentRequest) (*Result, error) {
	p, err := c.CreateLinkRequest(r)
	if err != nil {
		return nil, err
	}
	return c.exec(ctx, p)
}

func (c *Client) GetByPayID(ctx context.Context, payID string) (*Result, error) {
	p, err := c.GetByPayIDRequest(payID)
	if err != nil {
		return nil, err
	}
	return c.exec(ctx, p)
}

func (c *Client) GetByTransID(ctx context.Context, transID string) (*Result, error) {
	p, err := c.GetByTransIDRequest(transID)
	if err != nil {
		return nil, err
	}
	return c.exec(ctx, p)
}

func (c *Client) Operate(ctx context.Context, op Operation, payID string, r *OperationRequest, idempotencyKey string) (*Result, error) {
	p, err := c.OperationRequest(op, payID, r, idempotencyKey)
	if err != nil {
		return nil, err
	}
	return c.exec(ctx, p)
}

// Exec sends an already prepared request and decodes the payment answer.
func (c *Client) Exec(ctx context.Context, p *Prepared) (*Result, error) {
	return c.exec(ctx, p)
}
