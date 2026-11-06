// Package cards holds the official Paygate test cards.
package cards

import (
	"fmt"
	"strings"
	"time"
)

// Card is a Paygate test card. Any future expiry date and any CVC work.
type Card struct {
	Alias    string
	Brand    string // API brand value: VISA, MASTERCARD, AMEX
	Number   string
	Scenario string // what the card simulates
	Expect   string // expected outcome
	ThreeDS  bool
}

// Cards lists the test cards from the Nexi / Computop test guide.
var Cards = []Card{
	{"visa-ok", "VISA", "4111111111111111", "No 3DS", "success", false},
	{"visa-decline", "VISA", "4012888888881881", "No 3DS", "declined", false},
	{"mc-ok", "MASTERCARD", "5555555555554444", "No 3DS", "success", false},
	{"mc-decline", "MASTERCARD", "5105105105105100", "No 3DS", "declined", false},
	{"amex-ok", "AMEX", "375000000000007", "No 3DS", "success", false},
	{"amex-decline", "AMEX", "371449635398431", "No 3DS", "declined", false},
	{"visa-3ds-frictionless", "VISA", "4000019966199434", "3DS frictionless", "authenticated", true},
	{"mc-3ds-frictionless", "MASTERCARD", "5232122422543299", "3DS frictionless", "authenticated", true},
	{"visa-3ds-challenge", "VISA", "4000012892688323", "3DS challenge", "OTP 1234 = authenticated", true},
	{"mc-3ds-challenge", "MASTERCARD", "5232125125401459", "3DS challenge", "OTP 1234 = authenticated", true},
	{"visa-3ds-not-auth", "VISA", "4000011744135012", "3DS frictionless", "not authenticated", true},
	{"mc-3ds-not-auth", "MASTERCARD", "5232122741507017", "3DS frictionless", "not authenticated", true},
}

// DCCCard is an eDCC test card with an alias, from the table in the Nexi
// test guide ("Test eDCC"). The currency its issuer bills in decides the
// offer: a card in the shop's currency gets none. Payments with it go
// without 3-D Secure, and on production only simulated.
type DCCCard struct {
	Alias, Brand, Number, Currency string
}

// DCCCards has one card per currency of the test guide, with an alias.
var DCCCards = []DCCCard{
	{"dcc-usd", "VISA", "4176662280613305", "USD"},
	{"dcc-chf", "MASTERCARD", "5413330089372172", "CHF"},
	{"dcc-jpy", "MASTERCARD", "5346930600152169", "JPY"},
	{"dcc-sek", "VISA", "4570180040006415", "SEK"},
	{"dcc-nok", "MASTERCARD", "5101916000637029", "NOK"},
	{"dcc-dkk", "VISA", "4571994084776553", "DKK"},
	{"dcc-pln", "MASTERCARD", "2222790000840607", "PLN"},
	{"dcc-nzd", "VISA", "4055470554691478", "NZD"},
	{"dcc-thb", "VISA", "4761320007122404", "THB"},
	{"dcc-isk", "VISA", "4176662240331642", "ISK"},
	{"dcc-eur", "VISA", "4176662220232984", "EUR"},
}

// Card is the eDCC card as a test card without 3-D Secure.
func (d DCCCard) Card() Card {
	return Card{Alias: d.Alias, Brand: d.Brand, Number: d.Number, Scenario: "eDCC · " + d.Currency + " card", Expect: d.Expect()}
}

// Expect is the expected eDCC outcome with a shop in euros.
func (d DCCCard) Expect() string {
	if d.Currency == "EUR" {
		return "no offer in EUR shops"
	}
	return "offer in " + d.Currency
}

// FindDCCCard looks up an eDCC test card by alias (case-insensitive).
func FindDCCCard(alias string) (DCCCard, bool) {
	for _, c := range DCCCards {
		if strings.EqualFold(c.Alias, alias) {
			return c, true
		}
	}
	return DCCCard{}, false
}

// DCCTestCard is a row of the eDCC test table in the Nexi test guide
// ("Test eDCC"): a 10-digit BIN, a card number made from it and the
// currency the card's issuer bills in. That currency decides the offer: a
// card in the shop's currency gets none.
type DCCTestCard struct {
	BIN, Number, Currency string
}

// DCCTestCards is the whole table of the test guide, in its order.
var DCCTestCards = []DCCTestCard{
	{"4055470554", "4055470554691478", "NZD"},
	{"4176662220", "4176662220232984", "EUR"},
	{"4176662240", "4176662240331642", "ISK"},
	{"4176662280", "4176662280613305", "USD"},
	{"4310850000", "4310850000490180", "NOK"},
	{"4507421100", "4507421100113826", "ISK"},
	{"4570180040", "4570180040006415", "SEK"},
	{"4571994084", "4571994084776553", "DKK"},
	{"4761200005", "4761200005083420", "NZD"},
	{"4761320007", "4761320007122404", "THB"},
	{"4761730000", "4761730000166317", "USD"},
	{"4761731000", "4761731000753757", "JPY"},
	{"4761732000", "4761732000475227", "SEK"},
	{"4761739001", "4761739001310311", "USD"},
	{"4999600000", "4999600000971913", "THB"},
	{"2222790000", "2222790000840607", "PLN"},
	{"2223600089", "2223600089146314", "EUR"},
	{"5019791560", "5019791560437271", "USD"},
	{"5101916000", "5101916000637029", "NOK"},
	{"5101918000", "5101918000716399", "SEK"},
	{"5185610090", "5185610090069149", "PLN"},
	{"5346930600", "5346930600152169", "JPY"},
	{"5370601000", "5370601000616041", "JPY"},
	{"5413330001", "5413330001661314", "EUR"},
	{"5413330089", "5413330089372172", "CHF"},
	{"5426360000", "5426360000332778", "ISK"},
	{"5567630000", "5567630000795045", "USD"},
	{"6799998900", "6799998900558922", "USD"},
}

// Brand is the card's API brand, or "" where the number does not tell.
func (c DCCTestCard) Brand() string { return DetectBrand(c.Number) }

// FindDCCTestCard looks up the test guide's row of a card number or BIN.
func FindDCCTestCard(number string) (DCCTestCard, bool) {
	n := strings.ReplaceAll(number, " ", "")
	if len(n) < 10 {
		return DCCTestCard{}, false
	}
	for _, c := range DCCTestCards {
		if c.BIN == n[:10] {
			return c, true
		}
	}
	return DCCTestCard{}, false
}

// IsDCCBIN reports whether a card number, or its first 10 digits, belongs
// to the eDCC test cards of the Nexi test guide.
func IsDCCBIN(number string) bool {
	_, ok := FindDCCTestCard(number)
	return ok
}

// IsTestBIN reports whether a card number, or its first 10 digits, belongs
// to a test card: an eDCC test card or one of Cards.
func IsTestBIN(number string) bool {
	bin, err := BIN(number)
	if err != nil {
		return false
	}
	if IsDCCBIN(bin) {
		return true
	}
	for _, c := range Cards {
		if strings.HasPrefix(c.Number, bin) {
			return true
		}
	}
	return false
}

// ShortBIN shows 6 of the 10 digits of a BIN: with the last four of a
// payment all 10 would give away 14 digits of a real card.
func ShortBIN(bin string) string {
	if len(bin) <= 6 {
		return bin
	}
	return bin[:6] + "••••"
}

// BIN returns the first 10 digits of a card number, which eDCC asks for.
func BIN(number string) (string, error) {
	n := strings.ReplaceAll(number, " ", "")
	if len(n) < 10 || strings.Trim(n, "0123456789") != "" {
		return "", fmt.Errorf("eDCC needs at least the first 10 digits of the card number")
	}
	return n[:10], nil
}

// ChallengeOTPs documents the one-time passwords of the 3DS test ACS.
var ChallengeOTPs = []struct{ Code, Result string }{
	{"1234", "authenticated (Y)"},
	{"1111", "not authenticated (N)"},
	{"2222", "rejected (R)"},
	{"3333", "unavailable (U)"},
	{"7777", "attempted (A)"},
}

// FindCard looks up a test card, also an eDCC one, by alias
// (case-insensitive).
func FindCard(alias string) (Card, bool) {
	for _, c := range Cards {
		if strings.EqualFold(c.Alias, alias) {
			return c, true
		}
	}
	if d, ok := FindDCCCard(alias); ok {
		return d.Card(), true
	}
	return Card{}, false
}

// Aliases returns all test card aliases, e.g. for flag help and completion.
func Aliases() []string {
	out := make([]string, 0, len(Cards)+len(DCCCards))
	for _, c := range Cards {
		out = append(out, c.Alias)
	}
	for _, c := range DCCCards {
		out = append(out, c.Alias)
	}
	return out
}

// DefaultExpiry returns a safely-future expiry date in Paygate's YYYYMM format.
func DefaultExpiry() string {
	return fmt.Sprintf("%d12", time.Now().Year()+4)
}

// DetectBrand guesses the API brand from a card number prefix.
func DetectBrand(number string) string {
	n := strings.ReplaceAll(number, " ", "")
	switch {
	case strings.HasPrefix(n, "4"):
		return "VISA"
	case strings.HasPrefix(n, "34"), strings.HasPrefix(n, "37"):
		return "AMEX"
	case len(n) >= 2 && n[0] == '5' && n[1] >= '1' && n[1] <= '5':
		return "MASTERCARD"
	case len(n) >= 4 && n[:4] >= "2221" && n[:4] <= "2720":
		return "MASTERCARD"
	default:
		return ""
	}
}

// Mask renders a card number as "•••• 1111".
func Mask(number string) string {
	n := strings.ReplaceAll(number, " ", "")
	if len(n) < 4 {
		return n
	}
	return "•••• " + n[len(n)-4:]
}
