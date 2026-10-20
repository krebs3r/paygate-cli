package tui

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/krebs3r/paygate-cli/internal/export"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/reqlog"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

type inspectorScreen struct {
	entries  []reqlog.Entry
	sel      int
	offset   int
	response bool // show the response instead of the request
	scroll   int
	form     *huh.Form
	confirm  bool
	width    int
	err      error
}

func newInspectorScreen() *inspectorScreen { return &inspectorScreen{response: true, width: 80} }

func (s *inspectorScreen) ID() string       { return "inspect" }
func (s *inspectorScreen) Title() string    { return t("menu.inspect") }
func (s *inspectorScreen) Shortcut() string { return "i" }
func (s *inspectorScreen) Capturing() bool  { return s.form != nil }
func (s *inspectorScreen) Back(*Context) (bool, tea.Cmd) {
	if s.form != nil {
		s.form = nil
		return true, nil
	}
	return false, nil
}

func (s *inspectorScreen) Activate(*Context) tea.Cmd {
	s.reload()
	return nil
}

func (s *inspectorScreen) reload() {
	s.entries, s.err = reqlog.Load()
	s.sel = clamp(s.sel, len(s.entries))
}

func (s *inspectorScreen) Hints(*Context) []Hint {
	if s.form != nil {
		return []Hint{{"← →", t("key.select")}, {"enter", t("key.open")}, {"esc", t("key.cancel")}}
	}
	if len(s.entries) == 0 {
		return nil
	}
	return []Hint{{"↑↓", t("key.select")}, {"v", t("key.view")}, {"pgup pgdn", t("key.scroll")},
		{"y", t("key.copybody")}, {"c", t("key.curl")}, {"r", t("key.refresh")}, {"x", t("key.clear")}}
}

func (s *inspectorScreen) Update(ctx *Context, msg tea.Msg) tea.Cmd {
	switch msg.(type) {
	case payResultMsg, pollResultMsg, opResultMsg, paysOpDoneMsg, detailLoadedMsg, lookupDoneMsg:
		s.reload()
		return nil
	}
	if s.form != nil {
		f, cmd := s.form.Update(msg)
		s.form = f.(*huh.Form)
		switch s.form.State {
		case huh.StateAborted:
			s.form = nil
		case huh.StateCompleted:
			s.form = nil
			if s.confirm {
				if err := reqlog.Clear(); err != nil {
					return toast(err.Error(), ui.ToneDanger)
				}
				s.reload()
				return toast(t("insp.cleared"), ui.ToneSuccess)
			}
		}
		return cmd
	}
	km, ok := msg.(tea.KeyMsg)
	if !ok || len(s.entries) == 0 {
		if ok && km.String() == "r" {
			s.reload()
		}
		return nil
	}
	switch km.String() {
	case "up", "k":
		s.sel, s.scroll = clamp(s.sel-1, len(s.entries)), 0
	case "down", "j":
		s.sel, s.scroll = clamp(s.sel+1, len(s.entries)), 0
	case "v":
		s.response, s.scroll = !s.response, 0
	case "pgdown", " ", "ctrl+d":
		s.scroll += 10
	case "pgup", "ctrl+u":
		s.scroll = max(0, s.scroll-10)
	case "r":
		s.reload()
	case "y":
		e := s.entries[s.sel]
		body := e.RequestBody
		if s.response {
			body = e.ResponseBody
		}
		if err := clipboard.WriteAll(ui.PrettyJSON([]byte(body))); err != nil {
			return toast(err.Error(), ui.ToneDanger)
		}
		return toast(t("copied.body"), ui.ToneSuccess)
	case "c":
		return s.copyCurl(ctx)
	case "x":
		s.confirm = false
		s.form = newForm(s.width, huh.NewGroup(huh.NewConfirm().
			Title(fmt.Sprintf(t("insp.clear.q"), len(s.entries))).
			Affirmative(t("yes")).Negative(t("no")).Value(&s.confirm)))
		return s.form.Init()
	}
	return nil
}

// copyCurl rebuilds the selected request as curl. The key is referenced as
// $PAYGATE_API_KEY; masked card data stays masked.
func (s *inspectorScreen) copyCurl(ctx *Context) tea.Cmd {
	e := s.entries[s.sel]
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

func originOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.Scheme + "://" + u.Host
}

func shortPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return strings.TrimPrefix(u.Path, "/api/v2")
}

// ---- view -------------------------------------------------------------------

func (s *inspectorScreen) View(ctx *Context, w, h int) string {
	s.width = w
	out := title(t("insp.title")) + "  " + ui.Muted.Render(fmt.Sprintf(t("insp.count"), len(s.entries))) + "\n"
	out += ui.Muted.Render(t("insp.redacted")) + "\n\n"
	if s.form != nil {
		return out + s.form.View()
	}
	if s.err != nil {
		out += ui.Danger.Render(s.err.Error()) + "\n"
	}
	if len(s.entries) == 0 {
		return out + ui.Label.Render(wrap(t("insp.empty"), min(w, 72)))
	}
	avail := h - lipgloss.Height(out)
	if w >= 104 {
		listW := 46
		list := s.listView(ctx, listW, avail)
		detail := s.detailView(w-listW-3, avail)
		sep := ui.Muted.Render(strings.TrimSuffix(strings.Repeat("│\n", avail), "\n"))
		return out + lipgloss.JoinHorizontal(lipgloss.Top, fit(list, listW, avail), " ", sep, " ", detail)
	}
	listH := min(8, max(3, avail/3))
	return out + fit(s.listView(ctx, w, listH), w, listH) + "\n" + ui.Muted.Render(strings.Repeat("─", w)) + "\n" + s.detailView(w, avail-listH-1)
}

func (s *inspectorScreen) listView(ctx *Context, w, h int) string {
	if s.sel < s.offset {
		s.offset = s.sel
	}
	if s.sel >= s.offset+h {
		s.offset = s.sel - h + 1
	}
	var lines []string
	for i := s.offset; i < len(s.entries) && i < s.offset+h; i++ {
		e := s.entries[i]
		path := truncate(shortPath(e.URL), max(w-30, 8))
		row := ui.Muted.Render(e.Time.Local().Format("15:04:05")) + " " +
			lipgloss.NewStyle().Foreground(ui.ColAccent).Render(fmt.Sprintf("%-5s", e.Method)) + " " +
			padCells(path, max(w-30, 8)) + " " + statusText(e) + " " +
			ui.Muted.Render(fmt.Sprintf("%7s", paygate.FormatDuration(msDuration(e.DurationMs))))
		lines = append(lines, selectable(ctx, row, i == s.sel, w))
	}
	return strings.Join(lines, "\n")
}

func statusText(e reqlog.Entry) string {
	switch {
	case e.Error != "":
		return ui.Danger.Render("ERR")
	case e.Status >= 500:
		return ui.Danger.Render(fmt.Sprint(e.Status))
	case e.Status >= 400:
		return ui.Warning.Render(fmt.Sprint(e.Status))
	default:
		return ui.Success.Render(fmt.Sprint(e.Status))
	}
}

func (s *inspectorScreen) detailView(w, h int) string {
	e := s.entries[s.sel]
	tabs := ui.Badge(t("insp.request"), ui.ToneNeutral) + " " + ui.Badge(t("insp.response"), ui.ToneAccent)
	if !s.response {
		tabs = ui.Badge(t("insp.request"), ui.ToneAccent) + " " + ui.Badge(t("insp.response"), ui.ToneNeutral)
	}
	head := []string{
		tabs + "  " + ui.Muted.Render("v"),
		lipgloss.NewStyle().Foreground(ui.ColAccent).Render(e.Method) + " " + ui.Label.Render(truncate(e.URL, max(w-8, 10))),
		statusText(e) + ui.Muted.Render(fmt.Sprintf(" · %s · %s", paygate.FormatDuration(msDuration(e.DurationMs)), e.Time.Local().Format("02.01. 15:04:05"))),
	}
	if e.CorrelationID != "" {
		head = append(head, ui.Muted.Render("correlation "+e.CorrelationID))
	}
	if e.Profile != "" {
		head = append(head, ui.Muted.Render("profile "+e.Profile))
	}
	headers, body := e.RequestHeaders, e.RequestBody
	if s.response {
		headers, body = e.ResponseHeaders, e.ResponseBody
	}
	var lines []string
	lines = append(lines, "")
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
	case e.Error != "" && s.response:
		lines = append(lines, ui.Danger.Render(t("insp.error")+": ")+ui.Label.Render(wrap(e.Error, w)))
	case strings.TrimSpace(body) == "":
		lines = append(lines, ui.Muted.Render(t("insp.nobody")))
	default:
		lines = append(lines, strings.Split(ui.HighlightJSON([]byte(body)), "\n")...)
	}
	room := max(h-len(head), 3)
	maxScroll := max(len(lines)-room, 0)
	if s.scroll > maxScroll {
		s.scroll = maxScroll
	}
	visible := lines[s.scroll:min(len(lines), s.scroll+room)]
	if s.scroll+room < len(lines) {
		visible[len(visible)-1] = ui.Muted.Render(fmt.Sprintf("… %d more (pgdn)", len(lines)-s.scroll-room+1))
	}
	return fit(strings.Join(head, "\n")+"\n"+strings.Join(visible, "\n"), w, h)
}

func interestingHeader(k string) bool {
	switch strings.ToLower(k) {
	case "content-type", "authorization", "idempotency-key", "user-agent", "x-correlation-id", "www-authenticate", "location":
		return true
	}
	return false
}

func msDuration(ms int64) time.Duration { return time.Duration(ms) * time.Millisecond }
