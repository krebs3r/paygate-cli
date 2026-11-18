package tour

import (
	"context"

	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/mockpaygate"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

func formsTour() *Tour {
	return &Tour{
		ID:    "forms",
		Title: T{"Hosted Forms", "Hosted Forms"},
		Shows: T{"method picked in your shop, return with payId", "Zahlart im Shop gewählt, Rückkehr mit payId"},
		Lanes: []T{laneServer, lanePaygate, laneBrowser},
		Steps: []*Step{
			{
				Short: T{"Payment", "Zahlung"},
				Title: T{"Create a payment with a hosted form", "Zahlung mit Formular anlegen"},
				Explain: T{
					"Your shop shows the payment methods itself. Once the customer has picked one, your server creates the payment with exactly this method and integrationType HOSTED. Paygate answers with 202 and the link to the form. The tour takes credit card as the example (paymentMethods.type CARD): the customer enters the credit card data in Paygate's form, not in your shop.",
					"Dein Shop zeigt die Zahlarten selbst an. Hat der Kunde eine gewählt, legt dein Server die Zahlung mit genau dieser Zahlart und integrationType HOSTED an. Paygate antwortet mit 202 und dem Link zum Formular. Die Tour nimmt Kreditkarte als Beispiel (paymentMethods.type CARD): Die Kreditkartendaten gibt der Kunde im Formular von Paygate ein, nicht in deinem Shop.",
				},
				Doc:  docForms,
				Kind: Request,
				Arrows: []Arrow{
					{0, 1, T{"POST /payments (HOSTED)", "POST /payments (HOSTED)"}},
					{1, 0, T{"202 · _links.redirect", "202 · _links.redirect"}},
				},
				Plan: func(r *Run) (*Planned, error) {
					notes := baseNotes()
					notes["urls.return"], notes["urls.cancel"], notes["urls.webhook"] = noteReturn, noteCancel, noteWebhook
					notes["paymentMethods.type"] = T{"CARD · credit card, picked by the customer in your shop", "CARD · Kreditkarte, vom Kunden im Shop gewählt"}
					notes["paymentMethods.integrationType"] = T{"HOSTED · the form comes from Paygate", "HOSTED · das Formular kommt von Paygate"}
					return payment(r, core.PaymentInput{Flow: core.FlowMethod, Method: "card", Amount: "49.90"}, notes)
				},
				After: func(r *Run, res *paygate.Result, o *Outcome) {
					r.TransID, r.Amount = requestOf(r.planned[r.Step])
					rememberPayment(r, res)
					r.Redirect = res.Payment.RedirectURL()
					if r.Redirect != "" {
						o.Status = ui.Success.Render("✓ ") + T{"Link to the form received.", "Link zum Formular erhalten."}.String()
					}
				},
			},
			{
				Short: T{"Redirect", "Weiterleitung"},
				Title: T{"Redirect the customer to the form", "Kunden zum Formular weiterleiten"},
				Explain: T{
					"Your shop redirects the browser to the link from step 1. For credit cards and SEPA direct debit, Paygate shows the form itself. With PayPal and other methods, the customer goes straight to the provider. There is no selection page in between.",
					"Dein Shop leitet den Browser auf den Link aus Schritt 1 weiter. Bei Kreditkarte und SEPA-Lastschrift zeigt Paygate das Formular selbst. Bei PayPal und anderen Zahlarten landet der Kunde direkt beim Anbieter. Eine Auswahlseite gibt es dazwischen nicht.",
				},
				Doc:    docForms,
				Kind:   Browser,
				Next:   true,
				Arrows: []Arrow{{0, 2, T{"redirect to the form", "Redirect zum Formular"}}},
				Do:     openStep,
			},
			{
				Short: T{"Pay", "Bezahlen"},
				Title: T{"The customer pays", "Der Kunde bezahlt"},
				Explain: T{
					"The customer enters their credit card data in the form and pays, or cancels. Your server doesn't know about it yet.",
					"Der Kunde gibt im Formular seine Kreditkartendaten ein und bezahlt oder bricht ab. Dein Server bekommt davon zunächst nichts mit.",
				},
				Doc:    docForms,
				Kind:   Wait,
				Arrows: []Arrow{{2, 1, T{"customer pays", "Kunde zahlt"}}},
				Do:     waitCustomer,
			},
			{
				Short: T{"Return", "Rückkehr"},
				Title: T{"Back to the shop with the payId", "Rückkehr in den Shop mit der payId"},
				Explain: T{
					"After paying, Paygate sends the customer to urls.return, with the payId as a query parameter. Depending on how your merchant account is set up, Paygate also sends a webhook.",
					"Nach der Zahlung schickt Paygate den Kunden auf urls.return, mit der payId als Query-Parameter. Je nach Einstellung deines Händlerkontos schickt Paygate zusätzlich einen Webhook.",
				},
				Doc:    docForms,
				Kind:   Browser,
				Auto:   true,
				Arrows: []Arrow{{2, 0, T{"GET urls.return?payId", "GET urls.return?payId"}}},
				Do:     returnWithPayID,
			},
			{
				Short: T{"Details", "Details"},
				Title: T{"Look up the payment by payId", "Zahlungsdetails per payId abfragen"},
				Explain: T{
					"With the payId from the return, your server looks up the payment details. The responseCode decides whether your shop shows a success or a failure page: 00000000 means successful.",
					"Mit der payId aus dem Rücksprung fragt dein Server die Zahlungsdetails ab. Der responseCode entscheidet, ob dein Shop eine Erfolgs- oder eine Fehlerseite zeigt: 00000000 bedeutet erfolgreich.",
				},
				Doc:  docStatusByPayID,
				Kind: Request,
				Arrows: []Arrow{
					{0, 1, T{"GET getByPayId/{id}", "GET getByPayId/{id}"}},
					{1, 0, T{"200 · responseCode", "200 · responseCode"}},
				},
				Plan: func(r *Run) (*Planned, error) { return lookupPayID(r, r.PayID) },
				After: func(r *Run, res *paygate.Result, o *Outcome) {
					rememberPayment(r, res)
					o.Status = statusLine(res.Payment)
				},
			},
		},
	}
}

// returnWithPayID tells where the customer went after the form and which
// payId the return carries.
func returnWithPayID(ctx context.Context, r *Run) *Outcome {
	o := returnStep(ctx, r)
	cancelled := r.Payment != nil && r.Demo() && string(r.Payment.ResponseCode) == mockpaygate.CancelCode
	if r.PayID != "" && r.Payment != nil && !cancelled {
		// After the two addresses: the payId the return URL carries.
		line := "   " + ui.Label.Render("payId  ") + ui.Text.Render(r.PayID)
		o.Lines = append(o.Lines[:1], append([]string{line}, o.Lines[1:]...)...)
	}
	return o
}
