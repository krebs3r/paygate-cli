package mockpaygate

import (
	"encoding/json"
	"sort"
)

// state is what Snapshot saves: the payments, the status lookups of hosted
// payments and the open eDCC quotes.
type state struct {
	Payments []*record         `json:"payments"`
	Polls    map[string]int    `json:"polls,omitempty"`
	Quotes   map[string]*quote `json:"quotes,omitempty"`
}

// Snapshot returns the mock's payments as JSON, so a later process can go on
// with them (Restore). It holds no secrets: card numbers are kept as brand
// and last four digits only.
func (s *Server) Snapshot() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := state{Polls: s.polls, Quotes: s.quotes}
	for _, rec := range s.byTrans {
		st.Payments = append(st.Payments, rec)
	}
	sort.Slice(st.Payments, func(i, j int) bool { return st.Payments[i].Created.Before(st.Payments[j].Created) })
	return json.Marshal(st)
}

// Restore loads payments saved by Snapshot. Payments of the mock with the
// same transId are replaced.
func (s *Server) Restore(b []byte) error {
	var st state
	if err := json.Unmarshal(b, &st); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range st.Payments {
		if rec == nil || rec.TransID == "" {
			continue
		}
		s.byTrans[rec.TransID] = rec
		if rec.PayID != "" {
			s.byPayID[rec.PayID] = rec
		}
		if rec.LinkPayID != "" {
			s.byPayID[rec.LinkPayID] = rec
		}
	}
	for id, n := range st.Polls {
		s.polls[id] = n
	}
	for id, q := range st.Quotes {
		if q != nil {
			s.quotes[id] = q
		}
	}
	return nil
}

// Clear forgets all payments and quotes.
func (s *Server) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byTrans, s.byPayID = map[string]*record{}, map[string]*record{}
	s.polls, s.quotes = map[string]int{}, map[string]*quote{}
}
