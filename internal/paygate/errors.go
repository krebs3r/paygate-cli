package paygate

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// ErrUnauthorized is returned when Paygate rejects the credentials.
var ErrUnauthorized = errors.New("Paygate rejected the credentials (401 Unauthorized)")

// Problem is an RFC 9110 problem-details body (validation errors etc.).
type Problem struct {
	Type     string              `json:"type,omitempty"`
	Title    string              `json:"title,omitempty"`
	Status   int                 `json:"status,omitempty"`
	Detail   string              `json:"detail,omitempty"`
	Instance string              `json:"instance,omitempty"`
	TraceID  string              `json:"traceId,omitempty"`
	Errors   map[string][]string `json:"errors,omitempty"`
}

// FieldErrors returns "field: message" lines in a stable order.
func (p *Problem) FieldErrors() []string {
	if p == nil {
		return nil
	}
	keys := make([]string, 0, len(p.Errors))
	for k := range p.Errors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		for _, msg := range p.Errors[k] {
			out = append(out, fmt.Sprintf("%s: %s", k, msg))
		}
	}
	return out
}

// APIError is any non-2xx answer. Declines (422) and "not found" (404) carry
// a payment body; validation errors (400) carry a Problem.
type APIError struct {
	StatusCode int
	Payment    *Payment
	Problem    *Problem
	Response   *Response
}

func (e *APIError) Error() string {
	switch {
	case e.Payment != nil && (e.Payment.ResponseDescription != "" || e.Payment.ResponseCode != ""):
		return fmt.Sprintf("%d %s: %s (%s)", e.StatusCode, http.StatusText(e.StatusCode), e.Payment.ResponseDescription, e.Payment.ResponseCode)
	case e.Problem != nil && (e.Problem.Title != "" || e.Problem.Detail != ""):
		msg := e.Problem.Title
		if e.Problem.Detail != "" {
			msg = strings.TrimSpace(msg + " " + e.Problem.Detail)
		}
		return fmt.Sprintf("%d %s: %s", e.StatusCode, http.StatusText(e.StatusCode), msg)
	default:
		return fmt.Sprintf("%d %s", e.StatusCode, http.StatusText(e.StatusCode))
	}
}

func newAPIError(resp *Response) *APIError {
	e := &APIError{StatusCode: resp.StatusCode, Response: resp}
	if len(resp.Body) == 0 {
		return e
	}
	var keys map[string]json.RawMessage
	if json.Unmarshal(resp.Body, &keys) != nil {
		return e
	}
	_, hasCode := keys["responseCode"]
	_, hasPayID := keys["payId"]
	_, hasStatus := keys["status"]
	_, hasTitle := keys["title"]
	_, hasErrors := keys["errors"]
	if hasTitle || hasErrors {
		var p Problem
		if json.Unmarshal(resp.Body, &p) == nil {
			e.Problem = &p
		}
	}
	if hasCode || hasPayID || (hasStatus && !hasTitle) {
		var p Payment
		if json.Unmarshal(resp.Body, &p) == nil {
			e.Payment = &p
		}
	}
	return e
}

// AsAPIError unwraps an *APIError.
func AsAPIError(err error) (*APIError, bool) {
	var e *APIError
	ok := errors.As(err, &e)
	return e, ok
}

// IsNotFound reports whether the error is "payment not found".
func IsNotFound(err error) bool {
	e, ok := AsAPIError(err)
	if !ok {
		return false
	}
	return e.StatusCode == http.StatusNotFound || (e.Payment != nil && string(e.Payment.ResponseCode) == "20600061")
}

// CodeClass is the outcome encoded in the first digit of an 8-digit
// Paygate response code.
type CodeClass int

const (
	CodeUnknown CodeClass = iota
	CodeOK
	CodeError
	CodeFatal
	CodePending
	Code3DS
)

func (c CodeClass) String() string {
	switch c {
	case CodeOK:
		return "ok"
	case CodeError:
		return "error"
	case CodeFatal:
		return "fatal error"
	case CodePending:
		return "pending"
	case Code3DS:
		return "3-D Secure step"
	default:
		return "unknown"
	}
}

// ClassifyCode interprets a response code such as "00000000" or "20600061".
func ClassifyCode(code Code) CodeClass {
	s := strings.TrimSpace(string(code))
	if s == "" {
		return CodeUnknown
	}
	if len(s) <= 3 { // HTTP-like codes in some error bodies ("422", "200")
		switch s[0] {
		case '2':
			return CodeOK
		case '4', '5':
			return CodeError
		}
		return CodeUnknown
	}
	switch s[0] {
	case '0':
		return CodeOK
	case '2':
		return CodeError
	case '4':
		return CodeFatal
	case '6':
		return CodePending
	case '7':
		return Code3DS
	}
	return CodeUnknown
}

var knownCodes = map[string]string{
	"00000000": "success",
	"20600061": "payment not found",
	"20000083": "system error",
	"70000000": "3-D Secure versioning done, authentication pending",
	"70000001": "3-D Secure challenge required",
}

// DescribeCode returns a short explanation for well-known codes.
func DescribeCode(code Code) string {
	if d, ok := knownCodes[string(code)]; ok {
		return d
	}
	return ""
}

// Outcome is the interpreted state of a payment.
type Outcome int

const (
	OutcomePending Outcome = iota
	OutcomeSuccess
	OutcomeFailed
)

var (
	successStatuses = map[string]bool{"OK": true, "AUTHORIZED": true, "SUCCESS": true, "CAPTURED": true, "COMPLETED": true, "PAID": true}
	failedStatuses  = map[string]bool{"FAILED": true, "DECLINED": true, "REJECTED": true, "CANCELLED": true, "CANCELED": true, "EXPIRED": true, "ERROR": true}
	pendingStatuses = map[string]bool{"": true, "REQUEST": true, "PENDING": true, "AUTHORIZE_REQUEST": true, "AUTHORIZATION_REQUEST": true, "IN_PROGRESS": true, "INITIATED": true, "CREATED": true, "OPEN": true}
)

// OutcomeOf classifies a payment by status first and response code second.
func OutcomeOf(p *Payment) Outcome {
	if p == nil {
		return OutcomePending
	}
	st := strings.ToUpper(strings.TrimSpace(p.Status))
	cls := ClassifyCode(p.ResponseCode)
	switch {
	case cls == CodePending || cls == Code3DS:
		return OutcomePending
	case successStatuses[st]:
		return OutcomeSuccess
	case failedStatuses[st]:
		return OutcomeFailed
	case pendingStatuses[st] && st != "":
		return OutcomePending
	case cls == CodeOK && st != "":
		return OutcomeSuccess
	case cls == CodeError || cls == CodeFatal:
		return OutcomeFailed
	}
	return OutcomePending
}
