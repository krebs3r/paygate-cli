package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/mockpaygate"
	"github.com/krebs3r/paygate-cli/internal/paygate"
)

func TestBuildPaymentRequestDCC(t *testing.T) {
	test := &config.Profile{Brand: "nexi", Env: "test", MerchantID: "m"}
	prod := &config.Profile{Brand: "nexi", Env: "prod", MerchantID: "m"}
	body := func(in PaymentInput, prof *config.Profile) string {
		t.Helper()
		req, _, err := BuildPaymentRequest(in, prof)
		if err != nil {
			t.Fatalf("%+v: %v", in, err)
		}
		b, _ := json.Marshal(req)
		return string(b)
	}
	direct := PaymentInput{Flow: FlowDirect, Card: "dcc-usd", Amount: "12.99", IP: "127.0.0.1"}

	accept := direct
	accept.DCC, accept.DCCRequestID = DCCAccept, "q1"
	if b := body(accept, test); !strings.Contains(b, `"dcc":{"selected":true,"dccRequestId":"q1"}`) {
		t.Errorf("accept: %s", b)
	}
	decline := direct
	decline.DCC, decline.DCCRequestID = DCCDecline, "q1"
	if b := body(decline, test); !strings.Contains(b, `"dcc":{"selected":false,"dccRequestId":"q1"}`) {
		t.Errorf("decline: %s", b)
	}
	decline.DCCRequestID = "" // no offer: nothing to decline
	if b := body(decline, test); strings.Contains(b, `"dcc"`) {
		t.Errorf("decline without offer: %s", b)
	}
	if b := body(direct, test); strings.Contains(b, "dcc") {
		t.Errorf("without eDCC: %s", b)
	}

	hosted := PaymentInput{Flow: FlowHosted, Amount: "5", DCCEnabled: "on"}
	if b := body(hosted, test); !strings.Contains(b, `"paymentMethods":{"card":{"dccEnabled":true}}`) {
		t.Errorf("hosted on: %s", b)
	}
	method := PaymentInput{Flow: FlowMethod, Method: "card", Amount: "5", DCCEnabled: "off"}
	if b := body(method, test); !strings.Contains(b, `"card":{"dccEnabled":false}`) || !strings.Contains(b, `"type":"CARD"`) {
		t.Errorf("method off: %s", b)
	}
	if b := body(PaymentInput{Flow: FlowHosted, Amount: "5"}, test); strings.Contains(b, "paymentMethods") {
		t.Errorf("hosted without eDCC: %s", b)
	}

	for name, bad := range map[string]PaymentInput{
		"accept without quote": {Flow: FlowDirect, Card: "visa-ok", IP: "127.0.0.1", DCC: DCCAccept},
		"unknown answer":       {Flow: FlowDirect, Card: "visa-ok", IP: "127.0.0.1", DCC: "maybe", DCCRequestID: "q"},
		"switch on direct":     {Flow: FlowDirect, Card: "visa-ok", IP: "127.0.0.1", DCCEnabled: "on"},
		"answer on hosted":     {Flow: FlowHosted, DCC: DCCAccept, DCCRequestID: "q"},
		"paypal":               {Flow: FlowMethod, Method: "paypal", DCCEnabled: "on"},
		"link":                 {Flow: FlowLink, DCCEnabled: "on"},
	} {
		if _, _, err := BuildPaymentRequest(bad, test); err == nil {
			t.Errorf("%s must fail", name)
		}
	}
	// On production an eDCC test card pays only simulated.
	if _, _, err := BuildPaymentRequest(direct, prod); !errors.Is(err, ErrDCCTestCardNotSimulated) {
		t.Errorf("eDCC test card on production without simulation: %v", err)
	}
	for _, number := range []string{"4176 6622 8061 3305", "4761730000166317"} { // with and without an alias
		own := PaymentInput{Flow: FlowDirect, CardNumber: number, IP: "127.0.0.1"}
		if _, _, err := BuildPaymentRequest(own, prod); !errors.Is(err, ErrDCCTestCardNotSimulated) {
			t.Errorf("eDCC test card %s typed in on production: %v", number, err)
		}
		own.Simulate = "ok"
		if _, _, err := BuildPaymentRequest(own, prod); err != nil {
			t.Errorf("simulated on production: %v", err)
		}
	}
}

func TestProbeDCC(t *testing.T) {
	m := mockpaygate.New("demo-shop", "demo-key")
	srv := httptest.NewServer(m.Handler())
	defer srv.Close()
	c := paygate.New(srv.URL, "demo-shop", "demo-key", paygate.AuthBasic)
	eur := paygate.Amount{Value: 1299, Currency: "EUR"}

	var ps []*DCCProbe
	for _, tc := range []struct {
		number string
		want   DCCOutcome
	}{
		{"4176662280613305", DCCAsExpected},  // USD card: an offer in USD
		{"4176662220", DCCAsExpected},        // EUR card in a EUR shop: no offer
		{"4111111111111111", DCCNoOfferOnly}, // not in the test guide
		{"123", DCCFailed},                   // too short for a BIN
	} {
		p := ProbeDCC(context.Background(), c, tc.number, eur)
		if p.Outcome() != tc.want {
			t.Errorf("%s: outcome %d, want %d (%+v)", tc.number, p.Outcome(), tc.want, p)
		}
		ps = append(ps, p)
	}
	if p := ProbeDCC(context.Background(), c, "4176662280", paygate.Amount{Value: 1299, Currency: "USD"}); p.Outcome() != DCCAsExpected {
		t.Errorf("a USD card in a USD shop gets no offer: %+v", p)
	}
	s := SummarizeDCC(ps)
	if s != (DCCSummary{Total: 4, Offers: 1, AsExpected: 2, Failed: 1, CouldOffer: 1}) || s.Verdict() != DCCActive {
		t.Errorf("summary = %+v, verdict %s", s, s.Verdict())
	}
	// Only a card in the shop's currency and one the guide does not list:
	// no offer could have come, so no verdict.
	if v := SummarizeDCC(ps[1:3]).Verdict(); v != DCCUnclear {
		t.Errorf("verdict without a card that could get an offer = %s", v)
	}
	if v := SummarizeDCC(ps[3:]).Verdict(); v != DCCNoAnswer {
		t.Errorf("verdict when every card failed = %s", v)
	}

	// A merchant without eDCC, as on production: Paygate refuses the BIN
	// check of every card, test card or not, and offers nothing.
	m.DCCOff = true
	var off []*DCCProbe
	for _, n := range []string{"4176662280", "4176662220", "5300000000"} {
		p := ProbeDCC(context.Background(), c, n, eur)
		if p.Outcome() != DCCBinRefused || p.Fatal() {
			t.Errorf("without eDCC, %s: outcome %d", n, p.Outcome())
		}
		off = append(off, p)
	}
	want := DCCBinRefusal{Status: 422, Code: "20500077", Description: "NO VALID PAYMETHOD FOUND"}
	s = SummarizeDCC(off)
	if s.Verdict() != DCCRefused || s.BinRefused != 3 || s.OtherCards != 1 || s.CouldOffer != 0 || s.BinRefusal == nil || *s.BinRefusal != want {
		t.Errorf("without eDCC: %+v, refusal %+v, verdict %s", s, s.BinRefusal, s.Verdict())
	}
	// A card that failed shows nothing either way: still refused.
	if s := SummarizeDCC(append(off[:3:3], ps[3])); s.Verdict() != DCCRefused || s.BinRefused != 3 || s.Failed != 1 || s.BinRefusal == nil {
		t.Errorf("refused and one failed: %+v, verdict %s", s, s.Verdict())
	}
	// The same code with another description: the code stays, the
	// description goes. Another code: no common refusal.
	refuse := func(p *DCCProbe, body string) *DCCProbe {
		bin := *p.Offer.Bin
		bin.Response = &paygate.Response{StatusCode: 422, Body: []byte(body)}
		offer := *p.Offer
		offer.Bin = &bin
		return &DCCProbe{Offer: &offer, Expected: p.Expected, Merchant: p.Merchant}
	}
	same := refuse(off[2], `{"responseCode":"20500077","responseDescription":"OTHER"}`)
	if s := SummarizeDCC([]*DCCProbe{off[0], off[1], same}); s.BinRefusal == nil || *s.BinRefusal != (DCCBinRefusal{Status: 422, Code: "20500077"}) {
		t.Errorf("same code, other description: %+v", s.BinRefusal)
	}
	other := refuse(off[2], `{"responseCode":"20500099"}`)
	for _, ps := range [][]*DCCProbe{{off[0], off[1], other}, {off[0], other, off[1]}} {
		if s := SummarizeDCC(ps); s.Verdict() != DCCRefused || s.BinRefusal != nil {
			t.Errorf("different refusals: %+v, verdict %s", s.BinRefusal, s.Verdict())
		}
	}
	// A BIN check that failed itself refuses nothing: without an offer the
	// card failed.
	for _, status := range []int{500, 503, 429} {
		down := refuse(off[0], `{"title":"Service Unavailable"}`)
		down.Offer.Bin.Response.StatusCode = status
		down.Offer.BinErr = errors.New("server error")
		if down.Outcome() != DCCFailed || down.Offer.BinRefusal() != nil {
			t.Errorf("BIN check %d: outcome %d", status, down.Outcome())
		}
		if v := SummarizeDCC([]*DCCProbe{down}).Verdict(); v != DCCNoAnswer {
			t.Errorf("BIN check %d: verdict %s", status, v)
		}
	}
	// One BIN check answered: the refusal is no longer the verdict, the
	// missing offer for the USD card is.
	answered := &DCCProbe{Expected: "USD", Merchant: "EUR", Offer: &DCCOffer{
		BIN:     "4176662280",
		Country: "USA",
		Bin:     &paygate.BinResult{Lookup: &paygate.BinLookup{CardInfo: &paygate.CardInfo{Country: "USA"}}, Response: &paygate.Response{StatusCode: 200}},
		Quote: &paygate.QuoteResult{Quote: &paygate.DCCQuote{DCCInfo: &paygate.DCCInfo{Status: "NOT_AVAILABLE"}},
			Response: &paygate.Response{StatusCode: 422}},
	}}
	if answered.Outcome() != DCCNotAsExpected {
		t.Errorf("no offer after an answered BIN check: outcome %d", answered.Outcome())
	}
	if s := SummarizeDCC(append(off[:2:2], answered)); s.Verdict() != DCCInactive || s.BinRefused != 2 {
		t.Errorf("one BIN check answered: %+v, verdict %s", s, s.Verdict())
	}
	if v := SummarizeDCC([]*DCCProbe{answered}).Verdict(); v != DCCInactive {
		t.Errorf("no offer for a USD card = %s", v)
	}

	// A refused key and no answer at all stop a check; a bad number does not.
	bad := paygate.New(srv.URL, "demo-shop", "wrong-key", paygate.AuthBasic)
	if p := ProbeDCC(context.Background(), bad, "4176662280", eur); !p.Fatal() {
		t.Errorf("401 must stop the check: %+v", p)
	}
	gone := paygate.New("http://127.0.0.1:1", "demo-shop", "demo-key", paygate.AuthBasic)
	if p := ProbeDCC(context.Background(), gone, "4176662280", eur); !p.Fatal() {
		t.Errorf("no answer must stop the check: %+v", p)
	}
	if ps[3].Fatal() {
		t.Error("a card number that is too short does not stop the check")
	}
}

func TestCheckProdTestCard(t *testing.T) {
	prod := &config.Profile{Brand: "nexi", Env: "prod", MerchantID: "m"}
	test := &config.Profile{Brand: "nexi", Env: "test", MerchantID: "m"}
	in := PaymentInput{Flow: FlowDirect, Card: "dcc-usd", DCC: DCCAccept}
	if err := CheckProdTestCard(in, prod); !errors.Is(err, ErrDCCTestCardNotSimulated) {
		t.Errorf("production without simulation: %v", err)
	}
	for name, ok := range map[string]struct {
		in   PaymentInput
		prof *config.Profile
	}{
		"simulated":     {PaymentInput{Flow: FlowDirect, Card: "dcc-usd", Simulate: "ok"}, prod},
		"test system":   {in, test},
		"no test card":  {PaymentInput{Flow: FlowDirect, Card: "visa-ok"}, prod},
		"payment page":  {PaymentInput{Flow: FlowMethod, Method: "card"}, prod},
		"own real card": {PaymentInput{Flow: FlowDirect, CardNumber: "4111111111111111"}, prod},
	} {
		if err := CheckProdTestCard(ok.in, ok.prof); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
