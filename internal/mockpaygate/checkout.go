package mockpaygate

import (
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/krebs3r/paygate-cli/internal/money"
)

// Checkout modes of hosted payments.
const (
	CheckoutAuto = ""
	CheckoutAsk  = "ask"
)

// defaultChoices are the methods the checkout page of a session offers when
// the session does not limit them.
var defaultChoices = []string{"CARD", "PAYPAL", "IDEAL", "DIRECTDEBIT"}

// checkoutChoices are the methods a hosted payment can be paid with: the
// one method of a hosted single method, the allowed ones of a session, or
// a few common ones.
func checkoutChoices(b *paymentBody, method string) []string {
	if b.PaymentMethods != nil && b.PaymentMethods.Type != "" {
		return []string{method}
	}
	var out []string
	for _, m := range b.AllowedPaymentMethods {
		if m = strings.ToUpper(strings.TrimSpace(m)); m != "" {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		out = defaultChoices
	}
	return out
}

// CancelCode is the responseCode of a payment the customer cancelled on the
// demo checkout. Made up for the demo, like the other codes below.
const CancelCode = "22000001"

// Outcomes of the checkout page and what a payment looks like afterwards.
// The codes are made up for the demo; they only follow Paygate's classes
// (0 success, 2 declined).
var checkoutOutcomes = map[string]struct{ status, code, desc string }{
	"pay":     {"OK", "00000000", "success"},
	"decline": {"FAILED", "21000305", "declined on the demo checkout"},
	"cancel":  {"FAILED", CancelCode, "cancelled by the customer"},
}

// localURL reports whether a URL points to this computer.
func localURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// askPage shows the demo checkout: the payment, the methods and the three
// ways it can end. Opening it starts the customer's attempt.
func (s *Server) askPage(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("transId")
	s.mu.Lock()
	rec, ok := s.byTrans[id]
	if !ok || !rec.Hosted {
		s.mu.Unlock()
		s.page(w, http.StatusNotFound, s.text("notfound.title"), `<p>`+esc(s.text("notfound.text"))+`</p>`)
		return
	}
	if !rec.Opened {
		rec.Opened = true
		if rec.Link {
			// The customer's attempt is a payment of its own.
			rec.PayID = randHex(16)
			s.byPayID[rec.PayID] = rec
		}
		rec.Status, rec.Code, rec.Desc = "AUTHORIZE_REQUEST", "60000000", "waiting for customer"
	}
	view := *rec
	s.mu.Unlock()
	if view.Status != "AUTHORIZE_REQUEST" {
		s.resultPage(w, &view, "")
		return
	}

	var methods strings.Builder
	for i, m := range view.Choices {
		checked := ""
		if i == 0 {
			checked = " checked"
		}
		fmt.Fprintf(&methods, `<label class="m"><input type="radio" name="method" value="%s"%s><span>%s</span></label>`,
			esc(m), checked, esc(s.methodLabel(m)))
	}
	body := fmt.Sprintf(`<form method="post" action="/checkout" class="card">
<input type="hidden" name="transId" value="%s">
<div class="shop"><b>%s</b><span>%s %s</span></div>
<div class="amount">%s<small>%s</small></div>
<div class="methods">%s</div>
<div class="buttons">
<button name="action" value="pay" class="primary">%s</button>
<button name="action" value="decline">%s</button>
<button name="action" value="cancel">%s</button>
</div>
<p class="note">%s</p>
</form>
<aside>
<h2>%s</h2>
<div class="out"><b>%s</b><span>%s</span></div>
<div class="out"><b>%s</b><span>%s</span></div>
<div class="out"><b>%s</b><span>%s</span></div>
</aside>`,
		esc(view.TransID),
		esc(s.MerchantID), esc(s.text("order")), esc(view.TransID),
		esc(s.amount(view.Amount, view.Currency)), esc(view.Currency),
		methods.String(),
		esc(s.text("btn.pay")), esc(s.text("btn.decline")), esc(s.text("btn.cancel")),
		esc(s.text("note")),
		esc(s.text("after")),
		esc(s.text("btn.pay")), s.afterText("pay", &view),
		esc(s.text("btn.decline")), s.afterText("decline", &view),
		esc(s.text("btn.cancel")), s.afterText("cancel", &view),
	)
	s.page(w, http.StatusOK, s.text("title"), `<main class="two">`+body+`</main>`)
}

// afterText says what happens after a button, with the payment's real URLs.
func (s *Server) afterText(action string, rec *record) string {
	target, field := rec.ReturnURL, "urls.return"
	if action == "cancel" {
		target, field = rec.CancelURL, "urls.cancel"
	}
	o := checkoutOutcomes[action]
	return fmt.Sprintf(esc(s.text("after.line")), code(field), code(o.status), code(o.code)) +
		` <span class="url">` + esc(target) + `</span>`
}

// checkoutAnswer ends the customer's attempt with the button they pressed.
func (s *Server) checkoutAnswer(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	action := r.Form.Get("action")
	o, known := checkoutOutcomes[action]
	s.mu.Lock()
	rec, ok := s.byTrans[r.Form.Get("transId")]
	if !ok || !rec.Hosted || !known {
		s.mu.Unlock()
		s.page(w, http.StatusNotFound, s.text("notfound.title"), `<p>`+esc(s.text("notfound.text"))+`</p>`)
		return
	}
	if rec.Status != "AUTHORIZE_REQUEST" {
		// Answered already, e.g. a second click or the back button.
		view := *rec
		s.mu.Unlock()
		s.resultPage(w, &view, "")
		return
	}
	if action == "pay" && rec.Sim != "" && rec.Sim != "0000" {
		action, o = "decline", checkoutOutcomes["decline"]
	}
	if m := strings.ToUpper(r.Form.Get("method")); m != "" && contains(rec.Choices, m) {
		rec.Method = m
	}
	rec.Status, rec.Code, rec.Desc = o.status, o.code, o.desc
	if action == "pay" {
		if rec.Method == "CARD" && rec.Brand == "" {
			rec.Brand, rec.Last4 = "VISA", "1111"
		}
		if rec.Capture != "MANUAL" {
			rec.Captured = rec.Amount
		}
	}
	view := *rec
	s.mu.Unlock()
	go s.sendWebhook(view)

	target := view.ReturnURL
	if action == "cancel" {
		target = view.CancelURL
	}
	if localURL(target) {
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	s.resultPage(w, &view, action)
}

// resultPage tells how the payment ended and where Paygate would send the
// browser and the webhook. It stands in for the shop's return page, so the
// browser never leaves this computer.
func (s *Server) resultPage(w http.ResponseWriter, rec *record, action string) {
	if action == "" {
		action = "pay"
		switch {
		case rec.Status == "FAILED" && rec.Code == checkoutOutcomes["cancel"].code:
			action = "cancel"
		case rec.Status == "FAILED":
			action = "decline"
		}
	}
	target, field := rec.ReturnURL, "urls.return"
	if action == "cancel" {
		target, field = rec.CancelURL, "urls.cancel"
	}
	hook := esc(s.text("hook.off"))
	switch {
	case s.Webhook != "basic" && s.Webhook != "enhanced":
	case rec.WebhookURL == "":
	case s.LocalWebhooksOnly && !localURL(rec.WebhookURL):
		hook = fmt.Sprintf(esc(s.text("hook.skipped")), code("urls.webhook")) + ` <span class="url">` + esc(rec.WebhookURL) + `</span>`
	default:
		hook = fmt.Sprintf(esc(s.text("hook.sent")), code("urls.webhook")) + ` <span class="url">` + esc(rec.WebhookURL) + `</span>`
	}
	body := fmt.Sprintf(`<div class="card result %s">
<h1>%s</h1>
<p class="status">%s %s · %s</p>
<div class="out"><b>%s</b><span>%s <span class="url">%s</span></span></div>
<div class="out"><b>%s</b><span>%s</span></div>
<p class="note">%s</p>
</div>`,
		esc(action), esc(s.text("result."+action)),
		code(rec.Status), code(rec.Code), esc(rec.Desc),
		esc(s.text("browser")), fmt.Sprintf(esc(s.text("browser.text")), code(field)), esc(target),
		esc(s.text("webhook")), hook,
		esc(s.text("back")))
	s.page(w, http.StatusOK, s.text("result."+action), `<main>`+body+`</main>`)
}

func (s *Server) page(w http.ResponseWriter, status int, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	lang := "en"
	if s.german() {
		lang = "de"
	}
	fmt.Fprintf(w, `<!doctype html><html lang="%s"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title><style>%s</style>
<div class="band"><span><b>%s</b> · %s</span><span>%s</span></div>
%s`, lang, esc(title), checkoutCSS, esc(s.text("band")), esc(s.text("band.not")), esc(s.text("band.local")), body)
}

func (s *Server) methodLabel(m string) string {
	if l, ok := methodLabels[m]; ok {
		if m == "CARD" || m == "DIRECTDEBIT" {
			return s.text("method." + m)
		}
		return l
	}
	return m
}

var methodLabels = map[string]string{
	"CARD": "", "PAYPAL": "PayPal", "IDEAL": "iDEAL", "DIRECTDEBIT": "", "KLARNA": "Klarna",
	"APPLEPAY": "Apple Pay", "GOOGLEPAY": "Google Pay", "BANCONTACT": "Bancontact", "EPS": "EPS",
	"TWINT": "TWINT", "WERO": "Wero", "TRUSTLY": "Trustly", "MBWAY": "MB WAY", "SWISH": "Swish",
	"MOBILEPAY": "MobilePay",
}

func (s *Server) text(key string) string {
	t, ok := checkoutTexts[key]
	if !ok {
		return key
	}
	if s.german() {
		return t[1]
	}
	return t[0]
}

func (s *Server) german() bool { return s.Lang != nil && s.Lang() == "de" }

// amount formats an amount with a decimal comma on the German page.
func (s *Server) amount(minor int64, currency string) string {
	v := money.FormatValue(minor, currency)
	if s.german() {
		return strings.ReplaceAll(v, ".", ",")
	}
	return v
}

var checkoutTexts = map[string][2]string{
	"title":              {"Demo checkout", "Demo-Kasse"},
	"band":               {"DEMO CHECKOUT", "DEMO-KASSE"},
	"band.not":           {"not a real payment page", "keine echte Bezahlseite"},
	"band.local":         {"paygate demo · nothing leaves this computer", "paygate demo · nichts verlässt diesen Rechner"},
	"order":              {"Order", "Bestellung"},
	"btn.pay":            {"Pay", "Zahlung erfolgreich"},
	"btn.decline":        {"Decline", "Ablehnen"},
	"btn.cancel":         {"Cancel", "Abbrechen"},
	"note":               {"At this point Paygate shows the merchant's payment page. The demo checkout only plays what happens next.", "Im echten Ablauf zeigt Paygate hier die Bezahlseite des Händlers. Die Demo-Kasse spielt nur nach, was danach passiert."},
	"after":              {"What happens next", "Was danach passiert"},
	"after.line":         {"Browser to %s, status %s with %s.", "Browser zu %s, Status %s mit %s."},
	"method.CARD":        {"Card", "Karte"},
	"method.DIRECTDEBIT": {"SEPA Direct Debit", "SEPA-Lastschrift"},
	"result.pay":         {"Payment successful", "Zahlung erfolgreich"},
	"result.decline":     {"Payment declined", "Zahlung abgelehnt"},
	"result.cancel":      {"Payment cancelled", "Zahlung abgebrochen"},
	"browser":            {"Browser", "Browser"},
	"browser.text":       {"Paygate now sends the customer to %s. The demo checkout does not forward there, so the browser calls nothing on the internet during the demo:", "Paygate schickt den Kunden jetzt auf %s. Die Demo-Kasse leitet nicht dorthin weiter, damit der Browser in der Demo nichts im Internet aufruft:"},
	"webhook":            {"Webhook", "Webhook"},
	"hook.sent":          {"Sent to %s:", "Gesendet an %s:"},
	"hook.skipped":       {"Not sent: %s is not an address on this computer, and the demo only sends webhooks there:", "Nicht gesendet: %s ist keine Adresse auf diesem Rechner, und die Demo sendet Webhooks nur dorthin:"},
	"hook.off":           {"None; webhooks are off.", "Keiner; Webhooks sind aus."},
	"back":               {"Go back to the terminal: paygate shows the result there.", "Zurück zum Terminal: paygate zeigt dort das Ergebnis."},
	"notfound.title":     {"Unknown payment", "Unbekannte Zahlung"},
	"notfound.text":      {"The demo Paygate does not know this payment. It may belong to another paygate process.", "Der Demo-Paygate kennt diese Zahlung nicht. Vielleicht gehört sie zu einem anderen paygate-Prozess."},
}

func esc(s string) string { return html.EscapeString(s) }

func code(s string) string { return "<code>" + esc(s) + "</code>" }

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// checkoutCSS is neutral on purpose: the demo checkout must not look like
// Paygate's payment page. Everything is inline; the page loads nothing.
const checkoutCSS = `
:root{--bg:#f7f7f5;--card:#fff;--fg:#1c1f24;--muted:#6a707a;--line:#e3e4e6;--band:#2d2142;--bandfg:#f1eafe;--accent:#7c3aed;--ok:#15803d;--bad:#b91c1c;color-scheme:light}
@media (prefers-color-scheme:dark){:root{--bg:#121316;--card:#1b1d22;--fg:#e8e9ec;--muted:#9a9fa8;--line:#2c2f36;--band:#2d2142;--bandfg:#f1eafe;--accent:#a78bfa;--ok:#4ade80;--bad:#f87171;color-scheme:dark}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:15px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif}
.band{background:var(--band);color:var(--bandfg);font-size:13px;padding:9px 20px;display:flex;justify-content:space-between;gap:12px;flex-wrap:wrap}
.band b{letter-spacing:.08em}
main{max-width:960px;margin:0 auto;padding:32px 20px}
main.two{display:grid;grid-template-columns:minmax(0,1.15fr) minmax(0,1fr);gap:28px}
@media (max-width:760px){main.two{grid-template-columns:1fr}}
.card{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:22px 24px;display:grid;gap:16px;align-content:start}
.shop{display:flex;justify-content:space-between;gap:12px;flex-wrap:wrap;color:var(--muted);font-size:13px}.shop b{color:var(--fg);font-size:15px}
.amount{font-size:34px;font-weight:600;letter-spacing:-.02em;font-variant-numeric:tabular-nums}.amount small{font-size:16px;color:var(--muted);font-weight:500;margin-left:6px}
.methods{display:flex;flex-wrap:wrap;gap:8px}
.m{cursor:pointer}.m input{position:absolute;opacity:0}.m span{display:block;border:1px solid var(--line);border-radius:7px;padding:8px 14px;font-size:13px;color:var(--muted)}
.m input:checked+span{border-color:var(--fg);color:var(--fg);font-weight:600;box-shadow:inset 0 0 0 1px var(--fg)}
.m input:focus-visible+span{outline:2px solid var(--accent);outline-offset:2px}
.buttons{display:grid;grid-template-columns:1.4fr 1fr 1fr;gap:8px}
button{font:inherit;font-size:14px;font-weight:600;border-radius:7px;padding:10px 8px;border:1px solid var(--line);background:var(--card);color:var(--fg);cursor:pointer}
button.primary{background:var(--fg);color:var(--card);border-color:var(--fg)}
button:focus-visible{outline:2px solid var(--accent);outline-offset:2px}
.note{font-size:12.5px;color:var(--muted);margin:0}
aside{display:grid;gap:12px;align-content:start;font-size:14px}aside h2{margin:0;font-size:16px}
.out{border-top:1px solid var(--line);padding-top:10px;display:grid;gap:3px}.out span{color:var(--muted);font-size:13px}
code{font:12px ui-monospace,Menlo,Consolas,monospace;background:var(--line);border-radius:3px;padding:0 4px;color:var(--fg)}
.url{font:12px ui-monospace,Menlo,Consolas,monospace;word-break:break-all;color:var(--fg)}
.result{max-width:640px;margin:0 auto}.result h1{margin:0;font-size:24px}
.result.pay h1{color:var(--ok)}.result.decline h1,.result.cancel h1{color:var(--bad)}
.status{margin:0;color:var(--muted)}
`
