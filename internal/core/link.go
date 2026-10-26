package core

import (
	"errors"
	"fmt"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/krebs3r/paygate-cli/internal/paygate"
)

// Ways a payment link reaches the customer.
const (
	SendLink  = "link"  // the merchant shares it
	SendEmail = "email" // Paygate e-mails it
	SendSMS   = "sms"   // Paygate texts it
)

// applyLink adds the customer, the delivery and the validity of a payment
// link to the request.
func applyLink(req *paygate.PaymentRequest, in PaymentInput, now time.Time) error {
	var ci paygate.CustomerInfo
	if in.Email != "" {
		addr, err := mail.ParseAddress(in.Email)
		if err != nil || addr.Address != in.Email {
			return fmt.Errorf("%q is not an e-mail address", in.Email)
		}
		ci.Email = in.Email
	}
	if in.Phone != "" {
		p, err := ParsePhone(in.Phone)
		if err != nil {
			return err
		}
		ci.Phone = p
	}
	ci.FirstName, ci.LastName = splitName(in.Name)
	if ci != (paygate.CustomerInfo{}) {
		req.CustomerInfo = &ci
	}

	switch strings.ToLower(strings.TrimSpace(in.Send)) {
	case "", SendLink:
		// Paygate's default: the link comes back in the answer.
	case SendEmail, "e-mail", "mail":
		if ci.Email == "" {
			return errors.New("sending the link by e-mail needs the customer's e-mail address")
		}
		req.ServiceType = "EMAIL"
	case SendSMS:
		if ci.Phone == nil {
			return errors.New("sending the link by SMS needs the customer's mobile number")
		}
		req.ServiceType = "SMS"
	default:
		return fmt.Errorf("unknown delivery %q (use link, email or sms)", in.Send)
	}

	validFor, err := ParseValidity(in.Expires)
	if err != nil {
		return err
	}
	if validFor > 0 {
		req.ExpirationTime = now.Add(validFor).UTC().Truncate(time.Second).Format(time.RFC3339)
	}
	req.PartialPayment = in.Partial
	return nil
}

// ParseValidity reads how long a payment link stays valid: a Go duration
// such as 30m or 24h, or whole days such as 7d. Empty means Paygate's
// setting and returns 0.
func ParseValidity(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	var d time.Duration
	var err error
	if days, ok := strings.CutSuffix(s, "d"); ok {
		var n int
		n, err = strconv.Atoi(days)
		d = time.Duration(n) * 24 * time.Hour
	} else {
		d, err = time.ParseDuration(s)
	}
	if err != nil || d < time.Minute {
		return 0, fmt.Errorf("invalid validity %q (use e.g. 30m, 24h or 7d)", s)
	}
	return d, nil
}

// ParsePhone reads a mobile number written as +<country code> <number>,
// e.g. "+49 170 1234567". Paygate wants the country code on its own.
func ParsePhone(s string) (*paygate.Phone, error) {
	bad := fmt.Errorf("invalid mobile number %q (write it as +<country code> <number>, e.g. +49 170 1234567)", s)
	cc, number, ok := strings.Cut(strings.TrimSpace(s), " ")
	cc, plus := strings.CutPrefix(cc, "+")
	number = strings.NewReplacer(" ", "", "-", "", "/", "").Replace(number)
	if !ok || !plus || !digits(cc) || len(cc) > 3 || !digits(number) {
		return nil, bad
	}
	return &paygate.Phone{CountryCode: cc, Number: number}, nil
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// splitName takes the last word as last name and the rest as first name.
func splitName(name string) (first, last string) {
	words := strings.Fields(name)
	if len(words) == 0 {
		return "", ""
	}
	return strings.Join(words[:len(words)-1], " "), words[len(words)-1]
}

// LinkRecipient says where Paygate sends a payment link: the e-mail address
// or the mobile number, empty when the merchant shares the link.
func LinkRecipient(req *paygate.PaymentRequest) string {
	if req.CustomerInfo == nil {
		return ""
	}
	switch req.ServiceType {
	case "EMAIL":
		return req.CustomerInfo.Email
	case "SMS":
		if p := req.CustomerInfo.Phone; p != nil {
			return "+" + p.CountryCode + " " + p.Number
		}
	}
	return ""
}
