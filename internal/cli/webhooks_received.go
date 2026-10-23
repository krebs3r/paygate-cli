package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/listen"
	"github.com/krebs3r/paygate-cli/internal/money"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/ui"
	"github.com/krebs3r/paygate-cli/internal/webhooks"
)

func newWebhooksListCmd(app *App) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the webhooks paygate listen received",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			events, err := receivedEvents(app, all)
			if err != nil {
				return err
			}
			if app.JSON() {
				out := make([]map[string]any, 0, len(events))
				for _, e := range events {
					out = append(out, eventJSON(e))
				}
				b, _ := json.MarshalIndent(out, "", "  ")
				ui.Println(string(b))
				return nil
			}
			ui.Println()
			if len(events) == 0 {
				ui.Println(ui.Info("No webhooks received yet. Start paygate listen, then create a hosted payment."))
				ui.Println()
				ui.Println(ui.Next("paygate listen", "paygate pay --hosted --listen"))
				ui.Println()
				return nil
			}
			var rows [][]string
			for _, e := range events {
				amount := ""
				if e.Amount != nil {
					amount = money.Format(e.Amount.Value, e.Amount.Currency)
				}
				id := e.TransID
				if id == "" {
					id = e.PayID
				}
				answer := strconv.Itoa(e.Answer)
				if e.Repeat > 0 {
					answer += " ↻"
				}
				rows = append(rows, []string{strconv.Itoa(e.ID), e.Time.Format("02.01. 15:04:05"), string(e.Kind),
					signatureLabel(e.Signature), e.Status, amount, id, answer})
			}
			ui.Println(ui.Table([]string{"#", "Received", "Type", "Signature", "Status", "Amount", "Trans / pay ID", "Answer"}, rows))
			ui.Println()
			ui.Println(ui.Next("paygate webhooks show <#>", "paygate webhooks resend <#> --to <url>"))
			ui.Println()
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "webhooks of all profiles, not only the active one")
	return cmd
}

func signatureLabel(s string) string {
	switch s {
	case "valid":
		return "✓ valid"
	case "invalid":
		return "✗ invalid"
	case "unchecked":
		return "? unchecked"
	}
	return "– none"
}

// receivedEvents returns the stored webhooks of the active profile, or of
// all profiles.
func receivedEvents(app *App, all bool) ([]listen.Event, error) {
	events, err := listen.Load()
	if err != nil || all {
		return events, err
	}
	name, _, err := app.Profile()
	if err != nil {
		return nil, err
	}
	var out []listen.Event
	for _, e := range events {
		if e.Profile == name {
			out = append(out, e)
		}
	}
	return out, nil
}

// pickEvent finds a webhook by number, or the newest of the profile.
func pickEvent(app *App, args []string) (listen.Event, error) {
	if len(args) == 1 {
		id, err := strconv.Atoi(strings.TrimPrefix(args[0], "#"))
		if err != nil {
			return listen.Event{}, fmt.Errorf("%q is not a webhook number (see paygate webhooks list)", args[0])
		}
		e, ok := listen.Find(id)
		if !ok {
			return listen.Event{}, fmt.Errorf("no webhook #%d (see paygate webhooks list)", id)
		}
		return e, nil
	}
	events, err := receivedEvents(app, false)
	if err != nil {
		return listen.Event{}, err
	}
	if len(events) == 0 {
		return listen.Event{}, errors.New("no webhooks received yet: start paygate listen, then create a hosted payment")
	}
	return events[0], nil
}

func newWebhooksShowCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "show [#]",
		Short: "Show a received webhook with all checks (default: the newest)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := pickEvent(app, args)
			if err != nil {
				return err
			}
			key := ""
			name, prof, perr := app.Profile()
			if perr == nil && name == e.Profile {
				if key, err = config.HMACKey(name, prof); err != nil {
					return err
				}
			}
			check := webhooks.Verify(e.Header, e.Body, key, e.Time)
			if app.JSON() {
				b, _ := json.MarshalIndent(map[string]any{"webhook": eventJSON(e), "checks": check.Findings}, "", "  ")
				ui.Println(string(b))
				return nil
			}
			if perr == nil {
				app.PrintHeader(name, prof)
			} else {
				ui.Println()
			}
			info := fmt.Sprintf("Webhook #%d · %s · answered %d", e.ID, e.Time.Format("02.01.2006 15:04:05"), e.Answer)
			if e.RemoteIP != "" {
				info += " · from " + e.RemoteIP
			}
			ui.Println(ui.Info(info))
			if e.Repeat > 0 {
				ui.Println(ui.Info(fmt.Sprintf("Delivery %d of the same webhook, %s after the first", e.Repeat+1, sinceText(e.SinceFirst))))
			}
			if f := e.Forward; f != nil {
				if f.Error != "" {
					ui.Println(ui.Warn("Forward to " + f.URL + " failed: " + f.Error))
				} else {
					ui.Println(ui.Info(fmt.Sprintf("Forwarded to %s: %d · %s", f.URL, f.Status, paygate.FormatDuration(f.Duration))))
				}
			}
			ui.Println()
			printChecks(check)
			ui.Println()
			ui.Println(ui.Section("Headers"))
			names := make([]string, 0, len(e.Header))
			for n := range e.Header {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				ui.Println("   " + ui.Label.Render(n+": ") + strings.Join(e.Header[n], ", "))
			}
			ui.Println()
			ui.Println(ui.Section(fmt.Sprintf("Body · %d bytes", len(e.Body))))
			if json.Valid(e.Body) {
				ui.Println(indentLines(ui.PrettyJSON(e.Body), "   "))
			} else {
				ui.Println("   " + string(e.Body))
			}
			ui.Println()
			ui.Println(ui.Next(fmt.Sprintf("paygate webhooks resend %d --to <url>", e.ID)))
			ui.Println()
			return nil
		},
	}
}

func newWebhooksResendCmd(app *App) *cobra.Command {
	var to string
	cmd := &cobra.Command{
		Use:   "resend [#] --to <url>",
		Short: "Send a received webhook again, unchanged, to your handler",
		Long: `Send a received webhook again to your handler: the same body and the
Content-Type and X-Paygate-* headers as Paygate sent them.

An Enhanced webhook keeps its original timestamp, so a handler that checks
the 5-minute window rejects it later on. paygate webhooks send makes a fresh one.`,
		Example: "  paygate webhooks resend 12 --to http://localhost:3000/paygate/webhook",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := url.Parse(to)
			if to == "" || err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
				return errors.New("pass your endpoint with --to http(s)://…")
			}
			e, err := pickEvent(app, args)
			if err != nil {
				return err
			}
			headers := [][2]string{}
			for n, vs := range e.Header {
				if strings.EqualFold(n, "Content-Type") || strings.HasPrefix(strings.ToLower(n), "x-paygate-") {
					headers = append(headers, [2]string{n, strings.Join(vs, ", ")})
				}
			}
			sort.Slice(headers, func(i, j int) bool { return headers[i][0] < headers[j][0] })
			ui.Println()
			ui.Println(" " + ui.Label.Render(fmt.Sprintf("Webhook #%d, %s, as received on %s", e.ID, strings.ToLower(string(e.Kind)), e.Time.Format("02.01. 15:04:05"))))
			if e.Header.Get(webhooks.HeaderTimestamp) != "" {
				if age := time.Since(e.Time); age > webhooks.Tolerance {
					ui.Println(ui.Warn("Its timestamp is " + webhooks.Age(age) + "; a handler that checks it will reject it."))
				}
			}
			ui.Println()
			var res *webhookResponse
			err = ui.Spin("Sending webhook …", func() error {
				var e2 error
				res, e2 = postWebhook(app, to, headers, e.Body)
				return e2
			})
			if err != nil {
				ui.Println(ui.ErrorBox("Could not reach "+target.Host, []string{err.Error()}, "Is your endpoint running and reachable from this computer?"))
				return errSilentFail
			}
			line := res.Status + ui.Muted.Render(" · "+paygate.FormatDuration(res.Duration))
			if !res.Accepted() {
				ui.Println(ui.Fail(line + "   " + fmt.Sprintf("Your endpoint answered %d. Paygate counts only 2xx as delivered.", res.Code)))
				ui.Println()
				return errSilentFail
			}
			ui.Println(ui.OK(line + "   Your endpoint accepted the webhook."))
			ui.Println()
			return nil
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "URL of your webhook endpoint (http or https)")
	return cmd
}

func newWebhooksClearCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Delete all received webhooks kept on this computer",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !app.Yes {
				if !ui.Interactive() {
					return errors.New("refusing to delete the received webhooks without --yes")
				}
				ok, err := ui.Confirm("Delete all received webhooks?", "They are kept on this computer only; Paygate is not affected.", false)
				if err != nil {
					return err
				}
				if !ok {
					return ui.ErrAborted
				}
			}
			n, err := listen.Clear()
			if err != nil {
				return err
			}
			ui.Println(ui.OK(fmt.Sprintf("Deleted %d received webhooks", n)))
			return nil
		},
	}
}

func indentLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
