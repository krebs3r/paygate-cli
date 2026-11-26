package tour

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/krebs3r/paygate-cli/internal/cards"
	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

var noteBIN = T{"required · the first 10 digits of the card number", "Pflicht · die ersten 10 Ziffern der Kartennummer"}

// dccCard is the eDCC test card of the tour: one issued in another currency
// than the profile's, US dollars if it can be.
func dccCard(r *Run) cards.DCCTestCard {
	own := r.Session.Profile.DefaultCurrency()
	var other *cards.DCCTestCard
	for i, c := range cards.DCCTestCards {
		if c.Currency == own {
			continue
		}
		if c.Currency == "USD" {
			return c
		}
		if other == nil {
			other = &cards.DCCTestCards[i]
		}
	}
	if other != nil {
		return *other
	}
	return cards.DCCTestCards[0]
}

func dccTour() *Tour {
	return &Tour{
		ID:    "dcc",
		Title: T{"eDCC in your own checkout", "eDCC im eigenen Checkout"},
		Shows: T{"BIN check, quote, the customer's choice, payment, details", "BIN-Check, Quote, Wahl des Kunden, Zahlung, Details"},
		Lanes: []T{laneServer, lanePaygate, laneBrowser},
		Steps: []*Step{
			{
				Short: T{"BIN check", "BIN-Check"},
				Title: T{"Check the card (BIN lookup)", "Karte prüfen (BIN-Check)"},
				Explain: T{
					"As soon as the customer has entered the first 10 digits of the card number, your server can ask whether eDCC may come into question for the card. Paygate answers with the country that issued the card. The step is optional and only helps to decide early whether a quote makes sense.",
					"Sobald der Kunde die ersten 10 Ziffern der Kartennummer eingegeben hat, kann dein Server fragen, ob eDCC für die Karte infrage kommt. Paygate antwortet mit dem Land, das die Karte ausgegeben hat. Der Schritt ist optional und hilft nur, früh zu entscheiden, ob sich eine Quote lohnt.",
				},
				Doc:  docBinCheck,
				Kind: Request,
				Arrows: []Arrow{
					{0, 1, T{"POST /services/dcc/bin-check", "POST /services/dcc/bin-check"}},
					{1, 0, T{"200 · cardInfo.country", "200 · cardInfo.country"}},
				},
				Plan: func(r *Run) (*Planned, error) {
					prep, err := r.Session.Client.CheckBinRequest(dccCard(r).BIN)
					if err != nil {
						return nil, err
					}
					return &Planned{Prepared: prep, Notes: map[string]T{"bin": noteBIN}}, nil
				},
				After: func(r *Run, res *paygate.Result, o *Outcome) {
					var b paygate.BinLookup
					_ = json.Unmarshal(res.Response.Body, &b)
					if c := b.Country(); c != "" {
						o.Status = ui.Success.Render("✓ ") + T{"Card issued in " + c, "Karte aus " + c}.String()
						return
					}
					o.Status = ui.Danger.Render("✗ ") + answerLine(res.Response.StatusCode, string(b.ResponseCode), b.ResponseDescription)
				},
			},
			{
				Short: T{"Quote", "Quote"},
				Title: T{"Get an offer (quote)", "Angebot einholen (Quote)"},
				Explain: T{
					"Once your shop knows amount and currency, your server asks for a quote. Paygate converts the amount into the card's currency and returns the rate, the markup and the dccRequestId you need for the payment. Without an offer, Paygate answers with 422 and status NOT_AVAILABLE.",
					"Kennt dein Shop Betrag und Währung, holt dein Server eine Quote. Paygate rechnet den Betrag in die Währung der Karte um und liefert Kurs, Aufschlag und die dccRequestId, die du für die Zahlung brauchst. Gibt es kein Angebot, antwortet Paygate mit 422 und status NOT_AVAILABLE.",
				},
				Doc:  docQuote,
				Kind: Request,
				Arrows: []Arrow{
					{0, 1, T{"POST /services/dcc/quote", "POST /services/dcc/quote"}},
					{1, 0, T{"200 · dccInfo", "200 · dccInfo"}},
				},
				Plan: func(r *Run) (*Planned, error) {
					r.Amount = amountOf(r, "49.90")
					prep, err := r.Session.Client.QuoteDCCRequest(&paygate.DCCQuoteRequest{Bin: dccCard(r).BIN, Amount: r.Amount})
					if err != nil {
						return nil, err
					}
					notes := map[string]T{"bin": noteBIN, "amount.value": noteValue, "amount.currency": T{"required · your currency", "Pflicht · deine Währung"}}
					return &Planned{Prepared: prep, Notes: notes}, nil
				},
				After: func(r *Run, res *paygate.Result, o *Outcome) {
					var q paygate.DCCQuote
					_ = json.Unmarshal(res.Response.Body, &q)
					r.dcc = q.DCCInfo
					if r.dcc.Available() && r.dcc.Amounts != nil && r.dcc.Amounts.Cardholder != nil {
						o.Status = ui.Success.Render("✓ ") + T{"Offer: ", "Angebot: "}.String() + ui.Bold.Render(ui.DCCAmount(r.dcc.Amounts.Cardholder)) +
							ui.Muted.Render(T{" instead of ", " statt "}.String()+fmtAmount(r.Amount))
						return
					}
					r.dcc = nil
					if q.DCCInfo != nil && q.DCCInfo.Status != "" {
						o.Status = ui.Warning.Render("○ ") + T{"No offer", "Kein Angebot"}.String() + ui.Muted.Render(" · "+q.DCCInfo.Status)
						return
					}
					var e struct {
						Code paygate.Code `json:"responseCode"`
						Desc string       `json:"responseDescription"`
					}
					_ = json.Unmarshal(res.Response.Body, &e)
					o.Status = ui.Danger.Render("✗ ") + answerLine(res.Response.StatusCode, string(e.Code), e.Desc)
				},
			},
			{
				Short: T{"Offer", "Angebot"},
				Title: T{"Show both currencies", "Beide Währungen zeigen"},
				Explain: T{
					"Your checkout shows both amounts at the same time, with the rate, its source and the markup. Neither option may be preselected, and the customer has to agree explicitly. In this tour the customer picks their card's currency.",
					"Dein Checkout zeigt beide Beträge gleichzeitig, mit Kurs, Quelle und Aufschlag. Keine der beiden Optionen darf vorausgewählt sein, und der Kunde muss ausdrücklich zustimmen. In dieser Tour wählt der Kunde die Währung seiner Karte.",
				},
				Doc:    docEDCC,
				Kind:   Local,
				Auto:   true,
				Arrows: []Arrow{{2, 0, T{"customer picks a currency", "Kunde wählt Währung"}}},
				Skip: func(r *Run) T {
					if r.dcc == nil {
						return T{"Paygate made no offer in step 2, so the payment runs in your currency without any eDCC information.",
							"Paygate hat in Schritt 2 kein Angebot gemacht. Die Zahlung läuft deshalb in deiner Währung, ohne eDCC-Angaben."}
					}
					return T{}
				},
				Do: showOffer,
			},
			{
				Short: T{"Payment", "Zahlung"},
				Title: T{"Pay with the customer's choice", "Zahlung mit der Wahl des Kunden"},
				Explain: T{
					"Your server sends the card payment as with DIRECT and adds the customer's choice: if they agreed, paymentMethods.card.dcc carries selected true and the dccRequestId from the quote, and amount is the quote's amount in the card's currency. With your own amount, Paygate refuses the payment (20120006 INVALID CURRENCY). Without an offer, the payment runs as usual in your currency. You never work out the rate yourself; Paygate does.",
					"Dein Server schickt die Kartenzahlung wie bei DIRECT und gibt die Wahl des Kunden mit: Hat er zugestimmt, steht unter paymentMethods.card.dcc selected true und die dccRequestId aus der Quote, und amount ist der Betrag aus der Quote in der Währung der Karte. Mit deinem eigenen Betrag lehnt Paygate die Zahlung ab (20120006 INVALID CURRENCY). Gab es kein Angebot, läuft die Zahlung ganz normal in deiner Währung. Den Kurs rechnest du nie selbst aus, das übernimmt Paygate.",
				},
				Doc:  docEDCC,
				Kind: Request,
				Arrows: []Arrow{
					{0, 1, T{"POST /payments (dcc)", "POST /payments (dcc)"}},
					{1, 0, T{"201 · status", "201 · status"}},
				},
				Plan: func(r *Run) (*Planned, error) {
					card := dccCard(r)
					in := core.PaymentInput{Flow: core.FlowDirect, CardNumber: card.Number, Capture: "auto", Amount: "49.90", Skip3DS: true}
					in.AnswerDCC(core.DCCAccept, r.dcc)
					notes := directNotes()
					if in.DCC == core.DCCAccept {
						notes["amount.value"] = T{"the quote's amount in the card's currency", "Betrag aus der Quote, in der Währung der Karte"}
						notes["amount.currency"] = T{"the card's currency, from the quote", "Währung der Karte, aus der Quote"}
					}
					notes["paymentMethods.card.number"] = T{"eDCC test card, issued in " + card.Currency, "eDCC-Testkarte, ausgegeben in " + card.Currency}
					notes["paymentMethods.card.threeDsPolicy.skip"] = T{"skip the 3DS check", "3DS-Prüfung auslassen"}
					notes["paymentMethods.card.dcc.selected"] = T{"the customer's choice · true = the card's currency", "Wahl des Kunden · true = Währung der Karte"}
					notes["paymentMethods.card.dcc.dccRequestId"] = T{"required with selected true · from the quote", "Pflicht bei selected true · aus der Quote"}
					return payment(r, in, notes)
				},
				After: func(r *Run, res *paygate.Result, o *Outcome) {
					rememberPayment(r, res)
					o.Status = statusLine(res.Payment)
				},
			},
			{
				Short: T{"Details", "Details"},
				Title: T{"Look up the result with eDCC", "Ergebnis mit eDCC abfragen"},
				Explain: T{
					"The converted amount, the rate and whether the customer chose eDCC come with the payment details, not with the answer to the payment. If the customer chose eDCC, your confirmation page shows exactly these values: amount and currency charged, the rate, its source and date, and the markup.",
					"Den umgerechneten Betrag, den Kurs und ob der Kunde eDCC gewählt hat, liefern die Zahlungsdetails, nicht die Antwort auf die Zahlung. Hat der Kunde eDCC gewählt, zeigt deine Bestätigungsseite genau diese Werte: Betrag und Währung der Belastung, Kurs, Quelle und Datum des Kurses und den Aufschlag.",
				},
				Doc:  docStatus,
				Kind: Request,
				Arrows: []Arrow{
					{0, 1, T{"GET getByTransId/{id}", "GET getByTransId/{id}"}},
					{1, 0, T{"200 · dccInfo", "200 · dccInfo"}},
				},
				Plan: func(r *Run) (*Planned, error) { return lookup(r, r.TransID) },
				After: func(r *Run, res *paygate.Result, o *Outcome) {
					rememberPayment(r, res)
					o.Status = statusLine(res.Payment)
					o.Lines = dccOutcome(res.Payment)
				},
			},
		},
	}
}

// showOffer shows the quote as the checkout would: both amounts, nothing
// preselected, with rate, markup and source.
func showOffer(ctx context.Context, r *Run) *Outcome {
	d := r.dcc
	cardholder := ui.DCCAmount(d.Amounts.Cardholder)
	own := fmtAmount(r.Amount)
	w := max(lipgloss.Width(own), lipgloss.Width(cardholder)) + 3
	pad := func(s string) string { return s + strings.Repeat(" ", max(w-lipgloss.Width(s), 1)) }
	lines := []string{
		ui.Muted.Render(T{"What the customer sees, nothing preselected:", "Was der Kunde sieht, nichts vorausgewählt:"}.String()),
		"",
		"  ○  " + ui.Bold.Render(pad(own)) + ui.Label.Render(T{"in the shop's currency", "in der Währung des Shops"}.String()),
		"  ○  " + ui.Bold.Render(pad(cardholder)) + ui.Label.Render(T{"in the card's currency", "in der Währung der Karte"}.String()),
	}
	if rate := ui.DCCRate(d.Amounts, d.Rate); rate != "" {
		if m := ui.DCCMarkup(d.Rate); m != "" {
			rate += " · " + m
		}
		lines = append(lines, "     "+ui.Muted.Render(T{"Rate ", "Kurs "}.String()+rate))
	}
	if src := ui.DCCSource(d.Provider, d.Rate); src != "" {
		lines = append(lines, "     "+ui.Muted.Render(T{"Source ", "Quelle "}.String()+src))
	}
	status := ui.Success.Render("✓ ") + T{"The customer picks " + d.Amounts.Cardholder.Currency + ", their card's currency.",
		"Der Kunde wählt " + d.Amounts.Cardholder.Currency + ", die Währung seiner Karte."}.String()
	return &Outcome{Lines: lines, Status: status}
}

// dccOutcome describes the eDCC part of the payment details.
func dccOutcome(p *paygate.Payment) []string {
	if p == nil {
		return nil
	}
	d := p.Method().DCC
	if d == nil {
		return []string{ui.Muted.Render(T{"No eDCC information: the payment ran in the shop's currency.", "Keine eDCC-Angaben: Die Zahlung lief in der Währung des Shops."}.String())}
	}
	if !d.Selected {
		return []string{ui.Muted.Render(T{"dccSelected false: the customer paid in the shop's currency.", "dccSelected false: Der Kunde hat in der Währung des Shops bezahlt."}.String())}
	}
	rows := ui.DCCRows(d.Provider, d.Rate, d.Amounts)
	w := 0
	for _, row := range rows {
		w = max(w, lipgloss.Width(row.Label))
	}
	lines := []string{ui.Muted.Render(T{"For the confirmation page:", "Für die Bestätigungsseite:"}.String()), ""}
	for _, row := range rows {
		lines = append(lines, "  "+ui.Label.Render(row.Label+strings.Repeat(" ", w-lipgloss.Width(row.Label)+2))+row.Value)
	}
	return lines
}

// answerLine names an HTTP status with Paygate's code and description.
func answerLine(status int, code, desc string) string {
	line := ui.Bold.Render(strconv.Itoa(status))
	if code != "" {
		line += ui.Muted.Render(" · " + code)
	}
	if desc != "" {
		line += ui.Muted.Render(" " + desc)
	}
	return line
}
