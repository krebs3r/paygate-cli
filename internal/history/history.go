// Package history keeps a local list of payments created with paygate.
// Paygate has no "list payments" endpoint, so this is what the Payments
// screen shows. It stores IDs, amounts and states only: no card numbers, no
// keys. Set PAYGATE_NO_HISTORY=1 to disable it.
package history

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/krebs3r/paygate-cli/internal/config"
)

// MaxRecords is how many payments are kept; older ones are dropped.
const MaxRecords = 500

// Event is one step in a payment's timeline.
type Event struct {
	Time     time.Time `json:"time"`
	Kind     string    `json:"kind"` // create, status, capture, refund, cancel
	Status   string    `json:"status,omitempty"`
	Code     string    `json:"code,omitempty"`
	HTTP     int       `json:"http,omitempty"`
	Amount   int64     `json:"amount,omitempty"`
	Duration int64     `json:"durationMs,omitempty"`
}

// Record is one payment.
type Record struct {
	Time        time.Time `json:"time"`
	Updated     time.Time `json:"updated"`
	Profile     string    `json:"profile"`
	Env         string    `json:"env"`
	Brand       string    `json:"brand"`
	MerchantID  string    `json:"merchantId"`
	Flow        string    `json:"flow"`
	TransID     string    `json:"transId"`
	PayID       string    `json:"payId,omitempty"`
	Amount      int64     `json:"amount"`
	Currency    string    `json:"currency"`
	Method      string    `json:"method,omitempty"`
	Status      string    `json:"status,omitempty"`
	Code        string    `json:"responseCode,omitempty"`
	Description string    `json:"responseDescription,omitempty"`
	Simulation  string    `json:"simulation,omitempty"`
	Captured    int64     `json:"captured,omitempty"`
	Refunded    int64     `json:"refunded,omitempty"`
	Events      []Event   `json:"events,omitempty"`
}

var mu sync.Mutex

// Disabled reports whether history is switched off.
func Disabled() bool { return os.Getenv("PAYGATE_NO_HISTORY") != "" }

// Path returns the history file.
func Path() (string, error) {
	d, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "history.jsonl"), nil
}

// Load returns all records, newest first.
func Load() ([]Record, error) {
	mu.Lock()
	defer mu.Unlock()
	return load()
}

func load() ([]Record, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.TransID != "" {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, sc.Err()
}

// Upsert stores a record. An existing record with the same profile and
// transId is updated: non-empty fields win and events are appended.
func Upsert(r Record) error {
	if Disabled() || r.TransID == "" {
		return nil
	}
	mu.Lock()
	defer mu.Unlock()
	all, err := load()
	if err != nil {
		return err
	}
	now := time.Now()
	if r.Time.IsZero() {
		r.Time = now
	}
	r.Updated = now
	found := false
	for i := range all {
		if all[i].TransID == r.TransID && all[i].Profile == r.Profile {
			all[i] = merge(all[i], r)
			found = true
			break
		}
	}
	if !found {
		all = append([]Record{r}, all...)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Time.After(all[j].Time) })
	if len(all) > MaxRecords {
		all = all[:MaxRecords]
	}
	return save(all)
}

// Find returns the record for a payId or transId within a profile.
func Find(profile, id string) (Record, bool) {
	all, err := Load()
	if err != nil {
		return Record{}, false
	}
	for _, r := range all {
		if r.Profile == profile && (r.PayID == id || r.TransID == id) {
			return r, true
		}
	}
	return Record{}, false
}

func merge(old, upd Record) Record {
	out := old
	// A status lookup that found nothing new does not belong in the timeline.
	var events []Event
	for _, ev := range upd.Events {
		if ev.Kind == "status" && ev.Status == old.Status && ev.Code == old.Code {
			continue
		}
		events = append(events, ev)
	}
	upd.Events = events
	set := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	set(&out.PayID, upd.PayID)
	set(&out.Method, upd.Method)
	set(&out.Status, upd.Status)
	set(&out.Code, upd.Code)
	set(&out.Description, upd.Description)
	set(&out.Simulation, upd.Simulation)
	if upd.Amount != 0 {
		out.Amount, out.Currency = upd.Amount, upd.Currency
	}
	if upd.Captured != 0 || upd.Refunded != 0 {
		out.Captured, out.Refunded = upd.Captured, upd.Refunded
	}
	out.Updated = upd.Updated
	out.Events = append(out.Events, upd.Events...)
	return out
}

func save(all []Record) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, r := range all {
		if err := enc.Encode(r); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
