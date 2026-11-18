package demo

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/krebs3r/paygate-cli/internal/mockpaygate"
	"github.com/krebs3r/paygate-cli/internal/webhooks"
)

// Scenarios set how the demo Paygate behaves, e.g. to show a decline or an
// error without changing any code. They live in the demo folder, so the
// app and single commands share them.
type Scenarios struct {
	Checkout  string `json:"checkout,omitempty"`  // ask (default), pay, decline
	ThreeDS   string `json:"3ds,omitempty"`       // cards (default), always
	Webhook   string `json:"webhook,omitempty"`   // enhanced (default), basic, off
	Signature string `json:"signature,omitempty"` // ok (default), wrong-key, changed-body, old-timestamp
	EDCC      string `json:"edcc,omitempty"`      // on (default), off
	FailNext  string `json:"fail-next,omitempty"` // none (default), 400, 401, 429, 503: once
}

// Scenario describes one setting: its name for `paygate demo scenario`
// and the values it takes, the default first.
type Scenario struct {
	Name   string
	Values []string
	field  func(*Scenarios) *string
}

// ScenarioList lists the scenarios in the order the app shows them.
var ScenarioList = []Scenario{
	{"checkout", []string{"ask", "pay", "decline"}, func(s *Scenarios) *string { return &s.Checkout }},
	{"3ds", []string{"cards", "always"}, func(s *Scenarios) *string { return &s.ThreeDS }},
	{"webhook", []string{"enhanced", "basic", "off"}, func(s *Scenarios) *string { return &s.Webhook }},
	{"signature", []string{"ok", "wrong-key", "changed-body", "old-timestamp"}, func(s *Scenarios) *string { return &s.Signature }},
	{"edcc", []string{"on", "off"}, func(s *Scenarios) *string { return &s.EDCC }},
	{"fail-next", []string{"none", "400", "401", "429", "503"}, func(s *Scenarios) *string { return &s.FailNext }},
}

// Get returns the value of a scenario, its default when unset.
func (s Scenarios) Get(name string) string {
	for _, sc := range ScenarioList {
		if sc.Name == name {
			if v := *sc.field(&s); v != "" {
				return v
			}
			return sc.Values[0]
		}
	}
	return ""
}

// Set changes a scenario; it refuses unknown names and values.
func (s *Scenarios) Set(name, value string) error {
	value = strings.ToLower(strings.TrimSpace(value))
	var names []string
	for _, sc := range ScenarioList {
		names = append(names, sc.Name)
		if sc.Name != name {
			continue
		}
		for _, v := range sc.Values {
			if v == value {
				if v == sc.Values[0] {
					v = "" // the default is not stored
				}
				*sc.field(s) = v
				return nil
			}
		}
		return fmt.Errorf("unknown value %q for %s (use %s)", value, name, strings.Join(sc.Values, ", "))
	}
	sort.Strings(names)
	return fmt.Errorf("unknown scenario %q (use %s)", name, strings.Join(names, ", "))
}

// behaviour turns the scenarios into what the demo Paygate does.
func (s Scenarios) behaviour() mockpaygate.Behaviour {
	b := mockpaygate.Behaviour{Checkout: mockpaygate.CheckoutAsk, Webhook: "enhanced"}
	switch s.Get("checkout") {
	case "pay":
		b.Checkout = mockpaygate.CheckoutAuto
	case "decline":
		b.Checkout = mockpaygate.CheckoutDecline
	}
	b.Require3DS = s.Get("3ds") == "always"
	if w := s.Get("webhook"); w != "off" {
		b.Webhook = w
	} else {
		b.Webhook = ""
	}
	switch s.Get("signature") {
	case "wrong-key":
		b.Tamper = webhooks.TamperSignature
	case "changed-body":
		b.Tamper = webhooks.TamperBody
	case "old-timestamp":
		b.Tamper = webhooks.TamperTimestamp
	}
	b.DCCOff = s.Get("edcc") == "off"
	b.FailNext, _ = strconv.Atoi(s.Get("fail-next"))
	return b
}

const scenariosFile = "scenarios.json"

var scnMu sync.Mutex

func scenariosPath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, scenariosFile), nil
}

// LoadScenarios reads the scenarios; without a file all are at their
// default.
func LoadScenarios() (Scenarios, error) {
	scnMu.Lock()
	defer scnMu.Unlock()
	return loadScenarios()
}

func loadScenarios() (Scenarios, error) {
	var s Scenarios
	p, err := scenariosPath()
	if err != nil {
		return s, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}

// SaveScenarios stores the scenarios and applies them to the demo Paygate
// of this process. One running in another paygate process picks them up
// with its next request.
func SaveScenarios(s Scenarios) error {
	scnMu.Lock()
	err := saveScenarios(s)
	scnMu.Unlock()
	if err != nil {
		return err
	}
	if p := Running(); p != nil {
		p.applyScenarios(s)
	}
	return nil
}

func saveScenarios(s Scenarios) error {
	p, err := scenariosPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Scenarios returns the scenarios the demo Paygate runs with.
func (p *Paygate) Scenarios() Scenarios {
	p.scnMu.Lock()
	defer p.scnMu.Unlock()
	return p.scn
}

func (p *Paygate) applyScenarios(s Scenarios) {
	p.scnMu.Lock()
	p.scn, p.scnAt = s, time.Now()
	p.scnMu.Unlock()
	p.mock.SetBehaviour(s.behaviour())
}

// syncScenarios runs around every request: before it, it takes over
// scenarios another paygate process saved; after it, it notes that a
// one-time error was used up, so the next call works again.
func (p *Paygate) syncScenarios(before bool) {
	path, err := scenariosPath()
	if err != nil {
		return
	}
	if before {
		info, err := os.Stat(path)
		p.scnMu.Lock()
		changed := err == nil && info.ModTime().After(p.scnAt)
		p.scnMu.Unlock()
		if changed {
			if s, err := LoadScenarios(); err == nil {
				p.applyScenarios(s)
			}
		}
		return
	}
	s := p.Scenarios()
	if s.FailNext != "" && p.mock.Behaviour().FailNext == 0 {
		s.FailNext = ""
		_ = SaveScenarios(s)
	}
}
