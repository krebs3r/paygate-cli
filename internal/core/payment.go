package core

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/krebs3r/paygate-cli/internal/cards"
	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/ids"
	"github.com/krebs3r/paygate-cli/internal/money"
	"github.com/krebs3r/paygate-cli/internal/paygate"
)

// Flow is how a payment is collected.
type Flow string

const (
	FlowHosted Flow = "hosted" // hosted payment page with several methods
	FlowMethod Flow = "method" // hosted form for one method
	FlowDirect Flow = "direct" // server-to-server card payment
	FlowLink   Flow = "link"   // payment link to share, opens the hosted payment page
)

const (
	DefaultAmount      = "12.99"
	ProdDefaultAmount  = "0.01"
	DefaultDescription = "Paygate CLI demo order"
)

// PaymentInput is everything needed to build a payment request.
type PaymentInput struct {
	Flow        Flow
	Methods     []string // FlowHosted: offered methods (empty = all)
	Method      string   // FlowMethod
	Card        string   // FlowDirect: test card alias
	CardNumber  string   // FlowDirect: own card
	Expiry      string
	CVC         string
	Holder      string
	Amount      string
	Currency    string
	Description string
	TransID     string
	RefNr       string
	Capture     string // auto | manual
	Language    string
	Channel     string // ECOM | MOTO
	Skip3DS     bool
	IP          string // browserInfo.ipAddress; default: profile setting or this computer
	// Simulate turns the payment into a test transaction, also on
	// production, by sending the order description "Test:<code>".
	Simulate string
	// WebhookURL replaces the profile's webhook URL, e.g. with the address
	// of a running paygate listen.
	WebhookURL string
	// FlowLink: how the customer gets the link (link, email, sms), who the
	// customer is, the e-mail or SMS template at Paygate and how long the
	// link stays valid ("24h", "7d"; empty: Paygate's setting).
	Send     string
	Email    string
	Phone    string
	Name     string
	Template string
	Expires  string
	Partial  bool
}

var simCode = regexp.MustCompile(`^\d{4}$`)

// Simulation codes documented by Paygate.
const (
	SimSuccess = "Test:0000"
	SimDecline = "Test:0305"
)

// SimulationDescription maps "ok", "decline" or a four-digit code to the
// order description that makes Paygate simulate the result.
func SimulationDescription(s string) (string, error) {
	switch v := strings.ToLower(strings.TrimSpace(s)); {
	case v == "" || v == "off" || v == "none":
		return "", nil
	case v == "ok" || v == "success" || v == "0000":
		return SimSuccess, nil
	case v == "decline" || v == "declined" || v == "0305":
		return SimDecline, nil
	case simCode.MatchString(v):
		return "Test:" + v, nil
	case strings.HasPrefix(v, "test:") && simCode.MatchString(v[5:]):
		return "Test:" + v[5:], nil
	}
	return "", fmt.Errorf("unknown simulation %q (use ok, decline or a four-digit code like 0305)", s)
}

// BuildPaymentRequest turns the input into a Paygate request. It also returns
// a short label of the payment method ("VISA •••• 1111", "PayPal").
func BuildPaymentRequest(in PaymentInput, prof *config.Profile) (*paygate.PaymentRequest, string, error) {
	currency := strings.ToUpper(in.Currency)
	if currency == "" {
		currency = prof.DefaultCurrency()
	}
	amount := in.Amount
	if amount == "" {
		amount = DefaultAmount
	}
	value, err := money.Parse(amount, currency)
	if err != nil {
		return nil, "", err
	}
	language := in.Language
	if language == "" {
		language = prof.DefaultLanguage()
	}
	transID := in.TransID
	if transID == "" {
		transID = ids.NewTransID()
	}
	captureType := "AUTOMATIC"
	switch strings.ToLower(in.Capture) {
	case "auto", "automatic", "":
	case "manual":
		captureType = "MANUAL"
	default:
		return nil, "", fmt.Errorf("unknown capture method %q (use auto or manual)", in.Capture)
	}
	description := in.Description
	if description == "" {
		description = DefaultDescription
	}
	sim, err := SimulationDescription(in.Simulate)
	if err != nil {
		return nil, "", err
	}
	if sim != "" {
		description = sim
	}

	req := &paygate.PaymentRequest{
		TransID:       transID,
		RefNr:         in.RefNr,
		Amount:        paygate.Amount{Value: value, Currency: currency},
		Language:      language,
		CaptureMethod: &paygate.CaptureMethod{Type: captureType},
		Order:         &paygate.Order{Description: description, MerchantReference: transID},
	}
	urls := &paygate.URLs{Return: prof.Return(), Cancel: prof.Cancel(), Webhook: prof.Webhook()}
	if in.WebhookURL != "" {
		urls.Webhook = in.WebhookURL
	}

	switch in.Flow {
	case FlowHosted, FlowLink:
		req.URLs = urls
		for _, m := range in.Methods {
			for _, part := range strings.Split(m, ",") {
				if strings.TrimSpace(part) == "" {
					continue
				}
				pm, err := ResolveMethod(part)
				if err != nil {
					return nil, "", err
				}
				req.AllowedPaymentMethods = append(req.AllowedPaymentMethods, pm.Type)
			}
		}
		if in.Flow == FlowLink {
			if err := applyLink(req, in, time.Now()); err != nil {
				return nil, "", err
			}
		}
		return req, "", nil

	case FlowMethod:
		pm, err := ResolveMethod(in.Method)
		if err != nil {
			return nil, "", err
		}
		req.URLs = urls
		req.PaymentMethods = &paygate.PaymentMethods{Type: pm.Type, IntegrationType: "HOSTED"}
		if pm.Type == "IDEAL" {
			req.PaymentMethods.Ideal = &paygate.SubTyped{SubType: "IDEAL"}
		}
		return req, pm.Name, nil

	case FlowDirect:
		number, brand, threeDS := in.CardNumber, "", false
		if in.Card != "" {
			c, ok := cards.FindCard(in.Card)
			if !ok {
				return nil, "", fmt.Errorf("unknown test card %q (see `paygate testcards`)", in.Card)
			}
			number, brand, threeDS = c.Number, c.Brand, c.ThreeDS
		}
		number = strings.ReplaceAll(number, " ", "")
		if number == "" {
			return nil, "", errors.New("no card selected")
		}
		if brand == "" {
			brand = cards.DetectBrand(number)
		}
		expiry, cvc, holder := in.Expiry, in.CVC, in.Holder
		if expiry == "" {
			expiry = cards.DefaultExpiry()
		}
		if cvc == "" {
			cvc = "123"
		}
		if holder == "" {
			holder = "Paygate Tester"
		}
		card := &paygate.Card{Number: number, CardHolderName: holder, ExpiryDate: expiry, SecurityCode: cvc, Brand: brand}
		// Test cards without 3-D Secure are sent without it: a terminal
		// cannot run its browser steps.
		if in.Skip3DS || (in.Card != "" && !threeDS) {
			card.ThreeDsPolicy = &paygate.ThreeDsPolicy{Skip: "THIS_TRANSACTION"}
		}
		channel := strings.ToUpper(in.Channel)
		if channel == "" {
			channel = "ECOM"
		}
		req.Channel = channel
		ip := in.IP
		if ip == "" {
			ip = prof.CustomerIP
		}
		if ip == "" {
			ip = LocalIP()
		}
		if net.ParseIP(ip) == nil {
			return nil, "", fmt.Errorf("%q is not a valid IP address", ip)
		}
		req.BrowserInfo = DefaultBrowserInfo(language, ip)
		req.PaymentMethods = &paygate.PaymentMethods{Type: "CARD", IntegrationType: "DIRECT", Card: card}
		if threeDS {
			req.URLs = urls
		}
		return req, strings.TrimSpace(brand + " " + cards.Mask(number)), nil
	}
	return nil, "", errors.New("no payment flow selected")
}

// DefaultBrowserInfo is the browser data sent with direct card payments.
// Paygate requires the customer's IP address; with the CLI the customer is
// the person at this computer.
func DefaultBrowserInfo(lang, ip string) *paygate.BrowserInfo {
	_, offset := time.Now().Zone()
	return &paygate.BrowserInfo{
		AcceptHeaders:     "text/html,application/json",
		IPAddress:         ip,
		JavaEnabled:       false,
		JavaScriptEnabled: true,
		Language:          strings.ToUpper(lang),
		ColorDepth:        24,
		ScreenHeight:      1080,
		ScreenWidth:       1920,
		TimeZoneOffset:    fmt.Sprint(-offset / 60),
		UserAgent:         "Mozilla/5.0 (paygate-cli)",
	}
}

// LocalIP returns the address this computer uses for outgoing traffic.
// Dialing UDP only consults the routing table; no packet is sent.
func LocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:53")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	if a, ok := conn.LocalAddr().(*net.UDPAddr); ok && a.IP != nil {
		return a.IP.String()
	}
	return "127.0.0.1"
}

// Prepare builds the HTTP request for a payment without sending it.
func Prepare(c *paygate.Client, flow Flow, req *paygate.PaymentRequest) (*paygate.Prepared, error) {
	switch flow {
	case FlowHosted:
		return c.CreateSessionRequest(req)
	case FlowLink:
		return c.CreateLinkRequest(req)
	}
	return c.CreatePaymentRequest(req)
}
