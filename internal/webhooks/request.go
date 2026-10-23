package webhooks

import (
	"bytes"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// Request is a webhook as it was received: headers and the exact body.
type Request struct {
	Method string
	Target string
	Header http.Header
	Body   []byte
	Notes  []string // adjustments made while reading
}

var requestLine = regexp.MustCompile(`^([A-Z]+) (\S+)(?: HTTP/\d(?:\.\d)?)?$`)

// ParseRequest reads a saved HTTP request: an optional request line
// ("POST /webhook HTTP/1.1"), header lines, a blank line and the body, with
// CRLF or LF line ends. A file holding only a JSON body is read as body.
// Content-Length, when present, cuts the body to size, which drops a line
// break an editor added at the end.
func ParseRequest(raw []byte) (*Request, error) {
	r := &Request{Header: http.Header{}}
	if t := bytes.TrimLeft(raw, " \t\r\n"); len(t) > 0 && t[0] == '{' {
		r.Body = t
		return r, nil
	}
	head, body, found := splitHead(raw)
	for i, line := range strings.Split(string(head), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if r.Method == "" && len(r.Header) == 0 {
			if m := requestLine.FindStringSubmatch(line); m != nil {
				r.Method, r.Target = m[1], m[2]
				continue
			}
		}
		name, value, ok := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		if !ok || name == "" || strings.ContainsAny(name, " \t") {
			return nil, fmt.Errorf("line %d is neither a request line nor a header: %q", i+1, line)
		}
		r.Header.Add(name, strings.TrimSpace(value))
	}
	if !found {
		return r, nil
	}
	r.Body = body
	if n, err := strconv.Atoi(r.Header.Get("Content-Length")); err == nil {
		switch {
		case len(body) > n:
			r.Body = body[:n]
			r.Notes = append(r.Notes, fmt.Sprintf("body cut to Content-Length (%d bytes)", n))
		case len(body) < n:
			r.Notes = append(r.Notes, fmt.Sprintf("body has %d bytes, Content-Length says %d: the capture may be incomplete", len(body), n))
		}
	}
	return r, nil
}

// splitHead splits at the first blank line.
func splitHead(raw []byte) (head, body []byte, found bool) {
	crlf := bytes.Index(raw, []byte("\r\n\r\n"))
	lf := bytes.Index(raw, []byte("\n\n"))
	switch {
	case crlf >= 0 && (lf < 0 || crlf < lf):
		return raw[:crlf], raw[crlf+4:], true
	case lf >= 0:
		return raw[:lf], raw[lf+2:], true
	}
	return raw, nil, false
}
