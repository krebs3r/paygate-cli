package webhooks

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Level grades a finding.
type Level int

const (
	Pass Level = iota
	Info       // neutral, e.g. a header a Basic webhook does not have
	Warn
	Fail
)

func (l Level) String() string {
	switch l {
	case Pass:
		return "pass"
	case Info:
		return "info"
	case Warn:
		return "warn"
	default:
		return "fail"
	}
}

// MarshalJSON writes the level as a word.
func (l Level) MarshalJSON() ([]byte, error) { return json.Marshal(l.String()) }

// Finding is one result of a check. Name is the header or field it is
// about; it is empty for general notes.
type Finding struct {
	Level  Level  `json:"level"`
	Name   string `json:"name,omitempty"`
	Detail string `json:"detail"`
}

// Check is the outcome of Verify.
type Check struct {
	Kind      Kind          `json:"kind"`
	Findings  []Finding     `json:"findings"`
	Signed    bool          `json:"signed"`
	Checked   bool          `json:"checked"` // the signature was compared with a key
	Valid     bool          `json:"valid"`
	Expected  string        `json:"expected,omitempty"`
	Received  string        `json:"received,omitempty"`
	Timestamp string        `json:"timestamp,omitempty"`
	Age       time.Duration `json:"-"`
}

// Failed reports whether a finding failed.
func (c Check) Failed() bool {
	for _, f := range c.Findings {
		if f.Level == Fail {
			return true
		}
	}
	return false
}

// Verify checks a received webhook: signature headers, the signature against
// key, the timestamp against at and the body against the documented schema.
// An empty key skips the comparison of the signature.
func Verify(h http.Header, body []byte, key string, at time.Time) Check {
	kind, schema := Inspect(body)
	c := Check{Kind: kind}
	version := strings.TrimSpace(h.Get(HeaderVersion))
	c.Timestamp = strings.TrimSpace(h.Get(HeaderTimestamp))
	c.Received = strings.TrimSpace(h.Get(HeaderSignature))
	c.Signed = version != "" || c.Timestamp != "" || c.Received != ""

	if !c.Signed && kind != Enhanced {
		for _, name := range []string{HeaderVersion, HeaderTimestamp, HeaderSignature} {
			c.add(Info, name, "missing")
		}
		if kind == Basic {
			c.add(Warn, "", "Basic webhooks carry no signature. If you need the full payment and a signature, "+
				"ask Paygate support whether the Enhanced webhook is enabled for this merchant.")
		}
		c.Findings = append(c.Findings, schema...)
		return c
	}

	switch version {
	case "v1":
		c.add(Pass, HeaderVersion, "v1")
	case "":
		c.add(Warn, HeaderVersion, "missing")
	default:
		c.add(Warn, HeaderVersion, version+" (only v1 is documented)")
	}

	tsOK := false
	switch ts, err := strconv.ParseInt(c.Timestamp, 10, 64); {
	case c.Timestamp == "":
		c.add(Fail, HeaderTimestamp, "missing")
	case err != nil:
		c.add(Fail, HeaderTimestamp, c.Timestamp+" is not a Unix timestamp in seconds")
	default:
		tsOK = true
		c.Age = at.Sub(time.Unix(ts, 0))
		level := Pass
		if c.Age > Tolerance || c.Age < -Tolerance {
			level = Warn
		}
		c.add(level, HeaderTimestamp, c.Timestamp+" · "+Age(c.Age))
	}

	switch {
	case c.Received == "":
		c.add(Fail, HeaderSignature, "missing")
	case !tsOK:
		c.add(Fail, HeaderSignature, Short(c.Received)+" · cannot be checked without a valid timestamp")
	case key == "":
		c.add(Warn, HeaderSignature, Short(c.Received)+" · not checked, no HMAC key")
	default:
		c.Checked = true
		c.Expected = "v1=" + signHex(key, c.Timestamp, body)
		values := v1Values(c.Received)
		for _, v := range values {
			if equalHex(v, c.Expected[3:]) {
				c.Valid = true
			}
		}
		switch {
		case c.Valid:
			c.add(Pass, HeaderSignature, Short(c.Received)+" · matches the HMAC key")
		case len(values) == 0:
			c.add(Fail, HeaderSignature, Short(c.Received)+" · no v1=… value")
		default:
			c.add(Fail, HeaderSignature, Short(c.Received)+" · does not match the HMAC key, expected "+Short(c.Expected))
		}
	}
	c.Findings = append(c.Findings, schema...)
	return c
}

func (c *Check) add(l Level, name, detail string) {
	c.Findings = append(c.Findings, Finding{Level: l, Name: name, Detail: detail})
}

// v1Values returns the v1 entries of a header such as "v1=ab12,v2=cd34".
func v1Values(header string) []string {
	var out []string
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if v, ok := strings.CutPrefix(part, "v1="); ok {
			out = append(out, strings.TrimSpace(v))
		}
	}
	return out
}

func equalHex(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(strings.ToLower(a)), []byte(strings.ToLower(b))) == 1
}

// Age describes how old a timestamp is: "4 s old", "3 h 12 min old" or
// "2 min in the future".
func Age(d time.Duration) string {
	suffix := " old"
	if d < 0 {
		d, suffix = -d, " in the future"
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.1f s%s", d.Seconds(), suffix)
	case d < time.Hour:
		return fmt.Sprintf("%d min%s", int(d.Minutes()), suffix)
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h %d min%s", int(d.Hours()), int(d.Minutes())%60, suffix)
	default:
		return fmt.Sprintf("%d days%s", int(d.Hours()/24), suffix)
	}
}

// Diagnosis explains a signature someone computed for a webhook.
type Diagnosis struct {
	Match bool   `json:"match"`           // the value equals Paygate's signature
	Cause string `json:"cause,omitempty"` // the mistake that reproduces the value
}

// Diagnose compares yours ("v1=<hex>" or just the hex) with the signature
// Paygate computes and, if they differ, tries the usual mistakes.
func Diagnose(key, ts string, body []byte, yours string) Diagnosis {
	yours = strings.TrimSpace(yours)
	yours = strings.TrimPrefix(yours, "v1=")
	right := mac(sha256.New, []byte(key), signedPayload(ts, body))
	if yours == hex.EncodeToString(right) {
		return Diagnosis{Match: true}
	}
	for _, v := range variants(key, ts, body) {
		sum := mac(v.hash, v.key, v.msg)
		if v.plain {
			h := v.hash()
			h.Write(v.msg)
			sum = h.Sum(nil)
		}
		if enc := encodingOf(yours, sum); enc != "" {
			cause := v.cause
			if enc != "hex" {
				if cause != "" {
					cause += ", "
				}
				cause += enc
			}
			return Diagnosis{Cause: cause}
		}
	}
	return Diagnosis{}
}

type variant struct {
	cause string
	hash  func() hash.Hash
	key   []byte
	msg   []byte
	plain bool // a plain hash instead of an HMAC
}

func variants(key, ts string, body []byte) []variant {
	k := []byte(key)
	signed := signedPayload(ts, body)
	vs := []variant{
		{cause: "", hash: sha256.New, key: k, msg: signed},
		{cause: "HMAC-SHA256 over the body alone, without the timestamp", hash: sha256.New, key: k, msg: body},
		{cause: "HMAC-SHA256 over timestamp and body without the dot between them", hash: sha256.New, key: k, msg: append([]byte(ts), body...)},
		{cause: `HMAC-SHA256 over body + "." + timestamp, the wrong way round`, hash: sha256.New, key: k, msg: append(append(append([]byte{}, body...), '.'), ts...)},
	}
	for _, re := range reserialised(body) {
		vs = append(vs, variant{cause: "the body was parsed and serialised again (" + re.how + "); sign the raw bytes as received",
			hash: sha256.New, key: k, msg: signedPayload(ts, re.body)})
	}
	if trimmed := strings.TrimRight(string(body), "\r\n"); len(trimmed) != len(body) {
		vs = append(vs, variant{cause: "the body without its trailing line break", hash: sha256.New, key: k, msg: signedPayload(ts, []byte(trimmed))})
	} else {
		vs = append(vs, variant{cause: "the body with a trailing line break", hash: sha256.New, key: k, msg: signedPayload(ts, append(append([]byte{}, body...), '\n'))})
	}
	vs = append(vs,
		variant{cause: "a key with a trailing line break", hash: sha256.New, key: append(append([]byte{}, k...), '\n'), msg: signed},
		variant{cause: "a key with surrounding spaces removed", hash: sha256.New, key: []byte(strings.TrimSpace(key)), msg: signed},
	)
	if raw, err := hex.DecodeString(key); err == nil && len(raw) > 0 {
		vs = append(vs, variant{cause: "the key was hex-decoded; Paygate uses the key text as it is", hash: sha256.New, key: raw, msg: signed})
	}
	if raw, err := base64.StdEncoding.DecodeString(key); err == nil && len(raw) > 0 {
		vs = append(vs, variant{cause: "the key was Base64-decoded; Paygate uses the key text as it is", hash: sha256.New, key: raw, msg: signed})
	}
	vs = append(vs,
		variant{cause: "HMAC-SHA1 instead of HMAC-SHA256", hash: sha1.New, key: k, msg: signed},
		variant{cause: "HMAC-SHA512 instead of HMAC-SHA256", hash: sha512.New, key: k, msg: signed},
		variant{cause: "a plain SHA-256 hash without the key, not an HMAC", hash: sha256.New, msg: signed, plain: true},
	)
	return vs
}

type reserialisation struct {
	how  string
	body []byte
}

// reserialised returns the body as common JSON libraries write it again,
// leaving out forms identical to the raw bytes.
func reserialised(body []byte) []reserialisation {
	var out []reserialisation
	add := func(how string, b []byte) {
		if len(b) > 0 && string(b) != string(body) {
			out = append(out, reserialisation{how, b})
		}
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, body); err == nil {
		add("compact", compact.Bytes())
	}
	for _, ind := range []string{"  ", "    "} {
		var b bytes.Buffer
		if err := json.Indent(&b, body, "", ind); err == nil {
			add(fmt.Sprintf("indented with %d spaces", len(ind)), b.Bytes())
		}
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if dec.Decode(&v) == nil {
		if b, err := json.Marshal(v); err == nil {
			add("keys sorted", b)
		}
	}
	return out
}

// encodingOf tells how yours encodes sum: "hex", "upper-case hex instead of
// lower case" or "Base64 instead of hex"; empty if it does not.
func encodingOf(yours string, sum []byte) string {
	h := hex.EncodeToString(sum)
	switch {
	case yours == h:
		return "hex"
	case strings.EqualFold(yours, h):
		return "upper-case hex instead of lower case"
	case yours == base64.StdEncoding.EncodeToString(sum), yours == base64.RawURLEncoding.EncodeToString(sum),
		yours == base64.URLEncoding.EncodeToString(sum):
		return "Base64 instead of hex"
	}
	return ""
}
