package cli

import (
	"fmt"
	"strings"
)

// paymentMethod maps CLI-friendly aliases to Paygate paymentMethods.type.
type paymentMethod struct {
	Alias string
	Type  string
	Name  string
}

var paymentMethodList = []paymentMethod{
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

// hostedSingleMethods are offered in the interactive single-method flow.
var hostedSingleMethods = []string{"card", "paypal", "ideal", "sepa", "klarna", "bancontact", "eps", "twint", "p24", "wero"}

// resolveMethod accepts an alias ("sepa") or a raw API value ("DIRECTDEBIT").
func resolveMethod(s string) (paymentMethod, error) {
	s = strings.TrimSpace(s)
	for _, m := range paymentMethodList {
		if strings.EqualFold(m.Alias, s) || strings.EqualFold(m.Type, s) {
			return m, nil
		}
	}
	if s != "" && strings.ToUpper(s) == s && !strings.ContainsAny(s, " ,") {
		return paymentMethod{Alias: strings.ToLower(s), Type: s, Name: s}, nil
	}
	return paymentMethod{}, fmt.Errorf("unknown payment method %q (try: %s)", s, strings.Join(methodAliases(), ", "))
}

func methodAliases() []string {
	out := make([]string, len(paymentMethodList))
	for i, m := range paymentMethodList {
		out[i] = m.Alias
	}
	return out
}

func methodName(apiType string) string {
	for _, m := range paymentMethodList {
		if strings.EqualFold(m.Type, apiType) {
			return m.Name
		}
	}
	return apiType
}
