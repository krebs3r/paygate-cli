// Package tour explains Paygate integrations step by step: each tour is a
// flow such as the hosted payment page, drawn as a sequence diagram, with a
// short explanation per step and the real request and answer. Steps that
// call the API run against the active profile, in practice the demo. The
// app shows tours in its Tour screen, `paygate tour` in the terminal.
package tour

import (
	"context"
	"time"

	"github.com/krebs3r/paygate-cli/internal/i18n"
	"github.com/krebs3r/paygate-cli/internal/paygate"
)

// T is a text in English and German; String picks the app's language.
type T struct{ EN, DE string }

func (t T) String() string {
	if i18n.Current() == i18n.DE && t.DE != "" {
		return t.DE
	}
	return t.EN
}

// Kind says what a step does.
type Kind int

const (
	// Request sends one API request; the tour shows it before it is sent.
	Request Kind = iota
	// Browser is what the customer's browser does; paygate opens a page.
	Browser
	// Wait waits for the customer or a webhook.
	Wait
	// Local is worked out on this computer, e.g. a signature check.
	Local
	// Explain happens in the customer's browser; paygate does not run it
	// and only describes it.
	Explain
)

// Arrow is one row of the sequence diagram: a message from one lane to
// another, or a note on one lane when From equals To.
type Arrow struct {
	From, To int
	Label    T
}

// Step is one step of a tour.
type Step struct {
	Short   T // label in the step bar
	Title   T
	Explain T
	Doc     Doc // where the official documentation describes it
	Kind    Kind
	Arrows  []Arrow
	// Auto runs the step by itself as soon as it is reached: steps that wait
	// and steps that only show something. Next moves on to the following
	// step once this one ran, e.g. after the payment page was opened.
	Auto, Next bool
	// Skip, if it returns a text, skips the step with that reason, e.g. a
	// capture after a failed payment.
	Skip func(r *Run) T
	// Plan builds the request of a Request step before it is sent.
	Plan func(r *Run) (*Planned, error)
	// After takes over what the answer of a Request step tells, such as
	// the payId, and may add lines to the outcome.
	After func(r *Run, res *paygate.Result, o *Outcome)
	// Do runs a step of another kind. An outcome that is Pending asks to
	// run it again a moment later.
	Do func(ctx context.Context, r *Run) *Outcome
}

// Planned is the request of a step, ready to send.
type Planned struct {
	Prepared *paygate.Prepared
	Notes    map[string]T // notes on request fields, by JSON path (amount.value)
	// Record keeps the result in the local history.
	Record func(res *paygate.Result, err error)
}

// Tour is one integration flow.
type Tour struct {
	ID    string
	Title T
	Shows T   // what it shows, in a few words
	Lanes []T // the parties of the sequence diagram
	Steps []*Step
	// NeedsWebhooks: the tour only makes sense when paygate can receive
	// the webhook (demo, local mock or a running listener).
	NeedsWebhooks bool
}

// All returns the tours in the order of the menu.
func All() []*Tour {
	return []*Tour{hostedTour(), directTour(), webhooksTour(), operationsTour()}
}

// Find returns the tour with id ("hosted", "direct", "webhooks",
// "operations").
func Find(id string) *Tour {
	for _, t := range All() {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// Outcome is what running a step gave.
type Outcome struct {
	Pending  bool   // not decided yet: run the step again in a moment
	Status   string // one line about the state, e.g. waiting for the customer
	Exchange *Exchange
	Webhook  *Webhook
	Lines    []string // more to show, already styled
	Skipped  string   // why the step was not run
	Err      error
}

// Exchange is a request with its answer, as the tour shows it.
type Exchange struct {
	Method, URL string
	Headers     [][2]string // as sent, secrets masked
	Body        []byte
	Notes       map[string]T
	Status      int
	Duration    time.Duration
	Response    []byte
	Correlation string
	Curl        string
}
