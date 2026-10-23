// Package listen receives Paygate webhooks on this computer: a local
// listener behind a cloudflared quick tunnel (or a public URL of your own),
// a file with the received webhooks and the state that tells other paygate
// commands to send their webhooks here.
package listen

import (
	"bufio"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/webhooks"
)

// MaxEvents is how many received webhooks are kept; older ones are dropped.
const MaxEvents = 500

// Event is one received webhook with what paygate made of it.
type Event struct {
	ID            int              `json:"id"`
	Time          time.Time        `json:"time"`
	Profile       string           `json:"profile"`
	Target        string           `json:"target"`
	Header        http.Header      `json:"header"`
	Body          []byte           `json:"body"`
	RemoteIP      string           `json:"remoteIp,omitempty"`
	Kind          webhooks.Kind    `json:"kind"`
	PayID         string           `json:"payId,omitempty"`
	TransID       string           `json:"transId,omitempty"`
	Status        string           `json:"status,omitempty"`
	Amount        *webhooks.Amount `json:"amount,omitempty"`
	PaymentMethod string           `json:"paymentMethod,omitempty"`
	Signature     string           `json:"signature"` // valid, invalid, missing or unchecked
	Answer        int              `json:"answer"`    // status sent back to Paygate
	Forward       *Forward         `json:"forward,omitempty"`
	Repeat        int              `json:"repeat,omitempty"` // earlier deliveries of the same webhook
	SinceFirst    time.Duration    `json:"sinceFirst,omitempty"`
}

// Forward is the outcome of passing a webhook on to your own handler.
type Forward struct {
	URL      string        `json:"url"`
	Status   int           `json:"status,omitempty"`
	Duration time.Duration `json:"duration"`
	Error    string        `json:"error,omitempty"`
}

var mu sync.Mutex

// Disabled reports whether storing is switched off, like the history.
func Disabled() bool { return os.Getenv("PAYGATE_NO_HISTORY") != "" }

// Path returns the file with the received webhooks.
func Path() (string, error) {
	d, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "webhooks.jsonl"), nil
}

// Load returns all stored webhooks, newest first.
func Load() ([]Event, error) {
	mu.Lock()
	defer mu.Unlock()
	return load()
}

func load() ([]Event, error) {
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
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.ID > 0 {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, sc.Err()
}

// Find returns a stored webhook by ID.
func Find(id int) (Event, bool) {
	all, err := Load()
	if err != nil {
		return Event{}, false
	}
	for _, e := range all {
		if e.ID == id {
			return e, true
		}
	}
	return Event{}, false
}

// Append numbers the webhook and stores it.
func Append(e *Event) error {
	if Disabled() {
		return nil
	}
	mu.Lock()
	defer mu.Unlock()
	all, err := load()
	if err != nil {
		return err
	}
	e.ID = 1
	if len(all) > 0 {
		e.ID = all[0].ID + 1
	}
	all = append([]Event{*e}, all...)
	if len(all) > MaxEvents {
		all = all[:MaxEvents]
	}
	return save(all)
}

// Clear deletes all stored webhooks and returns how many there were.
func Clear() (int, error) {
	mu.Lock()
	defer mu.Unlock()
	all, err := load()
	if err != nil {
		return 0, err
	}
	p, err := Path()
	if err != nil {
		return 0, err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	return len(all), nil
}

func save(all []Event) error {
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
	for i := len(all) - 1; i >= 0; i-- { // oldest first, like a log
		if err := enc.Encode(all[i]); err != nil {
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
