package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/export"
	"github.com/krebs3r/paygate-cli/internal/history"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/ui"
	"github.com/krebs3r/paygate-cli/internal/webhooks"
)

func newWebhooksCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "webhooks",
		Aliases: []string{"webhook"},
		Short:   "Send test webhooks to your endpoint and check received ones",
		Long: `Test the webhook handling of your own integration without waiting for Paygate.

  send     builds the webhook Paygate sends for a payment, signs it with the
           profile's HMAC key and posts it to your endpoint
  verify   checks a webhook you received: signature, timestamp and body

Set the HMAC key once with: paygate profile set hmac-key`,
	}
	cmd.AddCommand(newWebhooksSendCmd(app), newWebhooksVerifyCmd(app))
	return cmd
}

type webhookSendOptions struct {
	to      string
	transID string
	body    string
	kind    string
	tamper  string
}

func newWebhooksSendCmd(app *App) *cobra.Command {
	o := &webhookSendOptions{}
	cmd := &cobra.Command{
		Use:   "send [payId] --to <url>",
		Short: "Send a signed test webhook to your endpoint",
		Long: `Send a webhook to your endpoint the way Paygate does.

The body is built from a payment (payId or --trans-id, looked up with GET)
or taken as it is from a file (--body). Enhanced webhooks are signed with
the profile's HMAC key; Basic webhooks carry only the payId and no
signature.

--tamper breaks one part on purpose, to check that your endpoint rejects it:
  signature   signed with another key
  timestamp   signed correctly, but 10 minutes old
  body        signed, then the amount changed

With --curl the webhook is printed as a curl command instead of sent.`,
		Example: `  paygate webhooks send 6B29FC4093DD010662DE --to https://shop.example.com/paygate/webhook
  paygate webhooks send --trans-id TIDMVGGX4YZ9I9APE --to http://localhost:3000/webhook
  paygate webhooks send --trans-id TIDMVGGX4YZ9I9APE --to http://localhost:3000/webhook --tamper signature
  paygate webhooks send --body payload.json --to http://localhost:3000/webhook --curl`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			payID := ""
			if len(args) == 1 {
				payID = args[0]
			}
			return runWebhookSend(app, o, payID)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.to, "to", "", "URL of your webhook endpoint (http or https)")
	f.StringVar(&o.transID, "trans-id", "", "build the body from the payment with this transaction ID")
	f.StringVar(&o.body, "body", "", "send this file as body instead (- reads stdin)")
	f.StringVar(&o.kind, "type", "enhanced", "webhook type: enhanced or basic")
	f.StringVar(&o.tamper, "tamper", "", "break the webhook on purpose: signature, timestamp or body")
	return cmd
}

func runWebhookSend(app *App, o *webhookSendOptions, payID string) error {
	target, err := url.Parse(o.to)
	if o.to == "" || err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		return errors.New("pass your endpoint with --to http(s)://…")
	}
	sources := 0
	for _, s := range []string{payID, o.transID, o.body} {
		if s != "" {
			sources++
		}
	}
	if sources != 1 {
		return errors.New("pass exactly one of a payId, --trans-id or --body")
	}
	kind := webhooks.Kind(strings.ToUpper(o.kind))
	if kind != webhooks.Enhanced && kind != webhooks.Basic {
		return fmt.Errorf("unknown --type %q (use enhanced or basic)", o.kind)
	}
	tamper, err := webhooks.ParseTamper(o.tamper)
	if err != nil {
		return err
	}
	if kind == webhooks.Basic && tamper != webhooks.TamperNone {
		return errors.New("Basic webhooks are not signed; --tamper needs --type enhanced")
	}

	name, prof, err := app.Profile()
	if err != nil {
		return err
	}
	key := ""
	if kind == webhooks.Enhanced {
		if key, err = config.HMACKey(name, prof); err != nil {
			return err
		}
		if key == "" {
			return fmt.Errorf("profile %s has no HMAC key — set it with `paygate profile set hmac-key` or $PAYGATE_HMAC_KEY", name)
		}
	}

	var msg webhooks.Message
	source := ""
	if o.body != "" {
		raw, err := readInput(o.body)
		if err != nil {
			return err
		}
		source = "from " + o.body
		if kind == webhooks.Basic {
			msg = webhooks.Message{Kind: webhooks.Basic, Body: raw}
		} else if msg, err = webhooks.SignBody(key, raw, time.Now(), tamper); err != nil {
			return err
		}
	} else {
		_, _, client, err := app.Client()
		if err != nil {
			return err
		}
		if !app.Curl {
			app.PrintHeader(name, prof)
		}
		pay, err := loadPayment(client, payID, o.transID)
		if err != nil {
			return err
		}
		source = "from GET by pay ID"
		if o.transID != "" {
			source = "from GET by trans ID"
		}
		if kind == webhooks.Basic {
			msg = webhooks.NewBasic(pay.PayID)
		} else {
			created := time.Now()
			if rec, ok := history.Find(name, pay.TransID); ok && !rec.Time.IsZero() {
				created = rec.Time
			}
			if msg, err = webhooks.NewEnhanced(key, webhooks.FromPayment(pay, created), time.Now(), tamper); err != nil {
				return err
			}
		}
	}

	if app.Curl {
		ui.Println(export.Webhook(o.to, msg.Headers(), msg.Body))
		if msg.Signature != "" && tamper != webhooks.TamperTimestamp {
			ui.Eprintln()
			ui.Eprintln(ui.Muted.Render("# Send it within 5 minutes: receivers reject older timestamps."))
		}
		return nil
	}
	if app.JSON() {
		return sendWebhookJSON(app, o.to, msg, tamper)
	}
	if o.body != "" {
		app.PrintHeader(name, prof)
	}
	what := "Simulates Paygate: Basic body, no signature"
	if kind == webhooks.Enhanced {
		what = "Simulates Paygate: Enhanced body " + source + ", signed with the HMAC key of " + name
	}
	ui.Println(" " + ui.Label.Render(what))
	if tamper != webhooks.TamperNone {
		ui.Println(ui.Warn("Broken on purpose: " + tamperLabel(tamper)))
	}
	ui.Println()
	ui.Println(" " + ui.Link.Render("POST") + " " + o.to)
	for _, h := range msg.Headers()[1:] {
		ui.Println("   " + ui.Label.Render(padTo(h[0], 29)) + webhooks.Short(h[1]))
	}
	ui.Println("   " + ui.Label.Render(padTo("Body", 29)) + fmt.Sprintf("%d bytes · %s", len(msg.Body), strings.ToLower(string(msg.Kind))))
	ui.Println()

	var res *webhookResponse
	err = ui.Spin("Sending webhook …", func() error {
		var e error
		res, e = postWebhook(app, o.to, msg)
		return e
	})
	if err != nil {
		ui.Println(ui.ErrorBox("Could not reach "+target.Host, []string{err.Error()}, "Is your endpoint running and reachable from this computer?"))
		return errSilentFail
	}
	ok := res.Accepted() == (tamper == webhooks.TamperNone)
	line := res.Status + ui.Muted.Render(" · "+paygate.FormatDuration(res.Duration)) + "   " + sendVerdict(res, tamper)
	if ok {
		ui.Println(ui.OK(line))
	} else {
		ui.Println(ui.Fail(line))
		if hint := tamperHint(tamper); hint != "" && res.Accepted() {
			ui.Println("   " + ui.Label.Render(hint))
		}
	}
	if snippet := strings.TrimSpace(string(res.Body)); snippet != "" {
		if len(snippet) > 160 {
			snippet = snippet[:160] + "…"
		}
		ui.Println("   " + ui.Muted.Render(strings.ReplaceAll(snippet, "\n", " ")))
	}
	ui.Println()
	if !ok {
		return errSilentFail
	}
	return nil
}

// loadPayment fetches the payment a webhook is built from.
func loadPayment(client *paygate.Client, payID, transID string) (*paygate.Payment, error) {
	var res *paygate.Result
	err := ui.Spin("Loading payment …", func() error {
		var e error
		if payID != "" {
			res, e = client.GetByPayID(context.Background(), payID)
		} else {
			res, e = client.GetByTransID(context.Background(), transID)
		}
		return e
	})
	if err != nil {
		return nil, err
	}
	return res.Payment, nil
}

type webhookResponse struct {
	Code     int
	Status   string
	Body     []byte
	Duration time.Duration
}

// Accepted reports a 2xx answer, the only one Paygate counts as delivered.
func (r *webhookResponse) Accepted() bool { return r.Code >= 200 && r.Code <= 299 }

func postWebhook(app *App, to string, msg webhooks.Message) (*webhookResponse, error) {
	client := &http.Client{
		Timeout: 15 * time.Second,
		// Like Paygate, report a redirect instead of following it.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	if app.Verbose > 0 {
		client.Transport = app.loggingTransport()
	}
	req, err := http.NewRequest(http.MethodPost, to, bytes.NewReader(msg.Body))
	if err != nil {
		return nil, err
	}
	for _, h := range msg.Headers() {
		req.Header.Set(h[0], h[1])
	}
	req.Header.Set("User-Agent", "paygate-cli (webhook test)")
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return &webhookResponse{Code: resp.StatusCode, Status: resp.Status, Body: body, Duration: time.Since(start)}, nil
}

func sendWebhookJSON(app *App, to string, msg webhooks.Message, tamper webhooks.Tamper) error {
	headers := map[string]string{}
	for _, h := range msg.Headers() {
		headers[h[0]] = h[1]
	}
	out := map[string]any{
		"request": map[string]any{"url": to, "type": msg.Kind, "tamper": string(tamper), "headers": headers, "body": string(msg.Body)},
	}
	res, err := postWebhook(app, to, msg)
	if err != nil {
		return err
	}
	ok := res.Accepted() == (tamper == webhooks.TamperNone)
	out["response"] = map[string]any{"status": res.Code, "durationMs": res.Duration.Milliseconds(), "body": string(res.Body)}
	out["asExpected"] = ok
	b, _ := json.MarshalIndent(out, "", "  ")
	ui.Println(string(b))
	if !ok {
		return errSilentFail
	}
	return nil
}

func tamperLabel(t webhooks.Tamper) string {
	switch t {
	case webhooks.TamperSignature:
		return "signed with another key"
	case webhooks.TamperTimestamp:
		return "dated 10 minutes back"
	default:
		return "the amount changed after signing"
	}
}

func sendVerdict(r *webhookResponse, t webhooks.Tamper) string {
	what := map[webhooks.Tamper]string{
		webhooks.TamperSignature: "the wrong signature",
		webhooks.TamperTimestamp: "the old timestamp",
		webhooks.TamperBody:      "the changed body",
	}[t]
	switch {
	case t == webhooks.TamperNone && r.Accepted():
		return "Your endpoint accepted the webhook."
	case t == webhooks.TamperNone:
		return fmt.Sprintf("Your endpoint answered %d. Paygate counts only 2xx as delivered and sends the webhook again.", r.Code)
	case r.Accepted():
		return "Your endpoint accepted " + what + "."
	default:
		return "Your endpoint rejected " + what + ", as it should."
	}
}

func tamperHint(t webhooks.Tamper) string {
	switch t {
	case webhooks.TamperSignature:
		return `Compare HMAC-SHA256(key, timestamp + "." + raw body) with X-Paygate-Signature and answer 4xx when they differ.`
	case webhooks.TamperTimestamp:
		return "Reject webhooks whose X-Paygate-Timestamp is more than 5 minutes off."
	case webhooks.TamperBody:
		return "Check the signature over the raw body bytes before you parse them."
	}
	return ""
}

type webhookVerifyOptions struct {
	request   string
	body      string
	timestamp string
	signature string
	yours     string
	at        string
}

func newWebhooksVerifyCmd(app *App) *cobra.Command {
	o := &webhookVerifyOptions{}
	cmd := &cobra.Command{
		Use:   "verify (--request <file> | --body <file>)",
		Short: "Check a received webhook: signature, timestamp and body",
		Long: `Check a webhook your endpoint received.

--request reads a saved HTTP request as your endpoint logged it: an optional
request line ("POST /webhook HTTP/1.1"), the headers, a blank line and the
body. --body reads only the body; pass the header values with --timestamp
and --signature. Use - to read from stdin.

The signature is checked with the profile's HMAC key (or $PAYGATE_HMAC_KEY),
the timestamp against --at (default: now) and the body against the
documented schema of the Basic or Enhanced webhook.

--yours takes the signature your own code computed for this webhook. If it
differs, paygate tries the usual mistakes and names the one that produces it.`,
		Example: `  paygate webhooks verify --request webhook.http
  paygate webhooks verify --request webhook.http --yours v1=0f3a…
  paygate webhooks verify --body body.json --timestamp 1792748527 --signature v1=8e4c…
  pbpaste | paygate webhooks verify --request -`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runWebhookVerify(app, o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.request, "request", "", "saved HTTP request with headers and body (- reads stdin)")
	f.StringVar(&o.body, "body", "", "body only (- reads stdin)")
	f.StringVar(&o.timestamp, "timestamp", "", "with --body: value of X-Paygate-Timestamp")
	f.StringVar(&o.signature, "signature", "", "with --body: value of X-Paygate-Signature")
	f.StringVar(&o.yours, "yours", "", "the signature your code computed, to find the mistake")
	f.StringVar(&o.at, "at", "", "when the webhook arrived (RFC 3339 or Unix seconds; default: now)")
	return cmd
}

func runWebhookVerify(app *App, o *webhookVerifyOptions) error {
	if (o.request == "") == (o.body == "") {
		return errors.New("pass either --request or --body")
	}
	var req *webhooks.Request
	if o.request != "" {
		raw, err := readInput(o.request)
		if err != nil {
			return err
		}
		if req, err = webhooks.ParseRequest(raw); err != nil {
			return err
		}
	} else {
		raw, err := readInput(o.body)
		if err != nil {
			return err
		}
		req = &webhooks.Request{Header: http.Header{}, Body: raw}
		if o.timestamp != "" || o.signature != "" {
			req.Header.Set(webhooks.HeaderVersion, "v1")
			req.Header.Set(webhooks.HeaderTimestamp, o.timestamp)
			req.Header.Set(webhooks.HeaderSignature, o.signature)
		}
	}
	at := time.Now()
	if o.at != "" {
		t, err := parseAt(o.at)
		if err != nil {
			return err
		}
		at = t
	}

	key, keyOwner := "", "$PAYGATE_HMAC_KEY"
	name, prof, err := app.Profile()
	switch {
	case err == nil:
		if key, err = config.HMACKey(name, prof); err != nil {
			return err
		}
		if os.Getenv("PAYGATE_HMAC_KEY") == "" {
			keyOwner = "the HMAC key of " + name
		}
	case errors.Is(err, config.ErrNoProfile):
		key = config.CleanSecret(os.Getenv("PAYGATE_HMAC_KEY"))
	default:
		return err
	}

	check := webhooks.Verify(req.Header, req.Body, key, at)
	body := req.Body
	if check.Checked && !check.Valid {
		// Editors and copy & paste add a line break at the end of a file.
		if trimmed := bytes.TrimRight(req.Body, "\r\n"); len(trimmed) < len(req.Body) {
			if c := webhooks.Verify(req.Header, trimmed, key, at); c.Valid {
				check, body = c, trimmed
				check.Findings = append(check.Findings, webhooks.Finding{Level: webhooks.Warn, Name: "Body",
					Detail: "the signature matches once the line break at the end of the input is removed"})
			}
		}
	}
	var diag *webhooks.Diagnosis
	if o.yours != "" {
		if key == "" {
			return errors.New("--yours needs the HMAC key: paygate profile set hmac-key, or $PAYGATE_HMAC_KEY")
		}
		d := webhooks.Diagnose(key, check.Timestamp, body, o.yours)
		diag = &d
	}
	failed := check.Failed() || (diag != nil && !diag.Match)

	if app.JSON() {
		out := map[string]any{
			"kind":      check.Kind,
			"bytes":     len(body),
			"notes":     req.Notes,
			"checks":    check.Findings,
			"signature": map[string]any{"signed": check.Signed, "checked": check.Checked, "valid": check.Valid, "expected": check.Expected, "received": check.Received, "timestamp": check.Timestamp, "ageSeconds": int64(check.Age.Seconds())},
		}
		if diag != nil {
			out["yours"] = diag
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		ui.Println(string(b))
		if failed {
			return errSilentFail
		}
		return nil
	}

	if err == nil {
		app.PrintHeader(name, prof)
	} else {
		ui.Println()
	}
	read := "the body"
	if req.Method != "" {
		read = req.Method + " " + req.Target
	}
	info := fmt.Sprintf("Read %s · %d bytes", read, len(body))
	if ct := req.Header.Get("Content-Type"); ct != "" {
		info += " · " + ct
	}
	if check.Checked {
		info += " · checked with " + keyOwner
	}
	ui.Println(ui.Info(info))
	for _, n := range req.Notes {
		ui.Println(ui.Warn(n))
	}
	ui.Println()
	ui.Println(" " + kindBadge(check.Kind) + "  " + ui.Label.Render(kindSummary(check.Kind)))
	ui.Println()
	ui.Println(ui.Section("Checks"))
	for _, f := range check.Findings {
		printFinding(f)
		if f.Name == webhooks.HeaderTimestamp && f.Level == webhooks.Warn {
			ui.Println("    " + ui.Label.Render("Fine for a saved request. A live receiver must reject anything more than 5 min off."))
		}
	}
	if diag != nil {
		ui.Println()
		ui.Println(ui.Section("Your signature"))
		yours := webhooks.Short(strings.TrimSpace(o.yours))
		switch {
		case diag.Match:
			ui.Println(ui.OK(yours + " matches Paygate's signature."))
		case diag.Cause != "":
			ui.Println(ui.Fail(yours + " differs from Paygate's signature."))
			ui.Println("   " + wrap("It matches "+diag.Cause+".", 76, "   "))
			ui.Println("   " + ui.Label.Render("Paygate signs ") + ui.Link.Render(`timestamp + "." + raw body`) + ui.Label.Render(" with HMAC-SHA256, hex-encoded."))
		default:
			ui.Println(ui.Fail(yours + " differs from Paygate's signature."))
			ui.Println("   " + ui.Label.Render("None of the usual mistakes produces it. Check the HMAC key and that you sign the raw body bytes."))
		}
	}
	ui.Println()
	if failed {
		return errSilentFail
	}
	return nil
}

func printFinding(f webhooks.Finding) {
	icon := map[webhooks.Level]string{
		webhooks.Pass: ui.Success.Render("✓"),
		webhooks.Info: ui.Muted.Render("–"),
		webhooks.Warn: ui.Warning.Render("!"),
		webhooks.Fail: ui.Danger.Render("✗"),
	}[f.Level]
	if f.Name == "" {
		ui.Println(" " + icon + "  " + wrap(f.Detail, 76, "    "))
		return
	}
	ui.Println(" " + icon + "  " + ui.Label.Render(padTo(f.Name, 29)) + f.Detail)
}

func kindBadge(k webhooks.Kind) string {
	switch k {
	case webhooks.Enhanced:
		return ui.Badge("ENHANCED", ui.ToneAccent)
	case webhooks.Basic:
		return ui.Badge("BASIC", ui.ToneNeutral)
	}
	return ui.Badge("UNKNOWN", ui.ToneDanger)
}

func kindSummary(k webhooks.Kind) string {
	switch k {
	case webhooks.Enhanced:
		return "the payment result, signed with HMAC-SHA256"
	case webhooks.Basic:
		return "only the payId; load the payment with GET"
	}
	return "neither a Basic nor an Enhanced webhook"
}

// readInput reads a file, or stdin for "-".
func readInput(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return b, nil
}

// parseAt accepts RFC 3339 or Unix seconds.
func parseAt(s string) (time.Time, error) {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Unix(n, 0), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("--at %q is neither RFC 3339 (2026-10-23T11:42:07Z) nor Unix seconds", s)
	}
	return t, nil
}

func padTo(s string, w int) string {
	if n := w - lipgloss.Width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s + " "
}

// wrap breaks text into lines of at most width cells; continuation lines
// start with indent.
func wrap(text string, width int, indent string) string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		if line != "" && lipgloss.Width(line)+1+lipgloss.Width(word) > width {
			lines = append(lines, line)
			line = word
			continue
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"+indent)
}
