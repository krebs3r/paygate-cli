package tour

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/browser"

	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/demo"
	"github.com/krebs3r/paygate-cli/internal/ids"
	"github.com/krebs3r/paygate-cli/internal/money"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

// Lanes of the sequence diagrams.
var (
	laneServer  = T{"Server", "Server"}
	lanePaygate = T{"Paygate", "Paygate"}
	laneBrowser = T{"Browser", "Browser"}
)

// notRun marks a step that happens in the customer's browser, which
// paygate cannot run.
var notRun = T{"not run, happens in the customer's browser", "nicht ausgeführt, läuft im Browser des Kunden"}

// OpenURL opens a page in the browser; tests replace it.
var OpenURL = browser.OpenURL

// Field notes shared by the tours.
var (
	noteTransID      = T{"required · your unique ID", "Pflicht · deine eindeutige ID"}
	noteValue        = T{"required · in the smallest unit (cents)", "Pflicht · in der kleinsten Einheit (Cent)"}
	noteCurrency     = T{"required · ISO 4217", "Pflicht · ISO 4217"}
	noteLanguage     = T{"optional · language for the customer", "optional · Sprache für den Kunden"}
	noteReference    = T{"optional · your order number", "optional · deine Bestellnummer"}
	noteDescription  = T{"optional · short order description", "optional · kurze Bestellbeschreibung"}
	noteIntegration  = T{"optional · name of your integration", "optional · Name deiner Integration"}
	noteManual       = T{"MANUAL · authorize now, capture later", "MANUAL · erst autorisieren, später einziehen"}
	noteAutomatic    = T{"AUTOMATIC · captured right away", "AUTOMATIC · sofort eingezogen"}
	noteReturn       = T{"required · where the browser goes after paying", "Pflicht · Rücksprung nach der Zahlung"}
	noteCancel       = T{"required · where the browser goes after cancelling", "Pflicht · Rücksprung nach einem Abbruch"}
	noteWebhook      = T{"required · notification to your server", "Pflicht · Benachrichtigung an deinen Server"}
	noteOpTransID    = T{"required · transId of the payment", "Pflicht · transId der Zahlung"}
	noteOpValue      = T{"required · amount of this operation", "Pflicht · Betrag dieser Operation"}
	noteDirectURLs   = T{"not used with DIRECT", "wird bei DIRECT nicht genutzt"}
	noteChannel      = T{"optional · ECOM for online shops", "optional · ECOM für Onlineshops"}
	noteBrowserInfo  = T{"the customer's browser, for 3-D Secure", "Browser des Kunden, für 3-D Secure"}
	noteIP           = T{"IP address of the customer", "IP-Adresse des Kunden"}
	noteMethodType   = T{"CARD", "CARD"}
	noteIntegrationT = T{"DIRECT · server-to-server", "DIRECT · Server-zu-Server"}
	noteSecurityCode = T{"CVC · never store it", "CVC · niemals speichern"}
	noteExpiry       = T{"YYYYMM", "JJJJMM"}
)

func baseNotes() map[string]T {
	return map[string]T{
		"transId": noteTransID, "amount.value": noteValue, "amount.currency": noteCurrency,
		"language": noteLanguage, "order.merchantReference": noteReference, "order.description": noteDescription,
		"externalIntegrationId": noteIntegration,
	}
}

// payment plans a payment of the tour: the request as `paygate pay`
// builds it, kept in the history like every payment.
func payment(r *Run, in core.PaymentInput, notes map[string]T) (*Planned, error) {
	if in.Amount == "" {
		in.Amount = core.DefaultAmount
	}
	if in.WebhookURL == "" {
		in.WebhookURL = r.WebhookURL()
	}
	req, label, err := core.BuildPaymentRequest(in, r.Session.Profile)
	if err != nil {
		return nil, err
	}
	prep, err := core.Prepare(r.Session.Client, in.Flow, req)
	if err != nil {
		return nil, err
	}
	if req.CaptureMethod != nil {
		switch req.CaptureMethod.Type {
		case "MANUAL":
			notes["captureMethod.type"] = noteManual
		case "AUTOMATIC":
			notes["captureMethod.type"] = noteAutomatic
		}
	}
	return &Planned{Prepared: prep, Notes: notes, Record: func(res *paygate.Result, err error) {
		core.RecordPayment(r.Session.Name, r.Session.Profile, in.Flow, req, label, res, err)
	}}, nil
}

// lookup plans a status request by transId.
func lookup(r *Run, transID string) (*Planned, error) {
	if transID == "" {
		return nil, errEarlierStep
	}
	prep, err := r.Session.Client.GetByTransIDRequest(transID)
	if err != nil {
		return nil, err
	}
	return &Planned{Prepared: prep, Record: func(res *paygate.Result, err error) {
		if err == nil && res != nil {
			core.RecordStatus(r.Session.Name, res.Payment)
		}
	}}, nil
}

// operation plans a capture, refund or reversal.
func operation(r *Run, op paygate.Operation, payID, transID string, amount paygate.Amount) (*Planned, error) {
	if payID == "" {
		return nil, errEarlierStep
	}
	prep, err := r.Session.Client.OperationRequest(op, payID, &paygate.OperationRequest{TransID: transID, Amount: amount}, ids.NewIdempotencyKey())
	if err != nil {
		return nil, err
	}
	notes := map[string]T{"transId": noteOpTransID, "amount.value": noteOpValue, "amount.currency": noteCurrency}
	return &Planned{Prepared: prep, Notes: notes, Record: func(res *paygate.Result, err error) {
		core.RecordOperation(r.Session.Name, transID, core.OperationKind(op), amount.Value, res, err)
	}}, nil
}

var errEarlierStep = fmt.Errorf("run the steps before this one first")

// rememberPayment keeps transId, payId and amount of a payment answer.
func rememberPayment(r *Run, res *paygate.Result) {
	if p := res.Payment; p != nil {
		if p.TransID != "" {
			r.TransID = p.TransID
		}
		if p.PayID != "" {
			r.PayID = p.PayID
		}
		if p.Amount != nil && p.Amount.Value > 0 {
			r.Amount = paygate.Amount{Value: p.Amount.Value, Currency: p.Amount.Currency}
		}
		r.Payment = p
	}
}

// requestOf reads transId and amount of the planned request, for answers
// that do not repeat them (a session answers with the link only).
func requestOf(p *Planned) (string, paygate.Amount) {
	var body struct {
		TransID string         `json:"transId"`
		Amount  paygate.Amount `json:"amount"`
	}
	_ = jsonUnmarshal(p.Prepared.Body, &body)
	return body.TransID, body.Amount
}

// statusLine describes a payment's state in one line.
func statusLine(p *paygate.Payment) string {
	if p == nil {
		return ""
	}
	icon := ui.Warning.Render("…")
	switch paygate.OutcomeOf(p) {
	case paygate.OutcomeSuccess:
		icon = ui.Success.Render("✓")
	case paygate.OutcomeFailed:
		icon = ui.Danger.Render("✗")
	}
	line := icon + " " + ui.Bold.Render(p.Status) + ui.Muted.Render(" · "+string(p.ResponseCode)+" "+p.ResponseDescription)
	if a := p.Amount; a != nil && a.Value > 0 {
		line += ui.Muted.Render(" · ") + money.Format(a.Value, a.Currency)
		if a.CapturedKnown || a.RefundedKnown {
			line += ui.Muted.Render(fmt.Sprintf(" · %s %s · %s %s",
				T{"captured", "eingezogen"}, money.Format(a.CapturedValue, a.Currency),
				T{"refunded", "erstattet"}, money.Format(a.RefundedValue, a.Currency)))
		}
	}
	return line
}

// openStep opens the payment page of the tour in the browser.
func openStep(ctx context.Context, r *Run) *Outcome {
	if r.Redirect == "" {
		return &Outcome{Err: errEarlierStep}
	}
	o := &Outcome{Lines: []string{ui.Link.Render(r.Redirect)}}
	if err := OpenURL(r.Redirect); err != nil {
		o.Status = T{"Open the link in a browser yourself.", "Öffne den Link selbst im Browser."}.String()
	} else {
		o.Status = T{"Opened in the browser.", "Im Browser geöffnet."}.String()
	}
	return o
}

// waitCustomer looks up the payment until the customer has decided.
func waitCustomer(ctx context.Context, r *Run) *Outcome {
	if r.TransID == "" {
		return &Outcome{Err: errEarlierStep}
	}
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	p, err := lookup(r, r.TransID)
	if err != nil {
		return &Outcome{Err: err}
	}
	res, err := r.Session.Client.Exec(c, p.Prepared)
	if paygate.IsNotFound(err) {
		return &Outcome{Pending: true, Status: T{
			"Paygate doesn't know the payment yet: the customer hasn't opened the payment page.",
			"Paygate kennt die Zahlung noch nicht, weil der Kunde die Bezahlseite noch nicht geöffnet hat."}.String()}
	}
	if err != nil {
		return &Outcome{Pending: true, Status: err.Error()}
	}
	pay := res.Payment
	if core.WaitOutcome(core.FlowHosted, pay) == paygate.OutcomePending {
		return &Outcome{Pending: true, Status: T{"Waiting for the customer", "Wartet auf den Kunden"}.String() +
			ui.Muted.Render(" · "+pay.Status+" "+string(pay.ResponseCode))}
	}
	core.RecordStatus(r.Session.Name, pay)
	rememberPayment(r, res)
	// The last lookup is the answer of the step.
	return &Outcome{Status: statusLine(pay), Exchange: r.exchange(p, res)}
}

// waitWebhook waits until the webhook of the tour's payment arrived.
func waitWebhook(ctx context.Context, r *Run) *Outcome {
	if r.PayID == "" {
		return &Outcome{Err: errEarlierStep}
	}
	if r.Demo() && webhooksOff() {
		return &Outcome{Skipped: T{
			"The demo scenarios turn webhooks off (Settings, Demo scenarios), so none comes.",
			"In den Demo-Szenarien sind Webhooks ausgeschaltet (Einstellungen, Demo-Szenarien), deshalb kommt keiner."}.String()}
	}
	if r.WebhookURL() == "" {
		return &Outcome{Skipped: T{
			"Paygate only sends webhooks to public HTTPS addresses. Start the webhook listener (Webhooks, space) and then restart the tour to see it here.",
			"Paygate schickt Webhooks nur an öffentliche HTTPS-Adressen. Starte den Webhook-Empfänger (Webhooks, Leertaste) und danach die Tour neu, dann siehst du ihn hier."}.String()}
	}
	w := r.FindWebhook(r.PayID)
	if w == nil {
		return &Outcome{Pending: true, Status: T{"Waiting for the webhook", "Wartet auf den Webhook"}.String()}
	}
	r.Webhook = w
	return &Outcome{Webhook: w, Status: webhookSummary(w, r.HMACKey())}
}

// webhooksOff reports whether the demo scenarios turn webhooks off.
func webhooksOff() bool {
	if p := demo.Running(); p != nil {
		return p.Scenarios().Get("webhook") == "off"
	}
	return false
}

// webhookSummary says in one line what kind of webhook arrived and whether
// its signature matches.
func webhookSummary(w *Webhook, key string) string {
	c := w.Check(key)
	kind := string(c.Kind)
	switch {
	case c.Valid:
		return ui.Success.Render("✓ ") + T{kind + " webhook, signature matches", kind + "-Webhook, Signatur stimmt"}.String()
	case c.Checked:
		return ui.Danger.Render("✗ ") + T{kind + " webhook, signature does not match", kind + "-Webhook, Signatur stimmt nicht"}.String()
	case !c.Signed:
		return ui.Success.Render("✓ ") + T{kind + " webhook, without signature", kind + "-Webhook, ohne Signatur"}.String()
	}
	return ui.Warning.Render("! ") + T{kind + " webhook, signature not checked (no HMAC key)", kind + "-Webhook, Signatur nicht geprüft (kein HMAC-Key)"}.String()
}

// amountOf builds an amount in the profile's currency.
func amountOf(r *Run, major string) paygate.Amount {
	cur := r.Session.Profile.DefaultCurrency()
	v, _ := money.Parse(major, cur)
	return paygate.Amount{Value: v, Currency: cur}
}

// fmtAmount formats an amount for texts.
func fmtAmount(a paygate.Amount) string { return money.Format(a.Value, a.Currency) }

// snippet styles a code example.
func snippet(lines ...string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = ui.Text.Render(l)
	}
	return out
}

func short(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

func lower(s string) string { return strings.ToLower(s) }

var jsonUnmarshal = json.Unmarshal
