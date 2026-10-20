package core

import (
	"time"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/history"
	"github.com/krebs3r/paygate-cli/internal/paygate"
)

// RecordPayment stores a payment response (or error) in the local history.
// Errors are ignored: history is a convenience and must never break a payment.
func RecordPayment(profileName string, p *config.Profile, flow Flow, req *paygate.PaymentRequest, method string, res *paygate.Result, err error) {
	rec := history.Record{
		Profile:    profileName,
		Env:        p.Env,
		Brand:      p.Brand,
		MerchantID: p.MerchantID,
		Flow:       string(flow),
		TransID:    req.TransID,
		Amount:     req.Amount.Value,
		Currency:   req.Amount.Currency,
		Method:     method,
	}
	if req.Order != nil && len(req.Order.Description) == 9 && req.Order.Description[:5] == "Test:" {
		rec.Simulation = req.Order.Description
	}
	pay, resp := extract(res, err)
	ev := history.Event{Time: time.Now(), Kind: "create"}
	if resp != nil {
		ev.HTTP, ev.Duration = resp.StatusCode, resp.Duration.Milliseconds()
	}
	if pay != nil {
		applyPayment(&rec, pay)
		ev.Status, ev.Code = rec.Status, rec.Code
		if pay.RedirectURL() != "" && rec.Status == "" {
			rec.Status = "REDIRECT"
		}
	}
	rec.Events = []history.Event{ev}
	_ = history.Upsert(rec)
}

// RecordStatus stores the result of a status lookup.
func RecordStatus(profileName string, p *paygate.Payment) {
	if p == nil || p.TransID == "" {
		return
	}
	rec := history.Record{Profile: profileName, TransID: p.TransID}
	applyPayment(&rec, p)
	if p.Amount != nil {
		rec.Amount, rec.Currency = p.Amount.Value, p.Amount.Currency
		rec.Captured, rec.Refunded = p.Amount.CapturedValue, p.Amount.RefundedValue
	}
	rec.Events = []history.Event{{Time: time.Now(), Kind: "status", Status: rec.Status, Code: rec.Code}}
	_ = history.Upsert(rec)
}

// RecordOperation stores a capture, refund or cancellation.
func RecordOperation(profileName, transID, kind string, amount int64, res *paygate.Result, err error) {
	pay, resp := extract(res, err)
	ev := history.Event{Time: time.Now(), Kind: kind, Amount: amount}
	if resp != nil {
		ev.HTTP, ev.Duration = resp.StatusCode, resp.Duration.Milliseconds()
	}
	if pay != nil {
		ev.Status, ev.Code = pay.Status, string(pay.ResponseCode)
	}
	_ = history.Upsert(history.Record{Profile: profileName, TransID: transID, Events: []history.Event{ev}})
}

func extract(res *paygate.Result, err error) (*paygate.Payment, *paygate.Response) {
	if e, ok := paygate.AsAPIError(err); ok {
		return e.Payment, e.Response
	}
	if res == nil {
		return nil, nil
	}
	return res.Payment, res.Response
}

func applyPayment(rec *history.Record, p *paygate.Payment) {
	rec.PayID = p.PayID
	rec.Status = p.Status
	rec.Code = string(p.ResponseCode)
	rec.Description = p.ResponseDescription
	if m := p.Method().Label(); m != "" {
		rec.Method = m
	}
}
