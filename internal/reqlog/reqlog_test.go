package reqlog

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

// Waiting for a payment asks the same question every few seconds; the log
// keeps one entry per answer.
func TestFoldsRepeatedLookups(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PAYGATE_NO_HISTORY", "")
	start := time.Date(2026, 10, 26, 15, 8, 0, 0, time.UTC)
	lookup := func(sec int, body string) Entry {
		return Entry{Time: start.Add(time.Duration(sec) * time.Second), Profile: "shop", Method: "GET",
			URL: "https://x/api/v2/payments/getByTransId/TID1", Status: 200, ResponseBody: body}
	}
	open := `{"payId":"LINK","status":"OK","responseCode":"00000000","xId":"a"}`
	for i, e := range []Entry{
		{Time: start, Profile: "shop", Method: "POST", URL: "https://x/api/v2/payments/links", Status: 202},
		lookup(2, open), lookup(4, `{"payId":"LINK","status":"OK","responseCode":"00000000","xId":"b"}`), lookup(6, open),
		lookup(8, `{"payId":"TRY1","status":"FAILED","responseCode":"21151201"}`),
		lookup(10, `{"payId":"TRY1","status":"FAILED","responseCode":"21151201"}`),
	} {
		if err := Append(e); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	entries, _ := Load()
	if len(entries) != 3 {
		t.Fatalf("want link, open lookups and failed lookups, got %d: %+v", len(entries), entries)
	}
	if e := entries[1]; e.Repeat != 2 || !e.First.Equal(start.Add(2*time.Second)) || !e.Time.Equal(start.Add(6*time.Second)) {
		t.Errorf("open lookups: repeat %d first %v time %v", e.Repeat, e.First, e.Time)
	}
	if e := entries[0]; e.Repeat != 1 || !strings.Contains(e.ResponseBody, "21151201") {
		t.Errorf("failed lookups = %+v", e)
	}
	// A paid attempt answers OK like the link's own record, but it is
	// another payment.
	_ = Append(lookup(11, `{"payId":"TRY2","status":"OK","responseCode":"00000000"}`))
	if entries, _ := Load(); len(entries) != 4 || entries[0].Repeat != 0 {
		t.Errorf("the paid attempt must get its own entry: %+v", entries[0])
	}
	// Other requests are never folded.
	_ = Append(Entry{Time: start.Add(12 * time.Second), Profile: "shop", Method: "POST", URL: "https://x/api/v2/payments/links", Status: 202})
	_ = Append(Entry{Time: start.Add(14 * time.Second), Profile: "shop", Method: "POST", URL: "https://x/api/v2/payments/links", Status: 202})
	if entries, _ := Load(); len(entries) != 6 {
		t.Errorf("POSTs must stay apart, got %d entries", len(entries))
	}
}

func TestForPayment(t *testing.T) {
	at := func(sec int) time.Time { return time.Date(2026, 10, 26, 15, 0, sec, 0, time.UTC) }
	entries := []Entry{ // newest first, like Load
		{Time: at(5), Profile: "shop", Method: "GET", URL: "https://x/api/v2/payments/getByPayId/PAY2"},
		{Time: at(4), Profile: "other", Method: "GET", URL: "https://x/api/v2/payments/getByTransId/TID1"},
		{Time: at(3), Profile: "shop", Method: "POST", URL: "https://x/api/v2/payments/PAY1/refunds", RequestBody: `{"transId":"TID1"}`},
		{Time: at(2), Profile: "shop", Method: "GET", URL: "https://x/api/v2/payments/getByTransId/TID2"},
		{Time: at(1), Profile: "shop", Method: "POST", URL: "https://x/api/v2/payments/links", RequestBody: `{"transId":"TID1"}`},
	}
	got := ForPayment(entries, "shop", "TID1", "PAY1", "PAY2")
	if len(got) != 3 || !got[0].Time.Equal(at(1)) || !got[1].Time.Equal(at(3)) || !got[2].Time.Equal(at(5)) {
		t.Errorf("ForPayment = %+v", got)
	}
}

func mapValues(m map[string]string) []string {
	var out []string
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
