package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/listen"
	"github.com/krebs3r/paygate-cli/internal/money"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/ui"
	"github.com/krebs3r/paygate-cli/internal/webhooks"
)

type listenOptions struct {
	port      int
	publicURL string
	local     bool
	forwardTo string
	respond   int
}

func newListenCmd(app *App) *cobra.Command {
	o := &listenOptions{}
	cmd := &cobra.Command{
		Use:   "listen",
		Short: "Receive Paygate webhooks on this computer",
		Long: `Receive the webhooks Paygate sends for your payments, check them and show
them live.

Paygate only calls public HTTPS addresses, so paygate starts a cloudflared
quick tunnel (no account needed) to a listener on this computer. Payments
created while it runs, with paygate pay or in the app, send their webhooks
here. With --public-url you use an address of your own instead.

Every webhook is checked like paygate webhooks verify does, answered with
200 and kept locally (paygate webhooks list). With --forward-to it also goes
to your own handler, unchanged, and Paygate gets your handler's status, so
you see its retries. Stop with ctrl+c; the address ends with it.`,
		Example: `  paygate listen
  paygate listen --forward-to http://localhost:3000/paygate/webhook
  paygate listen --respond 503          # see how Paygate retries
  paygate listen --public-url https://hooks.example.com`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runListen(app, o) },
	}
	f := cmd.Flags()
	f.IntVar(&o.port, "port", 0, "local port of the listener (default: any free port)")
	f.StringVar(&o.publicURL, "public-url", "", "public HTTPS address that reaches the listener, instead of cloudflared")
	f.BoolVar(&o.local, "local", false, "no tunnel: use the listener's own address (mock server; automatic with a local API)")
	f.StringVar(&o.forwardTo, "forward-to", "", "also post every webhook to your own handler")
	f.IntVar(&o.respond, "respond", 0, "status code for Paygate (default: 200, or your handler's status)")
	return cmd
}

func runListen(app *App, o *listenOptions) error {
	if err := o.validate(); err != nil {
		return err
	}
	name, prof, err := app.Profile()
	if err != nil {
		return err
	}
	key, err := config.HMACKey(name, prof)
	if err != nil {
		return err
	}
	// The API client confirms every webhook with GET; without an API key
	// the webhooks are still shown.
	_, _, client, clientErr := app.Client()
	local := o.local || localAPI(app, prof)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app.PrintHeader(name, prof)
	if prof.IsProd() && !app.JSON() {
		ui.Println(ui.Warn("Production profile: webhooks of real payments pass through the tunnel."))
		ui.Println()
	}
	var out sync.Mutex
	opts := listen.SessionOptions{
		Options: listen.Options{
			Profile: name, Key: key, Port: o.port, ForwardTo: o.forwardTo, Respond: o.respond,
			OnEvent: func(e listen.Event) {
				out.Lock()
				defer out.Unlock()
				if app.JSON() {
					printEventJSON(e)
					return
				}
				ui.Println(eventLine(e))
				for _, l := range eventNotes(e, key, name, true) {
					ui.Println(l)
				}
				if clientErr == nil && e.PayID != "" && e.Repeat == 0 {
					ui.Println(confirmLine(client, e.PayID))
				}
			},
		},
		PublicURL: o.publicURL,
		Local:     local,
	}
	var s *listen.Session
	what := "Starting a cloudflared tunnel …"
	if local || o.publicURL != "" {
		what = "Starting the listener …"
	}
	err = ui.Spin(what, func() error {
		var e error
		s, e = listen.Open(ctx, opts)
		return e
	})
	if err != nil {
		if errors.Is(err, listen.ErrNoCloudflared) {
			printNoCloudflared(prof)
			return errSilentFail
		}
		if errors.Is(err, context.Canceled) {
			return ui.ErrAborted
		}
		return err
	}
	defer s.Close()

	if !app.JSON() {
		printSession(s, key, name, o.forwardTo)
		ui.Println(" " + ui.Success.Render("●") + " " + ui.Bold.Render("Listening.") + " New payments of " + name +
			" send their webhooks here, also from other terminals and the app." + ui.Muted.Render("  ctrl+c stops"))
		ui.Println()
	}
	<-ctx.Done()
	s.Close()
	if !app.JSON() {
		ui.Println()
		closed := "Listener stopped"
		if s.Tunnel != nil {
			closed = "Tunnel closed"
		}
		ui.Println(" " + ui.Label.Render(closed+" · received webhooks are kept · ") + ui.Link.Render("paygate webhooks list"))
		ui.Println()
	}
	return nil
}

func (o *listenOptions) validate() error {
	for flag, u := range map[string]string{"--forward-to": o.forwardTo, "--public-url": o.publicURL} {
		if u == "" {
			continue
		}
		p, err := url.Parse(u)
		if err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" {
			return fmt.Errorf("%s needs an http(s):// URL", flag)
		}
	}
	if o.publicURL != "" && o.local {
		return errors.New("use either --public-url or --local")
	}
	if o.respond != 0 && (o.respond < 100 || o.respond > 599) {
		return fmt.Errorf("--respond %d is not an HTTP status", o.respond)
	}
	return nil
}

// localAPI reports an API on this computer (the mock server): it reaches
// the listener directly, so no tunnel is needed.
func localAPI(app *App, prof *config.Profile) bool {
	origin, err := app.Origin(prof)
	if err != nil {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func printSession(s *listen.Session, key, profile, forwardTo string) {
	row := func(label, value string) {
		ui.Println(ui.OK(ui.Label.Render(padTo(label, 13)) + value))
	}
	row("Listener", strings.TrimPrefix(s.Server.LocalURL(), "http://"))
	switch s.How {
	case listen.ViaTunnel:
		row("Tunnel", "cloudflared quick tunnel"+ui.Muted.Render(fmt.Sprintf(" · reachable after %.1f s", s.Ready.Seconds())))
	case listen.ViaPublicURL:
		row("Address", "your public URL"+ui.Muted.Render(" · reached the listener"))
	default:
		row("Address", "this computer only"+ui.Muted.Render(" · for the mock server; Paygate cannot reach it"))
	}
	row("Webhook URL", ui.Link.Render(s.URL))
	if key != "" {
		row("HMAC key", "of "+profile+ui.Muted.Render(" · Enhanced signatures are checked"))
	} else {
		ui.Println(ui.Warn(ui.Label.Render(padTo("HMAC key", 13)) + "not set · signatures are not checked" +
			ui.Muted.Render(" · paygate profile set hmac-key")))
	}
	if forwardTo != "" {
		ui.Println("   " + ui.Label.Render(padTo("Forward to", 13)) + forwardTo + ui.Muted.Render(" · Paygate gets its status"))
	}
	ui.Println()
}

func printNoCloudflared(prof *config.Profile) {
	ui.Println(ui.Fail(ui.Bold.Render("No tunnel found.") + " Paygate only sends webhooks to a public HTTPS address."))
	ui.Println()
	ui.Println("   " + ui.Label.Render("Install cloudflared once, no account needed:"))
	ui.Println("     " + ui.Link.Render(listen.InstallHint()))
	ui.Println()
	ui.Println("   " + ui.Label.Render("Then run ") + ui.Link.Render("paygate listen") + ui.Label.Render(" again."))
	ui.Println("   " + ui.Label.Render("Already have a public URL?  ") + ui.Link.Render("paygate listen --public-url https://…"))
	ui.Println()
	ui.Println("   " + ui.Muted.Render("Without listening, payments use "+prof.Webhook()))
	ui.Println("   " + ui.Muted.Render("and paygate learns the result by polling, as before."))
	ui.Println()
}

// eventLine is the one-line summary of a received webhook.
func eventLine(e listen.Event) string {
	kind := padTo(string(e.Kind), 9)
	var sig string
	switch e.Signature {
	case "valid":
		sig = ui.Success.Render(padTo("✓ signed", 12))
	case "invalid":
		sig = ui.Danger.Render(padTo("✗ signature", 12))
	case "unchecked":
		sig = ui.Warning.Render(padTo("? unchecked", 12))
	default:
		sig = ui.Muted.Render(padTo("– unsigned", 12))
	}
	var what string
	if e.Kind == webhooks.Enhanced {
		amount := ""
		if e.Amount != nil {
			amount = money.Format(e.Amount.Value, e.Amount.Currency)
		}
		what = statusText(e.Status, 8) + padLeft(amount, 11) + "  " + ui.Label.Render(padTo(e.PaymentMethod, 8)) + e.TransID
	} else {
		what = ui.Label.Render("PayID ") + e.PayID
	}
	answer := ui.Success.Render(fmt.Sprint(e.Answer))
	if e.Answer < 200 || e.Answer > 299 {
		answer = ui.Danger.Render(fmt.Sprint(e.Answer))
	}
	line := " " + ui.Label.Render(e.Time.Format("15:04:05")) + "  " + ui.Link.Render(kind) + sig + what + "  " + ui.Muted.Render("→ ") + answer
	if e.Forward != nil && e.Forward.Error == "" {
		line += ui.Muted.Render("  " + paygate.FormatDuration(e.Forward.Duration))
	}
	if e.Repeat > 0 {
		line += "  " + ui.Warning.Render("↻ again · +"+sinceText(e.SinceFirst))
	}
	return line
}

// sinceText renders the gap between deliveries: "63 s", "12 min".
func sinceText(d time.Duration) string {
	if d < 2*time.Minute {
		return fmt.Sprintf("%d s", int(d.Round(time.Second).Seconds()))
	}
	return fmt.Sprintf("%d min", int(d.Minutes()))
}

// eventNotes explain what needs attention.
func eventNotes(e listen.Event, key, profile string, basicNote bool) []string {
	var notes []string
	indent := "           "
	if e.Forward != nil && e.Forward.Error != "" {
		notes = append(notes, indent+ui.Warning.Render("! ")+ui.Label.Render("Your handler could not be reached: "+e.Forward.Error))
	} else if e.Forward != nil && (e.Forward.Status < 200 || e.Forward.Status > 299) {
		notes = append(notes, indent+ui.Warning.Render("! ")+ui.Label.Render(fmt.Sprintf("Your handler answered %d, so Paygate will send the webhook again.", e.Forward.Status)))
	}
	if e.Signature == "invalid" {
		c := webhooks.Verify(e.Header, e.Body, key, e.Time)
		notes = append(notes, indent+ui.Warning.Render("! ")+ui.Label.Render("Paygate sent "+webhooks.Short(c.Received)+", the HMAC key of "+profile+" gives "+webhooks.Short(c.Expected)+"."))
	}
	if basicNote && e.Kind == webhooks.Basic && e.Repeat == 0 {
		notes = append(notes, indent+ui.Muted.Render("Basic webhook: only the pay ID, no signature."))
	}
	return notes
}

// confirmLine loads the payment like a handler should after a webhook.
func confirmLine(client *paygate.Client, payID string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := client.GetByPayID(ctx, payID)
	prefix := "           " + ui.Label.Render("GET  ")
	if err != nil {
		return prefix + ui.Danger.Render(err.Error())
	}
	p := res.Payment
	parts := []string{statusText(p.Status, 0)}
	if p.Amount != nil {
		parts = append(parts, money.Format(p.Amount.Value, p.Amount.Currency))
	}
	if m := p.Method().Label(); m != "" {
		parts = append(parts, m)
	}
	parts = append(parts, p.TransID)
	return prefix + strings.Join(parts, ui.Muted.Render(" · ")) + ui.Muted.Render("  "+paygate.FormatDuration(res.Response.Duration))
}

func statusText(status string, width int) string {
	s := status
	if width > 0 {
		s = padTo(status, width)
	}
	switch status {
	case "OK", "AUTHORIZED":
		return ui.Success.Render(s)
	case "FAILED":
		return ui.Danger.Render(s)
	}
	return ui.Warning.Render(s)
}

func padLeft(s string, w int) string {
	if n := w - len([]rune(s)); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}

// webhookGrace is how long pay --listen waits for a webhook once the
// payment is final; Paygate sends it within seconds.
var webhookGrace = 20 * time.Second

// payHook receives the webhook of a single payment (pay --listen).
type payHook struct {
	session      *listen.Session
	key, profile string

	mu     sync.Mutex
	events []listen.Event
}

func openPayHook(app *App, name string, prof *config.Profile) (*payHook, error) {
	key, err := config.HMACKey(name, prof)
	if err != nil {
		return nil, err
	}
	h := &payHook{key: key, profile: name}
	opts := listen.SessionOptions{
		Options: listen.Options{Profile: name, Key: key, OnEvent: h.add},
		Local:   localAPI(app, prof),
	}
	what := "Opening a cloudflared tunnel for the webhook …"
	if opts.Local {
		what = "Starting a listener for the webhook …"
	}
	err = ui.Spin(what, func() error {
		var e error
		h.session, e = listen.Open(context.Background(), opts)
		return e
	})
	if err != nil {
		if errors.Is(err, listen.ErrNoCloudflared) {
			printNoCloudflared(prof)
			return nil, errSilentFail
		}
		return nil, err
	}
	if !app.JSON() {
		ui.Println(ui.OK(ui.Label.Render(padTo("Webhook URL", 13)) + ui.Link.Render(h.session.URL) + ui.Muted.Render("  "+string(h.session.How))))
	}
	return h, nil
}

func (h *payHook) add(e listen.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, e)
}

func (h *payHook) received() []listen.Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]listen.Event{}, h.events...)
}

func (h *payHook) close() { h.session.Close() }

// report waits briefly for a webhook still on its way, then prints what
// arrived and the GET that confirmed the payment.
func (h *payHook) report(created time.Time, confirm *paygate.Response) {
	if len(h.received()) == 0 {
		_ = ui.Spin("Waiting for the webhook …", func() error {
			for deadline := time.Now().Add(webhookGrace); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
				if len(h.received()) > 0 {
					return nil
				}
			}
			return nil
		})
	}
	evs := h.received()
	if len(evs) == 0 {
		ui.Println(ui.Warn(fmt.Sprintf("No webhook within %s after the payment was final. paygate listen would show a late one.", webhookGrace)))
	}
	for _, e := range evs {
		ui.Println(ui.OK(ui.Bold.Render(padTo("Webhook received", 21)) + webhookSummary(e, created)))
		for _, n := range eventNotes(e, h.key, h.profile, false) {
			ui.Println(n)
		}
	}
	if confirm != nil {
		ui.Println(ui.OK(ui.Bold.Render(padTo("Confirmed with GET", 21)) +
			ui.Muted.Render(fmt.Sprintf("%d · %s", confirm.StatusCode, paygate.FormatDuration(confirm.Duration)))))
	}
	ui.Println()
}

func webhookSummary(e listen.Event, since time.Time) string {
	parts := []string{ui.Link.Render(string(e.Kind))}
	switch e.Signature {
	case "valid":
		parts = append(parts, ui.Success.Render("✓ signature"))
	case "invalid":
		parts = append(parts, ui.Danger.Render("✗ signature"))
	case "unchecked":
		parts = append(parts, ui.Warning.Render("signature not checked"))
	default:
		parts = append(parts, ui.Label.Render("no signature"))
	}
	if e.Status != "" {
		parts = append(parts, "status "+e.Status)
	}
	if e.Repeat > 0 {
		parts = append(parts, ui.Warning.Render("repeat"))
	}
	d := e.Time.Sub(since).Round(time.Second)
	parts = append(parts, ui.Label.Render(fmt.Sprintf("after %d:%02d", int(d.Minutes()), int(d.Seconds())%60)))
	return strings.Join(parts, ui.Muted.Render(" · "))
}

// eventJSON shows the body as text instead of Base64.
func eventJSON(e listen.Event) map[string]any {
	b, _ := json.Marshal(e)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	m["body"] = string(e.Body)
	return m
}

func printEventJSON(e listen.Event) {
	b, _ := json.Marshal(eventJSON(e))
	ui.Println(string(b))
}
