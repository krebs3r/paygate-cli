package tour

import (
	"context"
	"net"
	"net/url"

	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/mockpaygate"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

// sessionStep creates a checkout session; the hosted and the webhooks tour
// start with it.
func sessionStep(arrowLabel T) *Step {
	return &Step{
		Short: T{"Session", "Session"},
		Title: T{"Create a checkout session", "Checkout-Session anlegen"},
		Explain: T{
			"Your server creates a checkout session. Paygate stores the amount, the transId and the three URLs and returns the link to the payment page. No money has moved yet, and the customer hasn't seen anything.",
			"Dein Server legt eine Checkout-Session an. Paygate speichert Betrag, transId und die drei URLs und schickt den Link zur Bezahlseite zurück. Bis hierhin ist noch kein Geld geflossen, und der Kunde hat noch nichts gesehen.",
		},
		Doc:  docSession,
		Kind: Request,
		Arrows: []Arrow{
			{0, 1, arrowLabel},
			{1, 0, T{"202 · _links.redirect", "202 · _links.redirect"}},
		},
		Plan: func(r *Run) (*Planned, error) {
			notes := baseNotes()
			notes["urls.return"], notes["urls.cancel"], notes["urls.webhook"] = noteReturn, noteCancel, noteWebhook
			return payment(r, core.PaymentInput{Flow: core.FlowHosted, Capture: "manual", Amount: "49.90"}, notes)
		},
		After: func(r *Run, res *paygate.Result, o *Outcome) {
			r.TransID, r.Amount = requestOf(r.planned[r.Step])
			r.Redirect = res.Payment.RedirectURL()
			if r.Redirect != "" {
				o.Status = ui.Success.Render("✓ ") + T{"Link to the payment page received.", "Link zur Bezahlseite erhalten."}.String()
			}
		},
	}
}

// payStep waits until the customer paid, declined or cancelled; with open
// it first opens the payment page.
func payStep(open bool) *Step {
	st := &Step{
		Short: T{"Pay", "Bezahlen"},
		Title: T{"The customer pays", "Der Kunde bezahlt"},
		Explain: T{
			"The customer picks a payment method and pays, or declines or cancels. Your server doesn't know about it yet. Until the customer has opened the page, the status lookup may not find the payment; after that it shows as waiting (AUTHORIZE_REQUEST).",
			"Der Kunde wählt eine Zahlart und bezahlt. Er kann auch ablehnen oder abbrechen. Dein Server bekommt davon zunächst nichts mit. Solange der Kunde die Seite nicht geöffnet hat, findet die Statusabfrage die Zahlung eventuell noch nicht. Danach steht sie auf „wartend“ (AUTHORIZE_REQUEST).",
		},
		Doc:    docHosted,
		Kind:   Wait,
		Arrows: []Arrow{{2, 1, T{"customer pays", "Kunde zahlt"}}},
		Do:     waitCustomer,
	}
	if open {
		st.Arrows = append([]Arrow{{0, 2, T{"redirect to the payment page", "Redirect zur Bezahlseite"}}}, st.Arrows...)
		st.Do = func(ctx context.Context, r *Run) *Outcome {
			if !r.opened {
				r.opened = true
				_ = openStep(ctx, r)
			}
			return waitCustomer(ctx, r)
		}
	}
	return st
}

// statusStep looks up the payment of the tour by its transId.
func statusStep(explain T) *Step {
	return &Step{
		Short:   T{"Status", "Status"},
		Title:   T{"Look up the status", "Status abfragen"},
		Explain: explain,
		Doc:     docStatus,
		Kind:    Request,
		Arrows: []Arrow{
			{0, 1, T{"GET getByTransId/{id}", "GET getByTransId/{id}"}},
			{1, 0, T{"200 · status", "200 · status"}},
		},
		Plan: func(r *Run) (*Planned, error) { return lookup(r, r.TransID) },
		After: func(r *Run, res *paygate.Result, o *Outcome) {
			rememberPayment(r, res)
			o.Status = statusLine(res.Payment)
		},
	}
}

func hostedTour() *Tour {
	return &Tour{
		ID:    "hosted",
		Title: T{"Hosted Payment Page", "Hosted Payment Page"},
		Shows: T{"session, redirect, webhook, status, capture", "Session, Weiterleitung, Webhook, Status, Capture"},
		Lanes: []T{laneServer, lanePaygate, laneBrowser},
		Steps: []*Step{
			sessionStep(T{"POST /payments/sessions", "POST /payments/sessions"}),
			{
				Short: T{"Redirect", "Weiterleitung"},
				Title: T{"Redirect the customer to the payment page", "Kunden zur Bezahlseite weiterleiten"},
				Explain: T{
					"Your shop redirects the customer to the link from step 1 (_links.redirect.href), usually with an HTTP redirect. From here on, the payment happens between the customer and Paygate. Card data never reaches your server.",
					"Dein Shop leitet den Kunden auf den Link aus Schritt 1 weiter (_links.redirect.href), meist per HTTP-Redirect. Ab jetzt läuft die Zahlung direkt zwischen Kunde und Paygate. Kartendaten kommen nie bei deinem Server an.",
				},
				Doc:    docHosted,
				Kind:   Browser,
				Next:   true,
				Arrows: []Arrow{{0, 2, T{"redirect to the payment page", "Redirect zur Bezahlseite"}}},
				Do:     openStep,
			},
			payStep(false),
			{
				Short: T{"Return", "Rückkehr"},
				Title: T{"Back to the shop", "Rückkehr in den Shop"},
				Explain: T{
					"After paying, Paygate sends the customer to urls.return; after cancelling, to urls.cancel. That page shows the result in your shop. But there is no guarantee the customer comes back, for example if they close the tab or lose the connection. So rely on the webhook and the status lookup.",
					"Nach der Zahlung schickt Paygate den Kunden auf urls.return, nach einem Abbruch auf urls.cancel. Dort zeigt dein Shop das Ergebnis an. Dass der Kunde wirklich zurückkommt, ist aber nicht garantiert, etwa wenn er den Tab schließt oder die Verbindung abbricht. Verlass dich deshalb auf Webhook und Statusabfrage.",
				},
				Doc:    docHosted,
				Kind:   Browser,
				Auto:   true,
				Arrows: []Arrow{{2, 0, T{"GET urls.return", "GET urls.return"}}},
				Do:     returnStep,
			},
			{
				Short: T{"Webhook", "Webhook"},
				Title: T{"Paygate reports the result", "Paygate meldet das Ergebnis"},
				Explain: T{
					"Paygate posts the result to urls.webhook, even if the customer never comes back to the shop. An Enhanced webhook carries the result in its body and is signed with your HMAC key. A Basic webhook carries only the payId.",
					"Paygate schickt das Ergebnis per POST an urls.webhook, auch wenn der Kunde nicht in den Shop zurückkommt. Ein Enhanced-Webhook enthält das Ergebnis im Body und ist mit deinem HMAC-Key signiert. Ein Basic-Webhook enthält nur die payId.",
				},
				Doc:    docWebhooks,
				Kind:   Wait,
				Arrows: []Arrow{{1, 0, T{"webhook to urls.webhook", "Webhook an urls.webhook"}}},
				Do:     waitWebhook,
			},
			statusStep(T{
				"Your server looks up the status using the transId from step 1. Whether you ship depends on status and responseCode: OK with 00000000 means successful.",
				"Dein Server fragt den Status über die transId aus Schritt 1 ab. Ob du die Ware verschickst, entscheiden status und responseCode: OK mit 00000000 bedeutet erfolgreich.",
			}),
			captureStep(),
		},
	}
}

// captureStep captures the full authorized amount of the tour's payment.
func captureStep() *Step {
	return &Step{
		Short: T{"Capture", "Capture"},
		Title: T{"Capture the amount", "Betrag einziehen (Capture)"},
		Explain: T{
			"With captureMethod MANUAL the amount is only authorized at first. Once you ship, you capture it, in full or in parts.",
			"Mit captureMethod MANUAL ist der Betrag zunächst nur autorisiert. Sobald du die Ware verschickst, ziehst du ihn per Capture ein, ganz oder in Teilbeträgen.",
		},
		Doc:  docCapture,
		Kind: Request,
		Arrows: []Arrow{
			{0, 1, T{"POST /{payId}/captures", "POST /{payId}/captures"}},
			{1, 0, T{"200 · OK", "200 · OK"}},
		},
		Skip: func(r *Run) T {
			if r.Payment == nil || paygate.OutcomeOf(r.Payment) != paygate.OutcomeSuccess {
				return T{"The payment was not successful, so there is nothing to capture.", "Die Zahlung war nicht erfolgreich, es gibt nichts einzuziehen."}
			}
			if a := r.Payment.Amount; a != nil && a.CapturedKnown && a.CapturedValue >= a.Value {
				return T{"The amount is captured already.", "Der Betrag ist bereits eingezogen."}
			}
			return T{}
		},
		Plan: func(r *Run) (*Planned, error) {
			return operation(r, paygate.OpCapture, r.PayID, r.TransID, r.Amount)
		},
		After: func(r *Run, res *paygate.Result, o *Outcome) {
			o.Status = statusLine(res.Payment)
		},
	}
}

// returnStep tells where the customer went after the payment page, by how
// the payment ended in step 3.
func returnStep(ctx context.Context, r *Run) *Outcome {
	p := r.Session.Profile
	used, status := "", ""
	switch {
	case r.Payment == nil:
		status = ui.Muted.Render(T{"The payment from step 3 is not known.", "Die Zahlung aus Schritt 3 ist nicht bekannt."}.String())
	case paygate.OutcomeOf(r.Payment) == paygate.OutcomeSuccess:
		used = "return"
		status = ui.Success.Render("✓ ") + T{"Paid: Paygate sends the customer back to urls.return.", "Bezahlt: Paygate schickt den Kunden zurück auf urls.return."}.String()
	case r.Demo() && string(r.Payment.ResponseCode) == mockpaygate.CancelCode:
		used = "cancel"
		status = ui.Warning.Render("↩ ") + T{"Cancelled: Paygate sends the customer to urls.cancel.", "Abgebrochen: Paygate schickt den Kunden auf urls.cancel."}.String()
	case r.Demo():
		used = "return"
		status = ui.Danger.Render("✗ ") + T{"Declined: the demo checkout sends the customer to urls.return.", "Abgelehnt: Die Demo-Kasse schickt den Kunden auf urls.return."}.String()
	default:
		status = ui.Danger.Render("✗ ") + T{"Not paid: the customer goes to urls.return, or to urls.cancel after cancelling.", "Nicht bezahlt: Der Kunde landet auf urls.return oder, nach einem Abbruch, auf urls.cancel."}.String()
	}
	line := func(name, u string, on bool) string {
		if on {
			return ui.Accent.Render("→ ") + ui.Bold.Render(name+"  ") + ui.Link.Render(u)
		}
		return "  " + ui.Muted.Render(name+"  "+u)
	}
	lines := []string{line("urls.return", p.Return(), used == "return"), line("urls.cancel", p.Cancel(), used == "cancel")}
	if r.Demo() && used != "" {
		target := p.Return()
		if used == "cancel" {
			target = p.Cancel()
		}
		host := target
		if u, err := url.Parse(target); err == nil && u.Host != "" {
			host = u.Host
		}
		note := T{"The demo checkout forwarded the browser to " + host + ".", "Die Demo-Kasse hat den Browser an " + host + " weitergeleitet."}
		if !localURL(target) {
			// The checkout runs on this computer; the shop address is on the
			// internet, and the demo calls nothing there.
			note = T{"The demo checkout does not really forward to " + host + ", so the browser calls nothing on the internet during the demo. It shows its own result page instead.",
				"Die Demo-Kasse leitet nicht wirklich an " + host + " weiter, damit der Browser in der Demo nichts im Internet aufruft. Sie zeigt stattdessen ihre eigene Ergebnisseite."}
		}
		lines = append(lines, "", ui.Label.Render(note.String()))
	}
	return &Outcome{Status: status, Lines: lines}
}

// localURL reports whether a URL points to this computer.
func localURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Hostname() == "localhost" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}
