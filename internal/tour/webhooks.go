package tour

import (
	"context"
	"strings"

	"github.com/krebs3r/paygate-cli/internal/ui"
	"github.com/krebs3r/paygate-cli/internal/webhooks"
)

func webhooksTour() *Tour {
	session := sessionStep(T{"POST /payments/sessions", "POST /payments/sessions"})
	session.Title = T{"Create a payment with a webhook URL", "Zahlung mit Webhook-URL anlegen"}
	session.Explain = T{
		"In urls.webhook you set, per payment, where Paygate sends the webhook. Paygate can only reach public HTTPS addresses. In the demo, paygate receives the webhook directly on this computer.",
		"In urls.webhook legst du für jede Zahlung fest, wohin Paygate den Webhook schickt. Paygate erreicht nur öffentliche HTTPS-Adressen. In der Demo nimmt paygate den Webhook direkt auf diesem Rechner entgegen.",
	}

	status := statusStep(T{
		"Respond to the webhook with a 2xx once you have stored it, and do any heavier work afterwards. The status lookup gives you the definitive state; with a Basic webhook it's the only way to get the result.",
		"Antworte auf den Webhook mit einem 2xx, sobald du ihn gespeichert hast, und erledige aufwendigere Arbeit erst danach. Den verbindlichen Stand liefert die Statusabfrage. Bei einem Basic-Webhook ist sie der einzige Weg, das Ergebnis zu erfahren.",
	})
	status.Title = T{"Acknowledge the webhook, look up the status", "Webhook bestätigen und Status abfragen"}

	return &Tour{
		ID:            "webhooks",
		Title:         T{"Check webhooks", "Webhooks prüfen"},
		Shows:         T{"Basic and Enhanced, HMAC signature", "Basic und Enhanced, HMAC-Signatur"},
		Lanes:         []T{laneServer, lanePaygate, laneBrowser},
		NeedsWebhooks: true,
		Steps: []*Step{
			session,
			payStep(true),
			{
				Short: T{"Receive", "Empfangen"},
				Title: T{"Receive the webhook", "Webhook empfangen"},
				Explain: T{
					"Paygate sends a POST request with a JSON body. An Enhanced webhook carries status, amount and payment method in the body, plus three headers: X-Paygate-Signature-Version, X-Paygate-Timestamp and X-Paygate-Signature. A Basic webhook carries only {\"payId\": \"…\"} and no signature.",
					"Paygate sendet einen POST-Request mit JSON-Body. Ein Enhanced-Webhook enthält Status, Betrag und Zahlart im Body, dazu drei Header: X-Paygate-Signature-Version, X-Paygate-Timestamp und X-Paygate-Signature. Ein Basic-Webhook enthält nur {\"payId\": \"…\"} und keine Signatur.",
				},
				Doc:    docWebhooks,
				Kind:   Wait,
				Arrows: []Arrow{{1, 0, T{"POST urls.webhook", "POST urls.webhook"}}},
				Do:     waitWebhook,
			},
			{
				Short: T{"Signature", "Signatur"},
				Title: T{"Verify the signature", "Signatur prüfen"},
				Explain: T{
					"Compute the signature yourself: HMAC-SHA256 with your HMAC key over the timestamp, a dot and the body, byte for byte as it arrived. If the hex value matches X-Paygate-Signature, the webhook comes from Paygate and wasn't modified on the way.",
					"Berechne die Signatur selbst: HMAC-SHA256 mit deinem HMAC-Key über den Zeitstempel, einen Punkt und den Body, Byte für Byte so, wie er angekommen ist. Stimmt der Hex-Wert mit X-Paygate-Signature überein, stammt der Webhook von Paygate und wurde unterwegs nicht verändert.",
				},
				Doc:    docSignature,
				Kind:   Local,
				Arrows: []Arrow{{0, 0, T{"HMAC-SHA256 check", "HMAC-SHA256 prüfen"}}},
				Do:     signatureStep,
			},
			status,
		},
	}
}

// signatureStep works the signature check out for the received webhook and
// shows that a changed body no longer matches.
func signatureStep(ctx context.Context, r *Run) *Outcome {
	w := r.Webhook
	if w == nil {
		return &Outcome{Skipped: T{"No webhook arrived, so there is nothing to check.", "Es ist kein Webhook angekommen, es gibt nichts zu prüfen."}.String()}
	}
	key := r.HMACKey()
	c := w.Check(key)
	if !c.Signed {
		return &Outcome{Status: T{"This is a Basic webhook, so there is no signature to verify.", "Das ist ein Basic-Webhook, er hat keine Signatur."}.String()}
	}
	ts := w.Header.Get(webhooks.HeaderTimestamp)
	body := string(w.Body)
	lines := []string{
		ui.Label.Render(T{"signed text   ", "signierter Text  "}.String()) + ui.Text.Render(short(ts+"."+body, 60)),
		ui.Label.Render(T{"HMAC key      ", "HMAC-Key         "}.String()) + ui.Muted.Render(T{"from the profile (", "aus dem Profil ("}.String()+short(key, 4)+")"),
		ui.Label.Render(T{"computed      ", "berechnet        "}.String()) + ui.Text.Render(lower(c.Expected)),
		ui.Label.Render(T{"received      ", "empfangen        "}.String()) + ui.Text.Render(c.Received),
		"",
	}
	for _, f := range c.Findings {
		if f.Name == "" || f.Name == "Body" {
			continue
		}
		lines = append(lines, findingLine(f))
	}
	// A body changed after signing must fail.
	changed := strings.Replace(body, "}", " }", 1)
	tampered := (&Webhook{Header: w.Header, Body: []byte(changed), At: w.At}).Check(key)
	mark := ui.Success.Render("✓ ")
	if tampered.Valid {
		mark = ui.Danger.Render("✗ ")
	}
	lines = append(lines, "", mark+T{"Cross-check: with one extra space in the body, the signature no longer matches.", "Gegenprobe: Mit einem zusätzlichen Leerzeichen im Body passt die Signatur nicht mehr."}.String())
	status := ui.Success.Render("✓ ") + T{"signature matches, timestamp is current", "Signatur stimmt, Zeitstempel ist aktuell"}.String()
	if !c.Valid || c.Failed() {
		status = ui.Danger.Render("✗ ") + T{"verification failed", "Prüfung fehlgeschlagen"}.String()
	}
	return &Outcome{Lines: lines, Status: status}
}

func findingLine(f webhooks.Finding) string {
	mark := ui.Success.Render("✓ ")
	switch f.Level {
	case webhooks.Fail:
		mark = ui.Danger.Render("✗ ")
	case webhooks.Warn:
		mark = ui.Warning.Render("! ")
	case webhooks.Info:
		mark = ui.Muted.Render("· ")
	}
	return mark + ui.Label.Render(f.Name) + ui.Muted.Render("  "+f.Detail)
}
