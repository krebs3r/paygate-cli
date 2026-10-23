// Package mockpaygate is an in-memory imitation of the Paygate REST API v2.
// It backs the automated tests and lets you record demos without real
// merchant credentials. Response shapes follow the OpenAPI examples.
package mockpaygate

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/krebs3r/paygate-cli/internal/webhooks"
)

// Server is a mock Paygate. Create it with New and mount Handler.
type Server struct {
	MerchantID string
	APIKey     string
	// BaseURL is used for hosted redirect links; set it after starting.
	BaseURL string
	// NotFoundPolls is how many status lookups of a hosted payment answer
	// "not found" before the customer "pays"; PendingPolls follow as pending.
	NotFoundPolls int
	PendingPolls  int
	// HostedMethod is the method a hosted payment is "paid" with.
	HostedMethod string
	// Require3DS asks for 3-D Secure on every direct card payment that does
	// not skip it. Otherwise only the 3DS test cards ask for it.
	Require3DS bool
	// Webhook is the webhook a hosted payment sends to its urls.webhook once
	// it is final: "basic", "enhanced" (signed with HMACKey) or "" for none.
	Webhook string
	HMACKey string
	// WebhookTo, if set, receives the webhooks instead of urls.webhook, so
	// they can reach a local http endpoint that Paygate could not call.
	WebhookTo string
	// OnWebhook, if set, learns the outcome of every webhook delivery.
	OnWebhook func(url string, status int, err error)

	mu       sync.Mutex
	byTrans  map[string]*record
	byPayID  map[string]*record
	polls    map[string]int
	requests int
}

type record struct {
	PayID, TransID, Status, Code, Desc, Method, Currency, Capture, Sim string
	Amount, Captured, Refunded                                         int64
	Hosted                                                             bool
	Brand, Last4                                                       string
	WebhookURL                                                         string
	Created                                                            time.Time
}

// New returns a mock that accepts the given credentials.
func New(merchantID, apiKey string) *Server {
	return &Server{
		MerchantID:    merchantID,
		APIKey:        apiKey,
		NotFoundPolls: 1,
		PendingPolls:  1,
		HostedMethod:  "PAYPAL",
		byTrans:       map[string]*record{},
		byPayID:       map[string]*record{},
		polls:         map[string]int{},
	}
}

// Requests returns the number of API requests served.
func (s *Server) Requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

const mockToken = "mock-access-token"

var declineCards = map[string]bool{
	"4012888888881881": true, "5105105105105100": true, "371449635398431": true,
	"4200000000000000": true, "5200000000000007": true, "378282246310005": true,
}

var threeDSCards = map[string]bool{
	"4000019966199434": true, "5232122422543299": true, "4000012892688323": true,
	"5232125125401459": true, "4000011744135012": true, "5232122741507017": true,
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /authorization/oauth/token", s.token)
	mux.HandleFunc("POST /api/v2/payments/sessions", s.auth(s.createSession))
	mux.HandleFunc("POST /api/v2/payments", s.auth(s.createPayment))
	mux.HandleFunc("GET /api/v2/payments/getByTransId/{id}", s.auth(s.getByTransID))
	mux.HandleFunc("GET /api/v2/payments/getByPayId/{id}", s.auth(s.getByPayID))
	mux.HandleFunc("POST /api/v2/payments/{id}/captures", s.auth(s.operation("capture")))
	mux.HandleFunc("POST /api/v2/payments/{id}/refunds", s.auth(s.operation("refund")))
	mux.HandleFunc("POST /api/v2/payments/{id}/reversals", s.auth(s.operation("reversal")))
	mux.HandleFunc("GET /checkout", s.checkoutPage)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Correlation-ID", randHex(8))
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if r.Form.Get("grant_type") != "client_credentials" {
		writeJSON(w, 400, map[string]any{"error": "unsupported_grant_type", "error_description": "grant_type must be client_credentials"})
		return
	}
	if r.Form.Get("client_id") != s.MerchantID || r.Form.Get("client_secret") != s.APIKey {
		writeJSON(w, 400, map[string]any{"error": "invalid_client", "error_description": "The client credentials are invalid."})
		return
	}
	writeJSON(w, 200, map[string]any{"access_token": mockToken, "token_type": "Bearer", "expires_in": 3600})
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests++
		s.mu.Unlock()
		ok := false
		if u, p, has := r.BasicAuth(); has && u == s.MerchantID && p == s.APIKey {
			ok = true
		}
		if r.Header.Get("Authorization") == "Bearer "+mockToken {
			ok = true
		}
		if !ok {
			w.Header().Set("WWW-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

type paymentBody struct {
	TransID string `json:"transId"`
	Amount  *struct {
		Value    int64  `json:"value"`
		Currency string `json:"currency"`
	} `json:"amount"`
	CaptureMethod *struct {
		Type string `json:"type"`
	} `json:"captureMethod"`
	Order *struct {
		Description string `json:"description"`
	} `json:"order"`
	BrowserInfo *struct {
		IPAddress string `json:"ipAddress"`
	} `json:"browserInfo"`
	URLs *struct {
		Return  string `json:"return"`
		Cancel  string `json:"cancel"`
		Webhook string `json:"webhook"`
	} `json:"urls"`
	PaymentMethods *struct {
		Type            string `json:"type"`
		IntegrationType string `json:"integrationType"`
		Card            *struct {
			Number        string `json:"number"`
			Brand         string `json:"brand"`
			ThreeDsPolicy *struct {
				Skip string `json:"skip"`
			} `json:"threeDsPolicy"`
		} `json:"card"`
	} `json:"paymentMethods"`
}

func (s *Server) decode(w http.ResponseWriter, r *http.Request) (*paymentBody, bool) {
	var b paymentBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		problem(w, map[string][]string{"$": {"The request body is not valid JSON."}})
		return nil, false
	}
	errs := map[string][]string{}
	if b.TransID == "" {
		errs["transId"] = []string{"The transId field is required."}
	}
	if b.Amount == nil || b.Amount.Value <= 0 {
		errs["amount.value"] = []string{"The amount value must be greater than 0."}
	} else if len(b.Amount.Currency) != 3 {
		errs["amount.currency"] = []string{"The currency must be a 3-letter ISO code."}
	}
	if len(errs) > 0 {
		problem(w, errs)
		return nil, false
	}
	return &b, true
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	b, ok := s.decode(w, r)
	if !ok {
		return
	}
	if b.URLs == nil || b.URLs.Return == "" || b.URLs.Cancel == "" || b.URLs.Webhook == "" {
		problem(w, map[string][]string{"urls": {"urls.return, urls.cancel and urls.webhook are required."}})
		return
	}
	s.hosted(w, b, s.HostedMethod)
}

func (s *Server) hosted(w http.ResponseWriter, b *paymentBody, method string) {
	rec := s.newRecord(b)
	rec.Hosted, rec.Method, rec.Status, rec.Code = true, method, "", ""
	s.store(rec)
	token := randHex(16)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"_links": map[string]any{"redirect": map[string]any{"href": s.BaseURL + "/checkout?token=" + token + "&transId=" + rec.TransID, "type": "text/html"}},
	})
}

func (s *Server) createPayment(w http.ResponseWriter, r *http.Request) {
	b, ok := s.decode(w, r)
	if !ok {
		return
	}
	if b.PaymentMethods == nil || b.PaymentMethods.Type == "" || b.PaymentMethods.IntegrationType == "" {
		problem(w, map[string][]string{"paymentMethods": {"paymentMethods.type and paymentMethods.integrationType are required."}})
		return
	}
	if strings.EqualFold(b.PaymentMethods.IntegrationType, "HOSTED") {
		if b.URLs == nil || b.URLs.Return == "" {
			problem(w, map[string][]string{"urls.return": {"Mandatory when paymentMethods.integrationType=HOSTED."}})
			return
		}
		s.hosted(w, b, strings.ToUpper(b.PaymentMethods.Type))
		return
	}
	card := b.PaymentMethods.Card
	if card == nil || card.Number == "" {
		problem(w, map[string][]string{"paymentMethods.card.number": {"Mandatory when paymentMethods.integrationType=DIRECT."}})
		return
	}
	// Like Paygate: direct card payments need the customer's IP address.
	if b.BrowserInfo == nil || b.BrowserInfo.IPAddress == "" {
		problemTitle(w, "Validation failed", map[string][]string{"BrowserInfo.IpAddress": {"'Browser Info Ip Address' must not be empty."}})
		return
	}
	rec := s.newRecord(b)
	rec.Method, rec.Brand, rec.Last4 = "CARD", strings.ToUpper(card.Brand), card.Number[len(card.Number)-4:]
	simDecline := rec.Sim != "" && rec.Sim != "0000"
	simOK := rec.Sim == "0000"
	switch {
	case simDecline || (declineCards[card.Number] && !simOK):
		rec.Status, rec.Code, rec.Desc = "FAILED", "422", "Payment could not be processed."
		s.store(rec)
		writeJSON(w, 422, map[string]any{
			"merchantId": s.MerchantID, "payId": rec.PayID, "xId": randHex(16), "transId": rec.TransID,
			"status": "FAILED", "responseCode": "422", "responseDescription": rec.Desc,
			"paymentMethods": map[string]any{"card": map[string]any{
				"brand": rec.Brand, "last4Digits": rec.Last4,
				"providerResponseCode": "05", "providerResponseMessage": "Do not honor",
			}},
		})
		return
	case (threeDSCards[card.Number] || s.Require3DS) && card.ThreeDsPolicy == nil:
		// Like Paygate: 3-D Secure versioning is done and the browser steps
		// (3DS method, challenge) are up to the merchant. No redirect link.
		rec.Status, rec.Code, rec.Desc = "REQUEST", "70000000", "AUTHENTICATION_REQUIRED"
		s.store(rec)
		out := s.paymentJSON(rec)
		pm := out["paymentMethods"].(map[string]any)
		pm["card"].(map[string]any)["versioningData"] = map[string]any{
			"threeDSServerTransId": rec.PayID,
			"threeDSMethodUrl":     s.BaseURL + "/cbThreeDS.aspx?action=mthdNtfn",
		}
		writeJSON(w, http.StatusCreated, out)
		return
	}
	rec.Code, rec.Desc = "00000000", "success"
	rec.Status = "OK"
	if rec.Capture == "MANUAL" {
		rec.Status = "AUTHORIZED"
	} else {
		rec.Captured = rec.Amount
	}
	s.store(rec)
	writeJSON(w, http.StatusCreated, s.paymentJSON(rec))
}

func (s *Server) newRecord(b *paymentBody) *record {
	capture := "AUTOMATIC"
	if b.CaptureMethod != nil && strings.EqualFold(b.CaptureMethod.Type, "MANUAL") {
		capture = "MANUAL"
	}
	rec := &record{
		PayID: randHex(16), TransID: b.TransID, Amount: b.Amount.Value,
		Currency: strings.ToUpper(b.Amount.Currency), Capture: capture, Created: time.Now(),
	}
	if b.URLs != nil {
		rec.WebhookURL = b.URLs.Webhook
	}
	// Like Paygate: the order description Test:<code> simulates the result.
	if b.Order != nil && strings.HasPrefix(b.Order.Description, "Test:") {
		rec.Sim = strings.TrimPrefix(b.Order.Description, "Test:")
	}
	return rec
}

func (s *Server) store(rec *record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byTrans[rec.TransID] = rec
	s.byPayID[rec.PayID] = rec
}

func (s *Server) paymentJSON(rec *record) map[string]any {
	out := map[string]any{
		"merchantId": s.MerchantID, "payId": rec.PayID, "xId": randHex(16), "transId": rec.TransID,
		"status": rec.Status, "responseCode": rec.Code, "responseDescription": rec.Desc,
	}
	pm := map[string]any{"type": rec.Method}
	if rec.Method == "CARD" {
		pm["card"] = map[string]any{"brand": rec.Brand, "last4Digits": rec.Last4, "pseudoCardNumber": "07232192456" + rec.Last4, "providerResponseCode": "201", "providerResponseMessage": "SUCCESS"}
	}
	out["paymentMethods"] = pm
	return out
}

func (s *Server) detailsJSON(rec *record) map[string]any {
	out := s.paymentJSON(rec)
	out["amount"] = map[string]any{"value": rec.Amount, "currency": rec.Currency, "capturedValue": rec.Captured, "refundedValue": rec.Refunded}
	out["captureMethod"] = map[string]any{"type": rec.Capture}
	return out
}

func notFound(w http.ResponseWriter) {
	writeJSON(w, 404, map[string]any{"payId": "00000000000000000000000000000000", "status": "FAILED", "responseCode": "20600061", "responseDescription": "PAYMENT NOT FOUND"})
}

func (s *Server) getByTransID(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	rec, ok := s.byTrans[id]
	var final *record // set when the payment completes with this lookup
	if ok && rec.Hosted && rec.Status != "OK" && rec.Status != "FAILED" {
		defer func() {
			if final != nil {
				go s.sendWebhook(*final)
			}
		}()
		s.polls[id]++
		n := s.polls[id]
		switch {
		case n <= s.NotFoundPolls:
			s.mu.Unlock()
			notFound(w)
			return
		case n <= s.NotFoundPolls+s.PendingPolls:
			rec.Status, rec.Code, rec.Desc = "AUTHORIZE_REQUEST", "60000000", "waiting for customer"
		case rec.Sim != "" && rec.Sim != "0000":
			rec.Status, rec.Code, rec.Desc = "FAILED", "21000"+rec.Sim[1:], "simulated decline"
			snapshot := *rec
			final = &snapshot
		default:
			rec.Status, rec.Code, rec.Desc = "OK", "00000000", "success"
			if rec.Method == "CARD" && rec.Brand == "" {
				rec.Brand, rec.Last4 = "VISA", "1111"
			}
			if rec.Capture != "MANUAL" {
				rec.Captured = rec.Amount
			}
			snapshot := *rec
			final = &snapshot
		}
	}
	s.mu.Unlock()
	if !ok {
		notFound(w)
		return
	}
	writeJSON(w, 200, s.detailsJSON(rec))
}

func (s *Server) getByPayID(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	rec, ok := s.byPayID[r.PathValue("id")]
	s.mu.Unlock()
	if !ok {
		notFound(w)
		return
	}
	writeJSON(w, 200, s.detailsJSON(rec))
}

func (s *Server) operation(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			TransID string `json:"transId"`
			Amount  *struct {
				Value    int64  `json:"value"`
				Currency string `json:"currency"`
			} `json:"amount"`
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil || b.TransID == "" || b.Amount == nil {
			problem(w, map[string][]string{"transId": {"transId and amount are required."}})
			return
		}
		s.mu.Lock()
		rec, ok := s.byPayID[r.PathValue("id")]
		if !ok {
			s.mu.Unlock()
			notFound(w)
			return
		}
		fail := ""
		switch kind {
		case "capture":
			if rec.Captured+b.Amount.Value > rec.Amount {
				fail = "Capture amount exceeds the authorized amount."
			} else {
				rec.Captured += b.Amount.Value
				if rec.Captured == rec.Amount {
					rec.Status = "OK"
				}
			}
		case "refund":
			if rec.Refunded+b.Amount.Value > rec.Captured {
				fail = "Refund amount exceeds the captured amount."
			} else {
				rec.Refunded += b.Amount.Value
			}
		case "reversal":
			if rec.Captured > 0 {
				fail = "Payment is already captured; refund it instead."
			} else {
				rec.Status = "FAILED"
				rec.Desc = "cancelled"
			}
		}
		s.mu.Unlock()
		if fail != "" {
			writeJSON(w, 422, map[string]any{"merchantId": s.MerchantID, "payId": rec.PayID, "xId": randHex(16), "transId": rec.TransID,
				"status": "FAILED", "responseCode": "422", "responseDescription": fail})
			return
		}
		writeJSON(w, 200, map[string]any{"merchantId": s.MerchantID, "payId": rec.PayID, "xId": randHex(16), "transId": rec.TransID,
			"status": "OK", "responseCode": "00000000", "responseDescription": "success"})
	}
}

// sendWebhook posts the configured webhook for a completed hosted payment.
// Unlike Paygate it accepts any URL, http and localhost included.
func (s *Server) sendWebhook(rec record) {
	if s.WebhookTo != "" {
		rec.WebhookURL = s.WebhookTo
	}
	if rec.WebhookURL == "" || (s.Webhook != "basic" && s.Webhook != "enhanced") {
		return
	}
	msg := webhooks.NewBasic(rec.PayID)
	if s.Webhook == "enhanced" {
		pl := webhooks.Payload{
			PayID: rec.PayID, TransID: rec.TransID, Status: rec.Status, ResponseCode: rec.Code, ResponseDescription: rec.Desc,
			Amount:         webhooks.Amount{Value: rec.Amount, Currency: rec.Currency},
			PaymentMethods: []webhooks.Method{{Type: rec.Method}},
			CreationDate:   rec.Created.UTC().Format(time.RFC3339),
		}
		var err error
		if msg, err = webhooks.NewEnhanced(s.HMACKey, pl, time.Now(), webhooks.TamperNone); err != nil {
			s.webhookDone(rec.WebhookURL, 0, err)
			return
		}
	}
	req, err := http.NewRequest(http.MethodPost, rec.WebhookURL, bytes.NewReader(msg.Body))
	if err != nil {
		s.webhookDone(rec.WebhookURL, 0, err)
		return
	}
	for _, h := range msg.Headers() {
		req.Header.Set(h[0], h[1])
	}
	resp, err := webhookClient.Do(req)
	if err != nil {
		s.webhookDone(rec.WebhookURL, 0, err)
		return
	}
	resp.Body.Close()
	s.webhookDone(rec.WebhookURL, resp.StatusCode, nil)
}

var webhookClient = &http.Client{Timeout: 10 * time.Second}

func (s *Server) webhookDone(url string, status int, err error) {
	if s.OnWebhook != nil {
		s.OnWebhook(url, status, err)
	}
}

func (s *Server) checkoutPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Mock checkout</title>
<body style="font-family:system-ui;background:#080b10;color:#dce8f5;display:grid;place-items:center;height:100vh;margin:0">
<div style="text-align:center"><h1 style="font-weight:500">Mock Paygate checkout</h1>
<p style="color:#8fa3bf">Transaction %s completes automatically. Return to your terminal.</p></div></body>`, htmlEscape(r.URL.Query().Get("transId")))
}

func problem(w http.ResponseWriter, errs map[string][]string) {
	problemTitle(w, "One or more validation errors occurred.", errs)
}

func problemTitle(w http.ResponseWriter, title string, errs map[string][]string) {
	writeJSON(w, 400, map[string]any{
		"type":    "https://tools.ietf.org/html/rfc9110#section-15.5.1",
		"title":   title,
		"status":  400,
		"traceId": "00-" + randHex(16) + "-" + randHex(8) + "-00",
		"errors":  errs,
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
