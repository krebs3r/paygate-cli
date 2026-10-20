// Package money converts between human-readable amounts ("12.99") and the
// minor-unit integers ("1299") that the Paygate REST API expects.
package money

import (
	"fmt"
	"math/big"
	"strings"
)

// Currencies offered in prompts. Any ISO 4217 code is accepted via flags.
var Common = []string{"EUR", "USD", "GBP", "CHF", "SEK", "DKK", "NOK", "PLN", "CZK"}

var zeroDecimal = map[string]bool{
	"BIF": true, "CLP": true, "DJF": true, "GNF": true, "ISK": true, "JPY": true,
	"KMF": true, "KRW": true, "PYG": true, "RWF": true, "UGX": true, "VND": true,
	"VUV": true, "XAF": true, "XOF": true, "XPF": true,
}

var threeDecimal = map[string]bool{
	"BHD": true, "IQD": true, "JOD": true, "KWD": true, "LYD": true, "OMR": true, "TND": true,
}

// Exponent returns the number of minor-unit digits of an ISO currency.
func Exponent(currency string) int {
	c := strings.ToUpper(currency)
	switch {
	case zeroDecimal[c]:
		return 0
	case threeDecimal[c]:
		return 3
	default:
		return 2
	}
}

// Parse converts "12.99", "12,99" or "12" into minor units of the currency.
func Parse(amount, currency string) (int64, error) {
	s := strings.TrimSpace(strings.ReplaceAll(amount, ",", "."))
	if s == "" {
		return 0, fmt.Errorf("amount is empty")
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return 0, fmt.Errorf("%q is not a valid amount", amount)
	}
	if r.Sign() <= 0 {
		return 0, fmt.Errorf("amount must be greater than zero")
	}
	exp := Exponent(currency)
	scaled := new(big.Rat).Mul(r, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exp)), nil)))
	if !scaled.IsInt() {
		return 0, fmt.Errorf("%s allows at most %d decimal places", strings.ToUpper(currency), exp)
	}
	if !scaled.Num().IsInt64() {
		return 0, fmt.Errorf("amount is too large")
	}
	return scaled.Num().Int64(), nil
}

// Format renders minor units as "12.99 EUR".
func Format(minor int64, currency string) string {
	return FormatValue(minor, currency) + " " + strings.ToUpper(currency)
}

// FormatValue renders minor units as "12.99" without the currency code.
func FormatValue(minor int64, currency string) string {
	exp := Exponent(currency)
	if exp == 0 {
		return fmt.Sprintf("%d", minor)
	}
	sign := ""
	if minor < 0 {
		sign = "-"
		minor = -minor
	}
	div := int64(1)
	for i := 0; i < exp; i++ {
		div *= 10
	}
	return fmt.Sprintf("%s%d.%0*d", sign, minor/div, exp, minor%div)
}
