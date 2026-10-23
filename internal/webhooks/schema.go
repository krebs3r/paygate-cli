package webhooks

import (
	"bytes"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// enhancedFields are the documented fields in documented order; refNr is
// the only optional one.
var enhancedFields = []string{"payId", "transId", "refNr", "status", "responseCode", "responseDescription", "amount", "paymentMethods", "creationDate"}

// methodTypes are the documented values of paymentMethods[].type.
var methodTypes = map[string]bool{
	"APPLEPAY": true, "BANCONTACT": true, "BOLETO": true, "CARD": true, "DIRECTDEBIT": true, "EASYCOLLECT": true,
	"FLOAPAY": true, "GOOGLEPAY": true, "IDEAL": true, "INSTANEA": true, "KLARNA": true, "MOBILEPAY": true,
	"MULTIBANCO": true, "MYBANK": true, "PAYPAL": true, "PAYU": true, "PFCONNECT": true, "RATEPAY": true,
	"RIVERTY": true, "SWISH": true, "WERO": true,
}

var currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

// Inspect classifies a body and checks it against the documented schema of
// its kind. Field names are case-sensitive, so "PayID" is reported.
func Inspect(body []byte) (Kind, []Finding) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil || obj == nil {
		return Unknown, []Finding{{Level: Fail, Name: "Body", Detail: "is not a JSON object"}}
	}
	s := &schema{obj: obj, byLower: map[string]string{}, used: map[string]bool{}}
	for k := range obj {
		s.byLower[strings.ToLower(k)] = k
	}
	kind := Unknown
	if len(obj) == 1 && s.byLower["payid"] != "" {
		kind = Basic
	} else {
		for _, f := range enhancedFields[1:] {
			if s.byLower[strings.ToLower(f)] != "" {
				kind = Enhanced
			}
		}
	}
	switch kind {
	case Basic:
		s.str("payId", true)
	case Enhanced:
		for _, f := range []string{"payId", "transId"} {
			s.str(f, true)
		}
		s.str("refNr", false)
		for _, f := range []string{"status", "responseCode", "responseDescription"} {
			s.str(f, true)
		}
		s.amount()
		s.methods()
		s.date()
	default:
		return Unknown, []Finding{{Level: Fail, Name: "Body", Detail: "is neither a Basic nor an Enhanced webhook"}}
	}
	s.unknown()
	if len(s.findings) == 0 {
		name := "Basic"
		if kind == Enhanced {
			name = "Enhanced"
		}
		s.findings = append(s.findings, Finding{Level: Pass, Name: "Body", Detail: "matches the " + name + " webhook schema"})
	}
	return kind, s.findings
}

type schema struct {
	obj      map[string]json.RawMessage
	byLower  map[string]string
	used     map[string]bool
	findings []Finding
}

func (s *schema) add(l Level, name, detail string) {
	s.findings = append(s.findings, Finding{Level: l, Name: name, Detail: detail})
}

// field looks a documented field up and reports a different spelling.
func (s *schema) field(name string, required bool) (json.RawMessage, bool) {
	if v, ok := s.obj[name]; ok {
		s.used[name] = true
		return v, true
	}
	if actual, ok := s.byLower[strings.ToLower(name)]; ok {
		s.used[actual] = true
		if name == "payId" && actual == "PayID" {
			// PayID is accepted as a spelling of payId.
			s.add(Warn, actual, "accepted; the documented name is payId")
		} else {
			s.add(Fail, actual, "should be "+name+" (field names are case-sensitive)")
		}
		return s.obj[actual], true
	}
	if required {
		s.add(Fail, name, "missing")
	}
	return nil, false
}

func (s *schema) str(name string, required bool) {
	raw, ok := s.field(name, required)
	if !ok {
		return
	}
	if string(bytes.TrimSpace(raw)) == "null" {
		if required {
			s.add(Fail, name, "is null")
		}
		return
	}
	var v string
	if json.Unmarshal(raw, &v) != nil {
		s.add(Fail, name, "must be a string")
	} else if v == "null" {
		s.add(Warn, name, `the text "null", not null`)
	}
}

func (s *schema) amount() {
	raw, ok := s.field("amount", true)
	if !ok {
		return
	}
	var a map[string]json.RawMessage
	if json.Unmarshal(raw, &a) != nil || a == nil {
		s.add(Fail, "amount", "must be an object")
		return
	}
	if v, ok := a["value"]; !ok {
		s.add(Fail, "amount.value", "missing")
	} else if _, err := strconv.ParseInt(string(bytes.TrimSpace(v)), 10, 64); err != nil {
		s.add(Fail, "amount.value", string(v)+" must be an integer in minor units")
	}
	var cur string
	if c, ok := a["currency"]; !ok {
		s.add(Fail, "amount.currency", "missing")
	} else if json.Unmarshal(c, &cur) != nil || !currencyRe.MatchString(cur) {
		s.add(Fail, "amount.currency", string(c)+" must be a 3-letter upper-case ISO code")
	}
	for k := range a {
		if k != "value" && k != "currency" {
			s.add(Warn, "amount."+k, "not in the documented schema")
		}
	}
}

func (s *schema) methods() {
	raw, ok := s.field("paymentMethods", true)
	if !ok {
		return
	}
	var list []map[string]json.RawMessage
	prefix := func(i int) string { return "paymentMethods[" + strconv.Itoa(i) + "]" }
	if json.Unmarshal(raw, &list) != nil {
		var one map[string]json.RawMessage
		if json.Unmarshal(raw, &one) != nil || one == nil {
			s.add(Fail, "paymentMethods", "must be an array of objects")
			return
		}
		// A single object is accepted in place of the array.
		s.add(Warn, "paymentMethods", "an object; accepted, the documented type is an array")
		list = []map[string]json.RawMessage{one}
		prefix = func(int) string { return "paymentMethods" }
	}
	for i, m := range list {
		name := prefix(i) + ".type"
		var t string
		if v, ok := m["type"]; !ok {
			s.add(Fail, name, "missing")
		} else if json.Unmarshal(v, &t) != nil {
			s.add(Fail, name, "must be a string")
		} else if !methodTypes[t] {
			s.add(Warn, name, t+" is not in the documented list")
		}
	}
}

func (s *schema) date() {
	raw, ok := s.field("creationDate", true)
	if !ok {
		return
	}
	var v string
	if json.Unmarshal(raw, &v) != nil {
		s.add(Fail, "creationDate", "must be a string")
		return
	}
	if _, err := time.Parse(time.RFC3339, v); err != nil {
		s.add(Fail, "creationDate", v+" is not an RFC 3339 date-time")
	}
}

func (s *schema) unknown() {
	var extra []string
	for k := range s.obj {
		if !s.used[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		s.add(Warn, k, "not in the documented schema")
	}
}
