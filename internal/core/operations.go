package core

import (
	"context"
	"time"

	"github.com/krebs3r/paygate-cli/internal/ids"
	"github.com/krebs3r/paygate-cli/internal/paygate"
)

// RemainingAmount is the default amount for a follow-up operation: what is
// left to capture, left to refund, or the full amount for a cancellation.
func RemainingAmount(op paygate.Operation, p *paygate.Payment) int64 {
	if p == nil || p.Amount == nil {
		return 0
	}
	a := p.Amount
	switch op {
	case paygate.OpCapture:
		if a.CapturedKnown {
			return a.Value - a.CapturedValue
		}
		return a.Value
	case paygate.OpRefund:
		base := a.Value
		if a.CapturedKnown && a.CapturedValue > 0 {
			base = a.CapturedValue
		}
		if a.RefundedKnown {
			return base - a.RefundedValue
		}
		return base
	default:
		return a.Value
	}
}

// OperationKind maps an operation to the history event kind.
func OperationKind(op paygate.Operation) string {
	switch op {
	case paygate.OpCapture:
		return "capture"
	case paygate.OpRefund:
		return "refund"
	default:
		return "cancel"
	}
}

// Operate runs a capture, refund or cancellation and records it.
func Operate(ctx context.Context, s *Session, op paygate.Operation, payID, transID string, amount paygate.Amount) (*paygate.Result, error) {
	c, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	body := &paygate.OperationRequest{TransID: transID, Amount: amount}
	res, err := s.Client.Operate(c, op, payID, body, ids.NewIdempotencyKey())
	RecordOperation(s.Name, transID, OperationKind(op), amount.Value, res, err)
	return res, err
}

// Lookup fetches a payment by payId and falls back to transId, then records
// its status.
func Lookup(ctx context.Context, s *Session, id string) (*paygate.Result, error) {
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := s.Client.GetByPayID(c, id)
	if paygate.IsNotFound(err) {
		res, err = s.Client.GetByTransID(c, id)
	}
	if err == nil {
		RecordStatus(s.Name, res.Payment)
	}
	return res, err
}
