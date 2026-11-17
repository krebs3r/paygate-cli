package tour

import (
	"github.com/krebs3r/paygate-cli/internal/paygate"
)

func operationsTour() *Tour {
	lanes := []T{laneServer, lanePaygate}
	return &Tour{
		ID:    "operations",
		Title: T{"Capture, refund, reversal", "Capture, Refund, Reversal"},
		Shows: T{"authorize, partial capture, partial refund, reversal", "Autorisieren, Teil-Capture, Teil-Refund, Reversal"},
		Lanes: lanes,
		Steps: []*Step{
			{
				Short: T{"Authorize", "Autorisieren"},
				Title: T{"Authorize the amount", "Betrag autorisieren"},
				Explain: T{
					"With captureMethod MANUAL, the amount is only authorized on the card, that is, reserved. It is captured only when you send a capture, for example when you ship.",
					"Mit captureMethod MANUAL wird der Betrag auf der Karte nur autorisiert, also vorgemerkt. Eingezogen wird er erst, wenn du einen Capture schickst, zum Beispiel beim Versand.",
				},
				Doc:  docCreate,
				Kind: Request,
				Arrows: []Arrow{
					{0, 1, T{"POST /payments (MANUAL)", "POST /payments (MANUAL)"}},
					{1, 0, T{"201 · authorized", "201 · autorisiert"}},
				},
				Plan: directPayment("visa-ok", "manual", "49.90", T{"test card without 3DS", "Testkarte ohne 3DS"}),
				After: func(r *Run, res *paygate.Result, o *Outcome) {
					rememberPayment(r, res)
					o.Status = statusLine(res.Payment)
				},
			},
			{
				Short: T{"Capture", "Teil-Capture"},
				Title: T{"Capture part of the amount", "Teilbetrag einziehen"},
				Explain: T{
					"You can also capture part of the amount, for example for partial deliveries. transId and amount are required; amount is the amount of this one capture. Here: 30.00 of 49.90 EUR.",
					"Du kannst auch Teilbeträge einziehen, etwa bei Teillieferungen. transId und amount sind Pflichtfelder; amount ist der Betrag dieses einen Captures. Hier sind es 30,00 von 49,90 EUR.",
				},
				Doc:  docCapture,
				Kind: Request,
				Arrows: []Arrow{
					{0, 1, T{"POST /{payId}/captures", "POST /{payId}/captures"}},
					{1, 0, T{"200 · OK", "200 · OK"}},
				},
				Skip: needsSuccess,
				Plan: func(r *Run) (*Planned, error) {
					return operation(r, paygate.OpCapture, r.PayID, r.TransID, amountOf(r, "30.00"))
				},
				After: func(r *Run, res *paygate.Result, o *Outcome) { o.Status = statusLine(res.Payment) },
			},
			{
				Short: T{"Refund", "Teil-Refund"},
				Title: T{"Refund part of the amount", "Teilbetrag erstatten"},
				Explain: T{
					"A refund pays back an amount you have captured, in full or in part. You can refund at most what was captured. Here: 10.00 of the 30.00 EUR.",
					"Mit einem Refund erstattest du einen bereits eingezogenen Betrag, ganz oder teilweise. Du kannst höchstens so viel erstatten, wie eingezogen wurde. Hier sind es 10,00 von den 30,00 EUR.",
				},
				Doc:  docRefund,
				Kind: Request,
				Arrows: []Arrow{
					{0, 1, T{"POST /{payId}/refunds", "POST /{payId}/refunds"}},
					{1, 0, T{"200 · OK", "200 · OK"}},
				},
				Skip: needsSuccess,
				Plan: func(r *Run) (*Planned, error) {
					return operation(r, paygate.OpRefund, r.PayID, r.TransID, amountOf(r, "10.00"))
				},
				After: func(r *Run, res *paygate.Result, o *Outcome) { o.Status = statusLine(res.Payment) },
			},
			{
				Short: T{"Authorize", "Autorisieren"},
				Title: T{"A second authorization", "Zweite Autorisierung"},
				Explain: T{
					"A second order that the customer cancels before you ship. It is also only authorized at first, with captureMethod MANUAL.",
					"Eine zweite Bestellung, die der Kunde vor dem Versand storniert. Auch sie wird mit captureMethod MANUAL zunächst nur autorisiert.",
				},
				Doc:  docCreate,
				Kind: Request,
				Arrows: []Arrow{
					{0, 1, T{"POST /payments (MANUAL)", "POST /payments (MANUAL)"}},
					{1, 0, T{"201 · authorized", "201 · autorisiert"}},
				},
				Plan: directPayment("visa-ok", "manual", "20.00", T{"test card without 3DS", "Testkarte ohne 3DS"}),
				After: func(r *Run, res *paygate.Result, o *Outcome) {
					if p := res.Payment; p != nil {
						r.SecondTransID, r.SecondPayID = p.TransID, p.PayID
					}
					o.Status = statusLine(res.Payment)
				},
			},
			{
				Short: T{"Reversal", "Reversal"},
				Title: T{"Release the authorization", "Autorisierung aufheben"},
				Explain: T{
					"A reversal releases an authorization you no longer want to capture, for example when an order is cancelled before shipping. The amount is then no longer reserved on the customer's card.",
					"Mit einem Reversal hebst du eine Autorisierung auf, die du nicht mehr einziehen willst, zum Beispiel wenn eine Bestellung vor dem Versand storniert wird. Der Betrag ist danach auf der Karte des Kunden nicht mehr vorgemerkt.",
				},
				Doc:  docReverse,
				Kind: Request,
				Arrows: []Arrow{
					{0, 1, T{"POST /{payId}/reversals", "POST /{payId}/reversals"}},
					{1, 0, T{"200 · OK", "200 · OK"}},
				},
				Plan: func(r *Run) (*Planned, error) {
					return operation(r, paygate.OpReversal, r.SecondPayID, r.SecondTransID, amountOf(r, "20.00"))
				},
				After: func(r *Run, res *paygate.Result, o *Outcome) { o.Status = statusLine(res.Payment) },
			},
		},
	}
}

// needsSuccess skips an operation when the payment of the tour failed.
func needsSuccess(r *Run) T {
	if r.Payment == nil || paygate.OutcomeOf(r.Payment) != paygate.OutcomeSuccess {
		return T{"The payment from step 1 was not successful.", "Die Zahlung aus Schritt 1 war nicht erfolgreich."}
	}
	return T{}
}
