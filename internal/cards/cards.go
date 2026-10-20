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

// ChallengeOTPs documents the one-time passwords of the 3DS test ACS.
var ChallengeOTPs = []struct{ Code, Result string }{
	{"1234", "authenticated (Y)"},
	{"1111", "not authenticated (N)"},
	{"2222", "rejected (R)"},
	{"3333", "unavailable (U)"},
	{"7777", "attempted (A)"},
}

// FindCard looks up a test card by alias (case-insensitive).
func FindCard(alias string) (Card, bool) {
	for _, c := range Cards {
		if strings.EqualFold(c.Alias, alias) {
			return c, true
		}
	}
	return Card{}, false
}

// Aliases returns all test card aliases, e.g. for flag help and completion.
func Aliases() []string {
	out := make([]string, len(Cards))
	for i, c := range Cards {
		out[i] = c.Alias
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
