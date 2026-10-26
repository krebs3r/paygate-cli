package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/history"
	"github.com/krebs3r/paygate-cli/internal/listen"
	"github.com/krebs3r/paygate-cli/internal/money"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/reqlog"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

// The payment detail lists the payment's API calls from the request log and
// its webhooks. Enter shows a call with its request and response. Calls the
// log no longer holds still appear from the payment's history.

// callRow is one line of that list: a logged exchange, a webhook or a
// history event whose request has left the log.
type callRow struct {
	at    time.Time
	entry *reqlog.Entry
	hook  *listen.Event
	event *history.Event
}

// loadCalls collects the payment's calls and webhooks, oldest first. The
// selection stays on the newest row while it was there.
func (s *paymentsScreen) loadCalls() {
	entries, _ := reqlog.Load()
	payIDs := []string{s.rec.PayID}
	if s.payment != nil {
		payIDs = append(payIDs, s.payment.PayID)
	}
	logged := reqlog.ForPayment(entries, s.rec.Profile, s.rec.TransID, payIDs...)
	rows := make([]callRow, 0, len(logged)+len(s.hooks))
	for i := range logged {
		rows = append(rows, callRow{at: logged[i].Time, entry: &logged[i]})
	}
	for i := range s.hooks {
		rows = append(rows, callRow{at: s.hooks[i].Time, hook: &s.hooks[i]})
	}
	s.callsGone = false
	for _, ev := range s.rec.Events {
		if !inLog(ev, logged) {
			ev := ev
			rows = append(rows, callRow{at: ev.Time, event: &ev})
			s.callsGone = true
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].at.Before(rows[j].at) })
	newest := len(s.calls) == 0 || s.callSel == len(s.calls)-1
	s.calls = rows
	if newest {
		s.callSel = len(rows) - 1
	}
	s.callSel = clamp(s.callSel, len(rows))
}

// callKeys handles the keys of the call list and of an opened call.
func (s *paymentsScreen) callKeys(ctx *Context, k string) (bool, tea.Cmd) {
	if s.callOpen {
		e := s.calls[s.callSel].entry
		switch k {
		case "[":
			s.stepCall(-1)
		case "]":
			s.stepCall(1)
		case "v":
			s.callResp, s.callScroll = !s.callResp, 0
		case "down":
			s.callScroll++ // the view stops at the end
		case "up":
			s.callScroll = max(0, s.callScroll-1)
		case "pgdown", " ", "ctrl+d":
			s.callScroll += 10
		case "pgup", "ctrl+u":
			s.callScroll = max(0, s.callScroll-10)
		case "c":
			return true, copyExchangeCurl(ctx, *e)
		case "y":
			return true, copyExchangeBody(*e, s.callResp)
		}
		return true, nil // the payment's own keys wait until the call is closed
	}
	if s.showJSON || len(s.calls) == 0 {
		return false, nil
	}
	switch k {
	case "up":
		s.callSel = clamp(s.callSel-1, len(s.calls))
	case "down":
		s.callSel = clamp(s.callSel+1, len(s.calls))
	case "enter":
		row := s.calls[s.callSel]
		switch {
		case row.hook != nil:
			return true, switchTo("webhooks", *row.hook)
		case row.event != nil:
			return true, toast(t("call.gone"), ui.ToneWarning)
		}
		s.callOpen, s.callResp, s.callScroll = true, true, 0
	default:
		return false, nil
	}
	return true, nil
}

// stepCall moves to the previous or next logged call, past webhooks.
func (s *paymentsScreen) stepCall(dir int) {
	for i := s.callSel + dir; i >= 0 && i < len(s.calls); i += dir {
		if s.calls[i].entry != nil {
			s.callSel, s.callScroll = i, 0
			return
		}
	}
}

// callsView is the list of calls below the payment card, scrolled so the
// selected one stays in view.
func (s *paymentsScreen) callsView(ctx *Context, w, h int) string {
	n := 0
	for _, r := range s.calls {
		if r.entry != nil {
			n++
		}
	}
	out := section(t("pays.timeline")) + "  " + ui.Muted.Render(fmt.Sprintf(t("pays.ncalls"), n)) + "\n"
	room := h - 1
	if s.callsGone {
		room-- // the note about calls that left the log
	}
	room = max(room, 1)
	s.callOffset = min(s.callOffset, s.callSel)
	if s.callSel >= s.callOffset+room {
		s.callOffset = s.callSel - room + 1
	}
	s.callOffset = max(min(s.callOffset, len(s.calls)-room), 0)
	// The date only when some call is from another day.
	stamp, now := "15:04:05", time.Now()
	for _, r := range s.calls {
		if y, mo, d := r.at.Local().Date(); y != now.Year() || mo != now.Month() || d != now.Day() {
			stamp = "02.01. 15:04"
		}
	}
	var lines []string
	for i := s.callOffset; i < len(s.calls) && i < s.callOffset+room; i++ {
		lines = append(lines, selectable(ctx, callLine(s.calls[i], s.rec.Flow, s.rec.Currency, stamp, w-2), i == s.callSel, w))
	}
	out += strings.Join(lines, "\n")
	if s.callsGone {
		out += "\n" + ui.Muted.Render(truncate(fmt.Sprintf(t("pays.nocalls"), reqlog.MaxEntries), w))
	}
	return out
}

// inLog reports whether the request log still holds the call behind a
// history event: a request of the same kind around the same time. Folded
// lookups cover the whole time from their first to their last.
func inLog(ev history.Event, logged []reqlog.Entry) bool {
	for _, e := range logged {
		from := e.Time
		if !e.First.IsZero() {
			from = e.First
		}
		if callKind(e) == ev.Kind && !ev.Time.Before(from.Add(-5*time.Second)) && !ev.Time.After(e.Time.Add(65*time.Second)) {
			return true
		}
	}
	return false
}

// callKind names a logged call like the payment history does.
func callKind(e reqlog.Entry) string {
	path := shortPath(e.URL)
	switch {
	case e.Method == http.MethodGet:
		return "status"
	case strings.HasSuffix(path, "/captures"):
		return "capture"
	case strings.HasSuffix(path, "/refunds"):
		return "refund"
	case strings.HasSuffix(path, "/reversals"):
		return "cancel"
	case path == "/payments", path == "/payments/sessions", path == "/payments/links":
		return "create"
	}
	return ""
}

// callLine renders a call: time, method, path, HTTP status, duration and
// what it did.
func callLine(r callRow, flow, currency, stamp string, w int) string {
	switch {
	case r.hook != nil:
		return ansi.Truncate(hookTimelineRow(*r.hook), w, "…")
	case r.event != nil:
		return ansi.Truncate(timelineRow(*r.event, currency)+ui.Muted.Render("  · "+t("call.notinlog")), w, "…")
	}
	e := *r.entry
	method := lipgloss.NewStyle().Foreground(ui.ColAccent).Render(fmt.Sprintf("%-5s", e.Method))
	repeat := ""
	if e.Repeat > 0 {
		repeat = fmt.Sprintf(" ×%d", e.Repeat+1)
	}
	stampText := ui.Muted.Render(e.Time.Local().Format(stamp))
	if w < 90 {
		// Narrow: the name of the call instead of its path, no duration.
		return ansi.Truncate(stampText+"  "+method+" "+padCells(truncate(pathLabel(e.URL)+repeat, 16), 16)+" "+
			statusText(e)+"  "+callSummary(e, flow), w, "…")
	}
	// Time, gaps, method, status and duration take len(stamp)+22 cells, the
	// summary keeps at least 28; the path needs no more than 34.
	pathW := min(max(w-len(stamp)-22-28, 16), 34)
	return ansi.Truncate(stampText+"  "+method+" "+padCells(truncate(compactPath(e.URL)+repeat, pathW), pathW)+" "+
		statusText(e)+" "+ui.Muted.Render(fmt.Sprintf("%7s", paygate.FormatDuration(msDuration(e.DurationMs))))+"  "+
		callSummary(e, flow), w, "…")
}

// pathLabel names a call by the last part of its path that is no ID:
// getByTransId, refunds, links.
func pathLabel(raw string) string {
	parts := strings.Split(shortPath(raw), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if p := parts[i]; p != "" && len(p) <= 12 {
			return p
		}
	}
	return shortPath(raw)
}

// callSummary says in a few words what a call did and what Paygate answered.
func callSummary(e reqlog.Entry, flow string) string {
	path := shortPath(e.URL)
	what := e.Method
	switch {
	case e.Method == http.MethodPost && path == "/payments/links":
		what = t("call.link")
	case e.Method == http.MethodPost && path == "/payments/sessions":
		what = t("call.session")
	case e.Method == http.MethodPost && path == "/payments":
		what = t("call.payment")
	case strings.HasSuffix(path, "/captures"):
		what = t("call.capture") + operationAmount(e)
	case strings.HasSuffix(path, "/refunds"):
		what = t("call.refund") + operationAmount(e)
	case strings.HasSuffix(path, "/reversals"):
		what = t("call.reversal") + operationAmount(e)
	case e.Method == http.MethodGet:
		what = t("call.status")
	}
	if e.Error != "" {
		return what + " " + ui.Danger.Render("ERR")
	}
	var p paygate.Payment
	if json.Unmarshal([]byte(e.ResponseBody), &p) != nil || p.Status == "" {
		return what
	}
	if e.Method == http.MethodGet && core.WaitOutcome(core.Flow(flow), &p) == paygate.OutcomePending && p.Method().Type == "" {
		return what + " " + ui.Muted.Render(t("call.open"))
	}
	style := ui.Warning
	switch paygate.OutcomeOf(&p) {
	case paygate.OutcomeSuccess:
		style = ui.Success
	case paygate.OutcomeFailed:
		style = ui.Danger
	}
	line := what + " " + style.Render(p.Status)
	if m := p.Method().Type; m != "" {
		line += ui.Muted.Render(" · " + m)
	}
	if paygate.OutcomeOf(&p) == paygate.OutcomeFailed && p.ResponseDescription != "" {
		line += ui.Muted.Render(" · " + p.ResponseDescription)
	}
	return line
}

// operationAmount is the amount a capture, refund or reversal asked for.
func operationAmount(e reqlog.Entry) string {
	var r paygate.OperationRequest
	if json.Unmarshal([]byte(e.RequestBody), &r) != nil || r.Amount.Currency == "" {
		return ""
	}
	return " " + money.Format(r.Amount.Value, r.Amount.Currency)
}

// callView shows the selected call: request and response side by side on a
// wide screen, otherwise one of them (v switches). Scrolling moves the active
// side.
func (s *paymentsScreen) callView(w, h int) string {
	e := *s.calls[s.callSel].entry
	pos, n := 0, 0
	for i, r := range s.calls {
		if r.entry != nil {
			n++
			if i == s.callSel {
				pos = n
			}
		}
	}
	right := ui.Muted.Render(fmt.Sprintf(t("call.of"), pos, n) + "  [ ]")
	crumb := ui.Muted.Render("← "+t("pays.title")+" / "+s.rec.TransID+" / ") + ui.Bold.Render(e.Method+" "+compactPath(e.URL))
	if lipgloss.Width(crumb)+lipgloss.Width(right)+1 > w {
		crumb = ui.Muted.Render("← "+s.rec.TransID+" / ") + ui.Bold.Render(e.Method+" "+pathLabel(e.URL))
	}
	out := spread(crumb, right, w) + "\n\n" +
		strings.Join(exchangeHead(e, w), "\n") + "\n"
	room := h - lipgloss.Height(out) - 1
	tab := func(response bool) string {
		label, tone := t("insp.request"), ui.ToneNeutral
		if response {
			label = t("insp.response")
		}
		if response == s.callResp {
			tone = ui.ToneAccent
		}
		return ui.Badge(label, tone)
	}
	side := func(response bool, w int) string {
		lines := exchangeLines(e, response, w)
		if response == s.callResp {
			return strings.Join(scrollWindow(lines, &s.callScroll, room), "\n")
		}
		top := 0
		return strings.Join(scrollWindow(lines, &top, room), "\n")
	}
	if w >= 100 {
		colW := (w - 3) / 2
		left := fit(tab(false)+"\n"+side(false, colW), colW, room+1)
		right := fit(tab(true)+"  "+ui.Muted.Render("v")+"\n"+side(true, colW), colW, room+1)
		sep := ui.Muted.Render(strings.TrimSuffix(strings.Repeat("│\n", room+1), "\n"))
		return out + "\n" + lipgloss.JoinHorizontal(lipgloss.Top, left, " ", sep, " ", right)
	}
	return out + "\n" + tab(false) + " " + tab(true) + "  " + ui.Muted.Render("v") + "\n" + side(s.callResp, w)
}
