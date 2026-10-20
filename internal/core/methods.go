package core

import (
	"fmt"
	"strings"
)

// PaymentMethod maps CLI-friendly aliases to Paygate paymentMethods.type.
type PaymentMethod struct {
	Alias string
	Type  string
	Name  string
}

// Methods lists the payment methods the CLI knows by alias.
var Methods = []PaymentMethod{
	{"card", "CARD", "Card"},
	{"paypal", "PAYPAL", "PayPal"},
	{"ideal", "IDEAL", "iDEAL"},
	{"sepa", "DIRECTDEBIT", "SEPA Direct Debit"},
	{"klarna", "KLARNA", "Klarna"},
	{"applepay", "APPLEPAY", "Apple Pay"},
	{"googlepay", "GOOGLEPAY", "Google Pay"},
	{"bancontact", "BANCONTACT", "Bancontact"},
	{"eps", "EPS", "EPS"},
	{"twint", "TWINT", "TWINT"},
	{"p24", "PRZELEWY24", "Przelewy24"},
	{"wero", "WERO", "Wero"},
	{"trustly", "TRUSTLY", "Trustly"},
	{"mbway", "MBWAY", "MB WAY"},
	{"swish", "SWISH", "Swish"},
	{"mobilepay", "MOBILEPAY", "MobilePay"},
}

// HostedSingleMethods are offered in the interactive single-method flow.
var HostedSingleMethods = []string{"card", "paypal", "ideal", "sepa", "klarna", "bancontact", "eps", "twint", "p24", "wero"}

// ResolveMethod accepts an alias ("sepa") or a raw API value ("DIRECTDEBIT").
func ResolveMethod(s string) (PaymentMethod, error) {
	s = strings.TrimSpace(s)
	for _, m := range Methods {
		if strings.EqualFold(m.Alias, s) || strings.EqualFold(m.Type, s) {
			return m, nil
		}
	}
	if s != "" && strings.ToUpper(s) == s && !strings.ContainsAny(s, " ,") {
		return PaymentMethod{Alias: strings.ToLower(s), Type: s, Name: s}, nil
	}
	return PaymentMethod{}, fmt.Errorf("unknown payment method %q (try: %s)", s, strings.Join(MethodAliases(), ", "))
}

// MethodAliases returns all aliases, e.g. for shell completion.
func MethodAliases() []string {
	out := make([]string, len(Methods))
	for i, m := range Methods {
		out[i] = m.Alias
	}
	return out
}

// MethodName returns the display name of an API method type.
func MethodName(apiType string) string {
	for _, m := range Methods {
		if strings.EqualFold(m.Type, apiType) {
			return m.Name
		}
	}
	return apiType
}
