package history

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestUpsertMergesAndOrders(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PAYGATE_NO_HISTORY", "")

	older := Record{Time: time.Now().Add(-time.Hour), Profile: "p", TransID: "T1", Amount: 100, Currency: "EUR",
		Events: []Event{{Kind: "create", HTTP: 201}}}
	if err := Upsert(older); err != nil {
		t.Fatal(err)
	}
	if err := Upsert(Record{Profile: "p", TransID: "T2", Amount: 200, Currency: "EUR"}); err != nil {
		t.Fatal(err)
	}
	if err := Upsert(Record{Profile: "p", TransID: "T1", PayID: "PAY1", Status: "OK", Events: []Event{{Kind: "status", Status: "OK"}}}); err != nil {
		t.Fatal(err)
	}
	all, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].TransID != "T2" {
		t.Fatalf("expected newest first, got %+v", all)
	}
	r, ok := Find("p", "PAY1")
	if !ok || r.Status != "OK" || r.Amount != 100 || len(r.Events) != 2 {
		t.Fatalf("merged record = %+v", r)
	}

	p, _ := Path()
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("history permissions = %v", info.Mode().Perm())
	}
	raw, _ := os.ReadFile(p)
	if strings.Contains(string(raw), "4111") {
		t.Error("history must not contain card numbers")
	}
}

func TestDisabled(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PAYGATE_NO_HISTORY", "1")
	if err := Upsert(Record{Profile: "p", TransID: "T1"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PAYGATE_NO_HISTORY", "")
	if all, _ := Load(); len(all) != 0 {
		t.Errorf("disabled history wrote %d records", len(all))
	}
}
