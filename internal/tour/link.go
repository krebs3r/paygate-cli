package tour

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

// Field notes of a payment link: only transId and amount are required.
var (
	noteExpiration = T{"optional · until when the link is valid (UTC)", "optional · bis wann der Link gilt (UTC)"}
	noteLinkReturn = T{"optional · where the browser goes after paying", "optional · Rücksprung nach der Zahlung"}
	noteLinkCancel = T{"optional · where the browser goes after cancelling", "optional · Rücksprung nach einem Abbruch"}
	noteLinkHook   = T{"optional · notification to your server", "optional · Benachrichtigung an deinen Server"}
)

func linkTour() *Tour {
	return &Tour{
		ID:    "link",
		Title: T{"Pay by Link", "Pay by Link"},
		Shows: T{"payment link, expiry, e-mail or SMS, webhook, status", "Zahlungslink, Ablaufzeit, E-Mail oder SMS, Webhook, Status"},
		Lanes: []T{laneServer, lanePaygate, laneBrowser},
		Steps: []*Step{
			{
				Short: T{"Link", "Link"},
				Title: T{"Create a payment link", "Zahlungslink anlegen"},
				Explain: T{
					"Your server creates a payment link with the amount and the transId. Paygate answers with the link only (_links.redirect), without a payId or a status yet. With expirationTime you set until when the link is valid.",
					"Dein Server legt einen Zahlungslink mit Betrag und transId an. Paygate antwortet nur mit dem Link (_links.redirect), noch ohne payId und Status. Mit expirationTime legst du fest, bis wann der Link gilt.",
				},
				Doc:  docLink,
				Kind: Request,
				Arrows: []Arrow{
					{0, 1, T{"POST /payments/links", "POST /payments/links"}},
					{1, 0, T{"202 · _links.redirect", "202 · _links.redirect"}},
				},
				Plan: func(r *Run) (*Planned, error) {
					notes := baseNotes()
					notes["urls.return"], notes["urls.cancel"], notes["urls.webhook"] = noteLinkReturn, noteLinkCancel, noteLinkHook
					notes["expirationTime"] = noteExpiration
					return payment(r, core.PaymentInput{Flow: core.FlowLink, Amount: "49.90", Expires: "7d"}, notes)
				},
				After: func(r *Run, res *paygate.Result, o *Outcome) {
					r.flow = core.FlowLink
					r.TransID, r.Amount = requestOf(r.planned[r.Step])
					r.Redirect = res.Payment.RedirectURL()
					if r.Redirect != "" {
						o.Status = ui.Success.Render("✓ ") + T{"Payment link received.", "Zahlungslink erhalten."}.String()
					}
				},
			},
			{
				Short: T{"Share", "Weitergeben"},
				Title: T{"The link reaches the customer", "Der Link geht an den Kunden"},
				Explain: T{
					"You give the link from step 1 to the customer yourself, for example by e-mail, in a chat or on the invoice. Paygate can also send it: with serviceType EMAIL to customerInfo.email, with serviceType SMS to customerInfo.phone. An SMS also needs externalTemplate, the name of your SMS template at Paygate. Here paygate opens the link in the browser, as if you were the customer.",
					"Den Link aus Schritt 1 gibst du dem Kunden selbst, etwa per E-Mail, im Chat oder auf der Rechnung. Paygate kann ihn auch verschicken: mit serviceType EMAIL an customerInfo.email, mit serviceType SMS an customerInfo.phone. Für eine SMS braucht Paygate zusätzlich externalTemplate, den Namen deiner SMS-Vorlage bei Paygate. Hier öffnet paygate den Link im Browser, als wärst du der Kunde.",
				},
				Doc:    docLink,
				Kind:   Browser,
				Next:   true,
				Arrows: []Arrow{{0, 2, T{"link by e-mail, chat, invoice", "Link per E-Mail, Chat, Rechnung"}}},
				Do:     shareLink,
			},
			{
				Short: T{"Pay", "Bezahlen"},
				Title: T{"The customer pays", "Der Kunde bezahlt"},
				Explain: T{
					"The customer opens the link and lands on Paygate's payment page. There they pick a payment method and pay, as on the Hosted Payment Page. Your server learns about it from the webhook and the status lookup.",
					"Der Kunde öffnet den Link und landet auf der Bezahlseite von Paygate. Dort wählt er eine Zahlart und bezahlt, wie bei der Hosted Payment Page. Dein Server erfährt davon über den Webhook und die Statusabfrage.",
				},
				Doc:    docLink,
				Kind:   Wait,
				Arrows: []Arrow{{2, 1, T{"customer pays", "Kunde zahlt"}}},
				Do:     waitCustomer,
			},
			webhookStep(),
			statusStep(T{
				"Your server looks up the status using the transId of the link. Whether the customer paid is shown by status and responseCode: OK with 00000000 means successful.",
				"Dein Server fragt den Status über die transId des Links ab. Ob der Kunde bezahlt hat, zeigen status und responseCode: OK mit 00000000 bedeutet erfolgreich.",
			}),
		},
	}
}

// shareLink opens the payment link like the customer would and shows how
// Paygate sends it by e-mail or SMS instead.
func shareLink(ctx context.Context, r *Run) *Outcome {
	o := openStep(ctx, r)
	if o.Err != nil {
		return o
	}
	o.Lines = append(o.Lines, "", ui.Muted.Render(T{"Paygate sends the link itself with these fields in step 1:", "Paygate verschickt den Link selbst mit diesen Feldern in Schritt 1:"}.String()), "")
	o.Lines = append(o.Lines, linkVariant("EMAIL", map[string]any{
		"serviceType":  "EMAIL",
		"customerInfo": paygate.CustomerInfo{Email: T{"customer@example.com", "kunde@example.com"}.String()},
	})...)
	o.Lines = append(o.Lines, "")
	o.Lines = append(o.Lines, linkVariant("SMS", map[string]any{
		"serviceType":      "SMS",
		"customerInfo":     paygate.CustomerInfo{Phone: &paygate.Phone{CountryCode: "49", Number: "1701234567"}},
		"externalTemplate": T{"your-sms-template", "deine-sms-vorlage"}.String(),
	})...)
	return o
}

// linkVariant shows the extra fields of a link sent by Paygate.
func linkVariant(name string, fields map[string]any) []string {
	b, _ := json.MarshalIndent(fields, "", "  ")
	lines := []string{ui.Label.Render(name)}
	for _, l := range strings.Split(string(b), "\n") {
		lines = append(lines, ui.Text.Render(l))
	}
	return lines
}
