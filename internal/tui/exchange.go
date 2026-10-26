package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/krebs3r/paygate-cli/internal/export"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/reqlog"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

// One HTTP exchange from the request log, as the Inspector and the payment
// detail show it.

// exchangeHead names the request: method and URL, outcome, correlation ID
// and profile.
func exchangeHead(e reqlog.Entry, w int) []string {
	outcome := statusText(e) + ui.Muted.Render(fmt.Sprintf(" · %s · %s", paygate.FormatDuration(msDuration(e.DurationMs)), e.Time.Local().Format("02.01. 15:04:05")))
	if e.Repeat > 0 {
		outcome += ui.Muted.Render(" · " + fmt.Sprintf(t("call.since"), e.Repeat+1, e.First.Local().Format("15:04:05")))
	}
	head := []string{
		lipgloss.NewStyle().Foreground(ui.ColAccent).Render(e.Method) + " " + ui.Label.Render(truncate(e.URL, max(w-8, 10))),
		outcome,
	}
	if e.CorrelationID != "" {
		head = append(head, ui.Muted.Render("correlation "+e.CorrelationID))
	}
	if e.Profile != "" {
		head = append(head, ui.Muted.Render("profile "+e.Profile))
	}
	return head
}

// exchangeLines renders one side of the exchange: the headers worth seeing
// and the body.
func exchangeLines(e reqlog.Entry, response bool, w int) []string {
	headers, body := e.RequestHeaders, e.RequestBody
	if response {
		headers, body = e.ResponseHeaders, e.ResponseBody
	}
	lines := []string{""}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !interestingHeader(k) {
			continue
		}
		lines = append(lines, ui.Label.Render(k+": ")+truncate(headers[k], max(w-len(k)-2, 8)))
	}
	lines = append(lines, "")
	switch {
	case e.Error != "" && response:
		lines = append(lines, ui.Danger.Render(t("insp.error")+": ")+ui.Label.Render(wrap(e.Error, w)))
	case strings.TrimSpace(body) == "":
		lines = append(lines, ui.Muted.Render(t("insp.nobody")))
	default:
		lines = append(lines, strings.Split(ui.HighlightJSON([]byte(body)), "\n")...)
	}
	return lines
}

// scrollWindow shows room lines from *scroll on and keeps *scroll inside the
// lines. The last line counts what is left below.
func scrollWindow(lines []string, scroll *int, room int) []string {
	room = max(room, 3)
	*scroll = min(*scroll, max(len(lines)-room, 0))
	visible := append([]string(nil), lines[*scroll:min(len(lines), *scroll+room)]...)
	if *scroll+room < len(lines) {
		visible[len(visible)-1] = ui.Muted.Render(fmt.Sprintf("… %d more (pgdn)", len(lines)-*scroll-room+1))
	}
	return visible
}

// compactPath shortens the IDs in an API path, so the kind of call stays
// readable in a narrow column: /payments/getByPayId/920e…
func compactPath(raw string) string {
	parts := strings.Split(shortPath(raw), "/")
	for i, p := range parts {
		if len(p) > 12 {
			parts[i] = p[:4] + "…"
		}
	}
	return strings.Join(parts, "/")
}

// copyExchangeCurl rebuilds a logged request as curl. The key is referenced
// as $PAYGATE_API_KEY; masked card data stays masked.
func copyExchangeCurl(ctx *Context, e reqlog.Entry) tea.Cmd {
	p := &paygate.Prepared{Method: e.Method, URL: e.URL, IdempotencyKey: e.RequestHeaders["Idempotency-Key"]}
	if e.RequestBody != "" && !strings.Contains(e.URL, "/oauth/token") {
		p.Body = []byte(ui.PrettyJSON([]byte(e.RequestBody)))
	}
	client := paygate.New(originOf(e.URL), "", "", paygate.AuthBasic)
	if ctx.Session != nil {
		client.MerchantID, client.Auth = ctx.Session.Profile.MerchantID, ctx.Session.Client.Auth
	}
	if err := clipboard.WriteAll(export.Curl(p, client, false)); err != nil {
		return toast(err.Error(), ui.ToneDanger)
	}
	if strings.Contains(e.RequestBody, "••") {
		return toast(t("insp.masked"), ui.ToneWarning)
	}
	return toast(t("copied.curl"), ui.ToneSuccess)
}

// copyExchangeBody puts one side's body on the clipboard, pretty printed.
func copyExchangeBody(e reqlog.Entry, response bool) tea.Cmd {
	body := e.RequestBody
	if response {
		body = e.ResponseBody
	}
	if err := clipboard.WriteAll(ui.PrettyJSON([]byte(body))); err != nil {
		return toast(err.Error(), ui.ToneDanger)
	}
	return toast(t("copied.body"), ui.ToneSuccess)
}
