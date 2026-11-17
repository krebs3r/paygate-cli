package tour

import (
	"context"
	"encoding/json"

	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

// directNotes are the field notes of a direct card payment.
func directNotes() map[string]T {
	n := baseNotes()
	n["paymentMethods.type"], n["paymentMethods.integrationType"] = noteMethodType, noteIntegrationT
	n["paymentMethods.card.securityCode"], n["paymentMethods.card.expiryDate"] = noteSecurityCode, noteExpiry
	n["browserInfo"], n["browserInfo.ipAddress"] = noteBrowserInfo, noteIP
	n["urls"], n["channel"] = noteDirectURLs, noteChannel
	return n
}

// directPayment plans a direct card payment with a test card.
func directPayment(card, capture, amount string, number T) func(r *Run) (*Planned, error) {
	return func(r *Run) (*Planned, error) {
		notes := directNotes()
		notes["paymentMethods.card.number"] = number
		return payment(r, core.PaymentInput{Flow: core.FlowDirect, Card: card, Capture: capture, Amount: amount}, notes)
	}
}

// versioning reads the 3-D Secure data of a direct payment's answer.
type versioning struct {
	ServerTransID string `json:"threeDSServerTransId"`
	MethodURL     string `json:"threeDSMethodUrl"`
	MethodForm    string `json:"threeDSMethodDataForm"`
	MethodData    struct {
		NotificationURL string `json:"threeDSMethodNotificationUrl"`
	} `json:"threeDSMethodData"`
}

func versioningOf(body []byte) *versioning {
	var resp struct {
		PaymentMethods struct {
			Card struct {
				VersioningData *versioning `json:"versioningData"`
			} `json:"card"`
		} `json:"paymentMethods"`
	}
	if json.Unmarshal(body, &resp) != nil {
		return nil
	}
	return resp.PaymentMethods.Card.VersioningData
}

func directTour() *Tour {
	var threeDS *versioning
	return &Tour{
		ID:    "direct",
		Title: T{"Direct card payment with 3DS", "Direkte Kartenzahlung mit 3DS"},
		Shows: T{"DIRECT, 3-D Secure, a payment without 3DS, a decline", "DIRECT, 3-D Secure, Zahlung ohne 3DS, Ablehnung"},
		Lanes: []T{laneServer, lanePaygate, laneBrowser},
		Steps: []*Step{
			{
				Short: T{"3DS card", "3DS-Karte"},
				Title: T{"Pay with a 3-D Secure card", "Zahlung mit 3DS-Karte"},
				Explain: T{
					"With DIRECT, your server sends the card data to Paygate itself. If the card requires 3-D Secure, the payment isn't complete yet: Paygate responds with responseCode 70000000 and the data the browser needs for the next steps.",
					"Bei DIRECT schickt dein Server die Kartendaten selbst an Paygate. Verlangt die Karte 3-D Secure, ist die Zahlung damit noch nicht abgeschlossen: Paygate antwortet mit responseCode 70000000 und liefert die Daten, die der Browser für die nächsten Schritte braucht.",
				},
				Doc:  docDirect,
				Kind: Request,
				Arrows: []Arrow{
					{0, 1, T{"POST /payments (DIRECT)", "POST /payments (DIRECT)"}},
					{1, 0, T{"201 · 70000000", "201 · 70000000"}},
				},
				Plan: directPayment("visa-3ds-challenge", "auto", "", T{"test card with a 3DS challenge", "Testkarte mit 3DS-Challenge"}),
				After: func(r *Run, res *paygate.Result, o *Outcome) {
					rememberPayment(r, res)
					threeDS = nil
					if o.Exchange != nil {
						threeDS = versioningOf(o.Exchange.Response)
					}
					o.Status = statusLine(res.Payment)
					if paygate.ClassifyCode(res.Payment.ResponseCode) == paygate.Code3DS {
						o.Status += "  " + ui.Muted.Render(T{"→ 3-D Secure required", "→ 3-D Secure erforderlich"}.String())
					}
				},
			},
			{
				Short: T{"3DS method", "3DS-Method"},
				Title: T{"The 3DS method in the browser", "3DS-Method im Browser"},
				Explain: T{
					"The answer from step 1 contains a threeDSMethodUrl and a threeDSMethodDataForm under paymentMethods.card.versioningData. Your shop posts this form to the URL through a hidden iframe in the customer's browser. The result goes to Paygate's threeDSMethodNotificationUrl.",
					"Die Antwort aus Schritt 1 enthält unter paymentMethods.card.versioningData eine threeDSMethodUrl und ein threeDSMethodDataForm. Dein Shop schickt dieses Formular über ein unsichtbares iframe im Browser des Kunden an die URL. Das Ergebnis geht an die threeDSMethodNotificationUrl von Paygate.",
				},
				Doc:    docCreate,
				Kind:   Explain,
				Auto:   true,
				Arrows: []Arrow{{2, 1, T{"3DS method", "3DS-Method"}}},
				Do: func(ctx context.Context, r *Run) *Outcome {
					v := threeDS
					if v == nil || v.MethodURL == "" {
						return &Outcome{Status: T{"The answer from step 1 carries no 3DS method.", "Die Antwort aus Schritt 1 enthält keine 3DS-Method."}.String()}
					}
					lines := []string{ui.Muted.Render(T{"With the values from step 1:", "Mit den Werten aus Schritt 1:"}.String()), ""}
					lines = append(lines, snippet(
						`<iframe name="threeDSMethod" hidden></iframe>`,
						`<form method="post" target="threeDSMethod"`,
						`      action="`+v.MethodURL+`">`,
						`  <input type="hidden" name="threeDSMethodData"`,
						`         value="`+short(v.MethodForm, 40)+`">`,
						`</form>`,
					)...)
					if v.MethodData.NotificationURL != "" {
						lines = append(lines, "", ui.Label.Render("threeDSMethodNotificationUrl  ")+ui.Link.Render(v.MethodData.NotificationURL))
					}
					return &Outcome{Lines: lines, Status: ui.Warning.Render("○ ") + notRun.String()}
				},
			},
			{
				Short: T{"Challenge", "Challenge"},
				Title: T{"The challenge at the card issuer", "Challenge beim Kartenaussteller"},
				Explain: T{
					"If the card issuer wants to verify the customer, the payment details contain acsURL and base64EncodedChallengeRequest under paymentMethods.card.authenticationData. The browser sends the challenge to that address and the customer confirms it; with the test cards, using the code 1234. Paygate receives the result, not your server.",
					"Möchte der Kartenaussteller den Kunden zusätzlich prüfen, stehen in den Zahlungsdetails unter paymentMethods.card.authenticationData die acsURL und der base64EncodedChallengeRequest. Der Browser schickt die Challenge an diese Adresse, und der Kunde bestätigt sie. Bei den Testkarten geht das mit dem Code 1234. Das Ergebnis erhält Paygate, nicht dein Server.",
				},
				Doc:    docStatus,
				Kind:   Explain,
				Auto:   true,
				Arrows: []Arrow{{2, 1, T{"challenge", "Challenge"}}},
				Do: func(ctx context.Context, r *Run) *Outcome {
					return &Outcome{Status: ui.Warning.Render("○ ") + notRun.String()}
				},
			},
			{
				Short: T{"No 3DS", "Ohne 3DS"},
				Title: T{"A payment without the 3DS check", "Zahlung ohne 3DS-Prüfung"},
				Explain: T{
					"With paymentMethods.card.threeDsPolicy.skip you ask Paygate to skip the 3-D Secure check. Without it, according to the API reference, every payment goes through EMV 3DS. The test card here has no 3DS, so the payment completes right away.",
					"Über paymentMethods.card.threeDsPolicy.skip legst du fest, dass die 3-D-Secure-Prüfung ausgelassen werden soll. Ohne diese Angabe läuft laut API-Referenz jede Zahlung über EMV 3DS. Die Testkarte hier hat kein 3DS, deshalb ist die Zahlung sofort abgeschlossen.",
				},
				Doc:  docCreate,
				Kind: Request,
				Arrows: []Arrow{
					{0, 1, T{"POST /payments", "POST /payments"}},
					{1, 0, T{"201 · OK", "201 · OK"}},
				},
				Plan: func(r *Run) (*Planned, error) {
					p, err := directPayment("visa-ok", "auto", "", T{"test card without 3DS", "Testkarte ohne 3DS"})(r)
					if err == nil {
						p.Notes["paymentMethods.card.threeDsPolicy.skip"] = T{"skip the 3DS check", "3DS-Prüfung auslassen"}
					}
					return p, err
				},
				After: func(r *Run, res *paygate.Result, o *Outcome) {
					rememberPayment(r, res)
					o.Status = statusLine(res.Payment)
				},
			},
			{
				Short: T{"Decline", "Ablehnung"},
				Title: T{"A declined card", "Abgelehnte Karte"},
				Explain: T{
					"You recognize a decline by status FAILED and the responseCode, not by the HTTP status alone. Store the responseCode: it tells you and support why the payment was declined.",
					"Eine Ablehnung erkennst du an status FAILED und am responseCode, nicht allein am HTTP-Status. Speichere den responseCode: Er zeigt dir und dem Support, warum die Zahlung abgelehnt wurde.",
				},
				Doc:  docCreate,
				Kind: Request,
				Arrows: []Arrow{
					{0, 1, T{"POST /payments", "POST /payments"}},
					{1, 0, T{"FAILED · responseCode", "FAILED · responseCode"}},
				},
				Plan: directPayment("visa-decline", "auto", "", T{"test card that is declined", "Testkarte, die abgelehnt wird"}),
				After: func(r *Run, res *paygate.Result, o *Outcome) {
					o.Status = statusLine(res.Payment)
				},
			},
			statusStep(T{
				"With DIRECT, too: when in doubt, look up the status by transId, for example if the response never arrived because of a timeout. Here it's the payment from step 4.",
				"Auch bei DIRECT gilt: Im Zweifel fragst du den Status über die transId ab, zum Beispiel wenn die Antwort wegen eines Timeouts nie angekommen ist. Hier ist es die Zahlung aus Schritt 4.",
			}),
		},
	}
}
