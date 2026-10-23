// Package export renders prepared Paygate requests as shell commands.
package export

import (
	"fmt"
	"strings"

	"github.com/krebs3r/paygate-cli/internal/paygate"
)

// APIKeyEnv is the variable the exported command reads the key from, so the
// secret never ends up in shell history or screenshots.
const APIKeyEnv = "PAYGATE_API_KEY"

// Curl renders a runnable curl command. Unless showSecret is set, the API key
// is referenced as $PAYGATE_API_KEY instead of being printed.
func Curl(p *paygate.Prepared, c *paygate.Client, showSecret bool) string {
	key := "$" + APIKeyEnv
	if showSecret {
		key = c.APIKey
	}
	var body strings.Builder
	if p.Body != nil {
		body.WriteString(" \\\n  --header 'Content-Type: application/json'")
		if p.IdempotencyKey != "" {
			fmt.Fprintf(&body, " \\\n  --header 'Idempotency-Key: %s'", shellQuote(p.IdempotencyKey))
		}
		fmt.Fprintf(&body, " \\\n  --data '%s'", shellQuote(string(p.Body)))
	}

	if c.Auth == paygate.AuthOAuth2 {
		return fmt.Sprintf(`ACCESS_TOKEN=$(curl --silent --request POST \
  --url '%s' \
  --header 'Content-Type: application/x-www-form-urlencoded' \
  --data-urlencode 'grant_type=client_credentials' \
  --data-urlencode 'client_id=%s' \
  --data-urlencode "client_secret=%s" | jq -r '.access_token')

curl --request %s \
  --url '%s' \
  --header "Authorization: Bearer $ACCESS_TOKEN"%s`,
			c.TokenURL(), shellQuote(c.MerchantID), dq(key), p.Method, p.URL, body.String())
	}
	return fmt.Sprintf(`curl --request %s \
  --url '%s' \
  --user "%s:%s"%s`, p.Method, p.URL, dq(c.MerchantID), dq(key), body.String())
}

// Webhook renders a webhook POST with its exact body and headers.
func Webhook(url string, headers [][2]string, body []byte) string {
	var b strings.Builder
	fmt.Fprintf(&b, "curl --request POST \\\n  --url '%s'", shellQuote(url))
	for _, h := range headers {
		fmt.Fprintf(&b, " \\\n  --header '%s: %s'", shellQuote(h[0]), shellQuote(h[1]))
	}
	fmt.Fprintf(&b, " \\\n  --data-raw '%s'", shellQuote(string(body)))
	return b.String()
}

// shellQuote escapes a value for use inside single quotes.
func shellQuote(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}

// dq escapes a value for use inside double quotes while keeping a leading
// $VARIABLE reference intact.
func dq(s string) string {
	if strings.HasPrefix(s, "$") && !strings.ContainsAny(s[1:], `"\$`+"`") {
		return s
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`")
	return r.Replace(s)
}
