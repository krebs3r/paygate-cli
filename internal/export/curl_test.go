package export

import (
	"strings"
	"testing"

	"github.com/krebs3r/paygate-cli/internal/paygate"
)

func TestCurlHidesSecretByDefault(t *testing.T) {
	c := paygate.New("https://test.paygate.nexigroup.com", "shop", "s3cr3t'key", paygate.AuthBasic)
	p, err := c.Prepare("POST", "/payments", map[string]string{"transId": "it's"}, "TID1")
	if err != nil {
		t.Fatal(err)
	}
	out := Curl(p, c, false)
	if strings.Contains(out, "s3cr3t") {
		t.Fatalf("secret leaked:\n%s", out)
	}
	for _, want := range []string{`--user "shop:$PAYGATE_API_KEY"`, "Idempotency-Key: TID1", `it'\''s`, "/api/v2/payments"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if !strings.Contains(Curl(p, c, true), "s3cr3t'key") {
		t.Error("--show-secrets should print the key")
	}
}

func TestCurlOAuth(t *testing.T) {
	c := paygate.New("https://test.computop-paygate.com", "shop", "k", paygate.AuthOAuth2)
	p, _ := c.Prepare("GET", "/payments/getByPayId/abc", nil, "")
	out := Curl(p, c, false)
	for _, want := range []string{"https://test.computop-paygate.com/authorization/oauth/token", `client_secret=$PAYGATE_API_KEY`, "Bearer $ACCESS_TOKEN"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "--data '") {
		t.Error("GET must not send a body")
	}
}
