package reqlog

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTransportRecordsAndRedacts(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PAYGATE_NO_HISTORY", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Correlation-ID", "corr-1")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"OK","access_token":"secret-token"}`)
	}))
	defer srv.Close()

	client := &http.Client{Transport: &Transport{Profile: "shop"}}
	body := `{"paymentMethods":{"card":{"number":"4111111111111111","securityCode":"123"}}}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v2/payments", strings.NewReader(body))
	req.SetBasicAuth("shop", "api-key")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(got), "secret-token") {
		t.Error("the caller must still receive the original response")
	}

	// Requests marked WithoutLog are not recorded.
	req2, _ := http.NewRequestWithContext(WithoutLog(context.Background()), http.MethodGet, srv.URL+"/probe", nil)
	if _, err := client.Do(req2); err != nil {
		t.Fatal(err)
	}

	entries, err := Load()
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %d, %v", len(entries), err)
	}
	e := entries[0]
	if e.Profile != "shop" || e.Status != 200 || e.CorrelationID != "corr-1" || e.Method != "POST" {
		t.Errorf("entry = %+v", e)
	}
	all := e.RequestBody + e.ResponseBody + strings.Join(mapValues(e.RequestHeaders), " ")
	for _, secret := range []string{"4111111111111111", `"123"`, "secret-token", "api-key", "c2hvcDphcGkta2V5"} {
		if strings.Contains(all, secret) {
			t.Errorf("log leaks %q", secret)
		}
	}
	if !strings.Contains(e.RequestBody, "411111••••••1111") || e.RequestHeaders["Authorization"] != "Basic [REDACTED]" {
		t.Errorf("masking: body %s auth %q", e.RequestBody, e.RequestHeaders["Authorization"])
	}

	if err := Clear(); err != nil {
		t.Fatal(err)
	}
	if entries, _ := Load(); len(entries) != 0 {
		t.Error("clear did not remove the entries")
	}
}

func TestKeepsNewestEntries(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PAYGATE_NO_HISTORY", "")
	for i := 0; i < MaxEntries+5; i++ {
		if err := Append(Entry{Method: "GET", URL: "/x", Status: i}); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := Load()
	if len(entries) != MaxEntries {
		t.Fatalf("kept %d entries", len(entries))
	}
}

func mapValues(m map[string]string) []string {
	var out []string
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
