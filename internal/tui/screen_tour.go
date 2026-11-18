package tui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/export"
	"github.com/krebs3r/paygate-cli/internal/tour"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

// Panes below the diagram of a tour step.
const (
	paneRequest = iota
	paneResponse
	paneCurl
)

// tourScreen lists the integration tours and walks through one of them,
// step by step, in focus mode.
type tourScreen struct {
	tours []*tour.Tour
	sel   int
	msg   string // why a tour did not start

	run     *tour.Run
	plan    *tour.Planned // request of the current step, built before it runs
	planErr error
	busy    bool
	cancel  context.CancelFunc
	waiting string // state of a step that waits
	spin    spinner.Model
	pane    int
	scroll  int  // the step scrolls as a whole below its title and step bar
	docArea area // where the documentation link is drawn, for clicks

	// Where the pane started and how many lines fitted at the last view,
	// so the answer can be brought into view after a step ran.
	paneStart, room int

	// form asks for the format of the protocol (x, and when the tour is
	// finished); format is its answer. finishing: the tour ends after it.
	form      *huh.Form
	format    string
	finishing bool
}

func newTourScreen() *tourScreen {
	return &tourScreen{tours: tour.All(), spin: newSpinner()}
}

func (s *tourScreen) ID() string                { return "tour" }
func (s *tourScreen) Title() string             { return t("menu.tour") }
func (s *tourScreen) Shortcut() string          { return "t" }
func (s *tourScreen) Activate(*Context) tea.Cmd { return nil }

// Focus hides the menu while a tour runs: diagram and request need the
// width, also in a screen share.
func (s *tourScreen) Focus(*Context) bool { return s.run != nil }

// Capturing keeps the keys in the tour while it runs: ← → move between the
// steps, esc leaves the tour.
func (s *tourScreen) Capturing() bool { return s.run != nil }

func (s *tourScreen) Back(*Context) (bool, tea.Cmd) {
	if s.run != nil {
		s.leave()
		return true, nil
	}
	return false, nil
}

func (s *tourScreen) Hints(ctx *Context) []Hint {
	if s.run == nil {
		return []Hint{{"↑↓", t("key.select")}, {"enter", t("tour.start")}}
	}
	if s.form != nil && s.finishing {
		return []Hint{{"↑↓", t("key.select")}, {"enter", t("form.confirm")}, {"esc", t("tour.export.back")}}
	}
	if s.form != nil {
		return []Hint{{"↑↓", t("key.select")}, {"enter", t("tour.export.save")}, {"esc", t("key.cancel")}}
	}
	if s.busy {
		hints := []Hint{{"esc", t("tour.stopwait")}, {"d", t("tour.doc.key")}}
		if s.run.Redirect != "" {
			hints = append(hints, Hint{"o", t("tour.browser")})
		}
		return hints
	}
	enter := t("tour.exec")
	switch {
	case s.run.Finished() && s.run.Step == len(s.run.Tour.Steps)-1:
		enter = t("tour.finish")
	case s.run.Done(s.run.Step):
		enter = t("tour.next")
	}
	hints := []Hint{{"enter", enter}, {"← →", t("tour.step")}, {"v", t("tour.pane")}, {"y", t("tour.copy")}, {"c", "curl"}, {"d", t("tour.doc.key")}}
	if s.run.Redirect != "" {
		hints = append(hints, Hint{"o", t("tour.browser")})
	}
	return append(hints, Hint{"x", t("tour.export.key")}, Hint{"esc", t("tour.leave")})
}

// tourStepMsg carries the outcome of a step run in the background.
type tourStepMsg struct {
	run *tour.Run
	o   *tour.Outcome
}

// tourAgainMsg runs a waiting step again.
type tourAgainMsg struct{ run *tour.Run }

func (s *tourScreen) Update(ctx *Context, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		if !s.busy {
			return nil
		}
		var cmd tea.Cmd
		s.spin, cmd = s.spin.Update(msg)
		return cmd
	case tourStepMsg:
		return s.stepDone(msg)
	case tourAgainMsg:
		if msg.run != s.run || !s.busy {
			return nil
		}
		return s.exec()
	case sessionChangedMsg:
		// Another profile or the end of the demo: the tour ran against the
		// old one.
		s.leave()
		return nil
	}
	if s.form != nil {
		f, cmd := s.form.Update(msg)
		s.form = f.(*huh.Form)
		switch s.form.State {
		case huh.StateAborted:
			// esc goes back to the tour, also when it was about to end.
			s.form, s.finishing = nil, false
		case huh.StateCompleted:
			s.form = nil
			finishing := s.finishing
			s.finishing = false
			if s.format == noLog {
				s.leave()
				return toast(t("tour.finished"), ui.ToneSuccess)
			}
			cmd, saved := s.saveProtocol(tour.Format(s.format))
			if finishing && saved {
				s.leave()
			}
			return cmd
		}
		return cmd
	}
	// A click on the documentation link opens it; the wheel scrolls.
	if mm, ok := msg.(tea.MouseMsg); ok {
		if s.run == nil || mm.Action != tea.MouseActionPress {
			return nil
		}
		switch mm.Button {
		case tea.MouseButtonLeft:
			if s.docArea.contains(mm.X-ctx.ContentX, mm.Y-ctx.ContentY) {
				return s.openDoc()
			}
		case tea.MouseButtonWheelUp:
			s.scroll = max(s.scroll-3, 0)
		case tea.MouseButtonWheelDown:
			s.scroll += 3
		}
		return nil
	}
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if s.run == nil {
		return s.updateList(ctx, km)
	}
	return s.updateRun(ctx, km)
}

// openDoc opens the documentation of the current step in the browser.
func (s *tourScreen) openDoc() tea.Cmd {
	u := s.run.Current().Doc.URL(s.run.Session.Profile.Brand)
	if u == "" {
		return nil
	}
	if err := tour.OpenURL(u); err != nil {
		return toast(err.Error(), ui.ToneDanger)
	}
	return toast(t("tour.doc.opened"), ui.ToneSuccess)
}

func (s *tourScreen) updateList(ctx *Context, km tea.KeyMsg) tea.Cmd {
	switch km.String() {
	case "up", "k":
		s.sel = clamp(s.sel-1, len(s.tours))
	case "down", "j":
		s.sel = clamp(s.sel+1, len(s.tours))
	case "enter":
		return s.start(ctx, s.tours[s.sel])
	}
	return nil
}

// start begins a tour with the active profile.
func (s *tourScreen) start(ctx *Context, tr *tour.Tour) tea.Cmd {
	if ctx.Session == nil {
		s.msg = t("tour.nosession")
		return nil
	}
	r, err := tour.Start(tour.Find(tr.ID), ctx.Session, ctx.Version)
	switch {
	case errors.Is(err, tour.ErrProduction):
		s.msg = t("tour.prod")
		return nil
	case errors.Is(err, tour.ErrNoWebhooks):
		s.msg = t("tour.nohooks")
		return nil
	case err != nil:
		s.msg = err.Error()
		return nil
	}
	s.msg, s.run, s.busy, s.waiting = "", r, false, ""
	// "Tour finished" of the last tour must not stand over the new one.
	return tea.Batch(dismissToast, s.enterStep())
}

// leave ends the tour and goes back to the list.
func (s *tourScreen) leave() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.run != nil {
		s.run.Close()
	}
	s.run, s.plan, s.planErr, s.busy, s.waiting, s.form, s.finishing = nil, nil, nil, false, "", nil, false
}

// enterStep prepares the view of the current step: its request, if it
// sends one, and the pane to show. A step that waits for the customer or a
// webhook starts waiting at once, so a payment in the browser shows up
// without a key press.
func (s *tourScreen) enterStep() tea.Cmd {
	s.plan, s.planErr, s.scroll, s.waiting = nil, nil, 0, ""
	if s.run.Done(s.run.Step) {
		s.pane = paneResponse
		return nil
	}
	s.pane = paneRequest
	st := s.run.Current()
	switch {
	case st.Kind == tour.Wait || st.Auto:
		s.pane = paneResponse
		return s.runStep()
	case st.Kind == tour.Request && st.Skip == nil:
		s.plan, s.planErr = s.run.Plan()
	case st.Kind == tour.Request:
		// A step that may be skipped is planned when it runs.
		if why := st.Skip(s.run).String(); why == "" {
			s.plan, s.planErr = s.run.Plan()
		}
	}
	return nil
}

// runStep runs the current step in the background.
func (s *tourScreen) runStep() tea.Cmd {
	s.busy = true
	c, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	return tea.Batch(s.execWith(c), s.spin.Tick)
}

func (s *tourScreen) updateRun(ctx *Context, km tea.KeyMsg) tea.Cmd {
	r := s.run
	if s.busy {
		// While a step waits, only esc stops it; looking around still works.
		switch km.String() {
		case "esc":
			if s.cancel != nil {
				s.cancel()
			}
			s.busy, s.waiting = false, ""
			return nil
		case "enter", "left", "right":
			return nil
		}
	}
	switch km.String() {
	case "esc":
		s.leave()
	case "enter":
		switch {
		case r.Done(r.Step) && r.Step == len(r.Tour.Steps)-1:
			// Leaving drops the run: offer to keep its log first.
			return s.askProtocol(ctx.Width, true)
		case r.Done(r.Step):
			if r.Go(r.Step + 1) {
				return s.enterStep()
			}
		default:
			return s.runStep()
		}
	case "right":
		if r.Go(r.Step + 1) {
			return s.enterStep()
		}
	case "left":
		if r.Go(r.Step - 1) {
			return s.enterStep()
		}
	case "v":
		s.pane = (s.pane + 1) % 3
		s.showPane()
	case "d":
		return s.openDoc()
	case "y":
		return s.copyPane()
	case "m":
		return toggleMouse
	case "up", "k":
		s.scroll = max(s.scroll-1, 0)
	case "down", "j":
		s.scroll++
	case "pgup":
		s.scroll = max(s.scroll-10, 0)
	case "pgdown":
		s.scroll += 10
	case "c":
		if curl := s.curl(); curl != "" {
			if err := clipboard.WriteAll(curl); err != nil {
				return toast(err.Error(), ui.ToneDanger)
			}
			return toast(t("copied.curl"), ui.ToneSuccess)
		}
	case "o":
		if r.Redirect != "" {
			if err := tour.OpenURL(r.Redirect); err != nil {
				return toast(err.Error(), ui.ToneDanger)
			}
		}
	case "x":
		return s.askProtocol(ctx.Width, false)
	}
	return nil
}

// noLog is the answer "don't save" when the tour ends.
const noLog = "none"

// askProtocol asks for the format of the protocol of the run. When the
// tour is finishing, it also offers to end it without saving.
func (s *tourScreen) askProtocol(w int, finishing bool) tea.Cmd {
	if !s.run.Ran() {
		return toast(t("tour.export.empty"), ui.ToneWarning)
	}
	dir, _ := os.Getwd()
	s.format, s.finishing = string(tour.HTML), finishing
	opts := []huh.Option[string]{
		huh.NewOption(t("tour.export.html"), string(tour.HTML)),
		huh.NewOption(t("tour.export.md"), string(tour.Markdown)),
	}
	title := t("tour.export.title")
	if finishing {
		title = t("tour.export.finish")
		opts = append(opts, huh.NewOption(t("tour.export.none"), noLog))
	}
	sel := huh.NewSelect[string]().
		Title(title).
		Description(fmt.Sprintf(t("tour.export.d"), config.Tilde(dir))).
		Options(opts...).
		Value(&s.format)
	s.form = newForm(w, huh.NewGroup(sel))
	return s.form.Init()
}

// saveProtocol writes the protocol of the run to the working directory,
// under a name that is not taken yet, and says whether it worked.
func (s *tourScreen) saveProtocol(f tour.Format) (tea.Cmd, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return toast(err.Error(), ui.ToneDanger), false
	}
	at := time.Now()
	data := tour.Export(s.run, f, at)
	name := tour.FileName(s.run, f, at)
	for i := 2; ; i++ {
		path := filepath.Join(dir, name)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		switch {
		case errors.Is(err, os.ErrExist) && i < 100:
			name = strings.TrimSuffix(tour.FileName(s.run, f, at), "."+string(f)) + fmt.Sprintf("-%d.%s", i, f)
			continue
		case err != nil:
			return toast(err.Error(), ui.ToneDanger), false
		}
		_, err = file.Write(data)
		if cerr := file.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return toast(err.Error(), ui.ToneDanger), false
		}
		// The folder was named when asking; a long path shows the name only.
		shown := config.Tilde(path)
		if len([]rune(shown)) > 70 {
			shown = name
		}
		return toast(fmt.Sprintf(t("tour.export.saved"), shown), ui.ToneSuccess), true
	}
}

// exec runs the current step again, with the context of the first try.
func (s *tourScreen) exec() tea.Cmd {
	c := context.Background()
	if s.cancel == nil {
		var cancel context.CancelFunc
		c, cancel = context.WithCancel(c)
		s.cancel = cancel
	}
	return s.execWith(c)
}

func (s *tourScreen) execWith(c context.Context) tea.Cmd {
	r := s.run
	return func() tea.Msg { return tourStepMsg{run: r, o: r.Exec(c)} }
}

func (s *tourScreen) stepDone(msg tourStepMsg) tea.Cmd {
	if msg.run != s.run || !s.busy {
		return nil // left the tour or stopped waiting meanwhile
	}
	o := msg.o
	if o != nil && o.Pending {
		s.waiting = o.Status
		run := s.run
		return tea.Tick(time.Second, func(time.Time) tea.Msg { return tourAgainMsg{run: run} })
	}
	s.busy, s.waiting, s.cancel = false, "", nil
	s.pane = paneResponse
	s.showPane()
	// E.g. the payment page is open: wait for the customer right away.
	if s.run.Current().Next && o != nil && o.Err == nil && s.run.Go(s.run.Step+1) {
		return s.enterStep()
	}
	return nil
}

// showPane scrolls so that the pane is in view, as far as the window allows
// without hiding the step's title.
func (s *tourScreen) showPane() {
	s.scroll = max(0, min(s.paneStart, s.paneStart+10-s.room))
}

// copyPane copies what the pane shows as plain text: the request body, the
// answer or the curl command.
func (s *tourScreen) copyPane() tea.Cmd {
	text, what := "", ""
	o := s.run.Outcome(s.run.Step)
	var x *tour.Exchange
	if o != nil {
		x = o.Exchange
	}
	switch s.pane {
	case paneCurl:
		text, what = s.curl(), t("copied.curl")
	case paneRequest:
		switch {
		case x != nil:
			text = plainRequest(x.Method, x.URL, x.Body)
		case s.plan != nil:
			text = plainRequest(s.plan.Prepared.Method, s.plan.Prepared.URL, s.plan.Prepared.Body)
		}
		what = t("tour.copied.request")
	default:
		switch {
		case x != nil && len(x.Response) > 0:
			text = ui.PrettyJSON(x.Response)
		case o != nil && o.Webhook != nil:
			text = ui.PrettyJSON(o.Webhook.Body)
		case o != nil && len(o.Lines) > 0:
			text = ansi.Strip(strings.Join(o.Lines, "\n"))
		}
		what = t("tour.copied.response")
	}
	if text == "" {
		return toast(t("tour.nothingtocopy"), ui.ToneWarning)
	}
	if err := copyText(text); err != nil {
		return toast(err.Error(), ui.ToneDanger)
	}
	return toast(what, ui.ToneSuccess)
}

// copyText puts text on the clipboard; tests replace it.
var copyText = clipboard.WriteAll

// plainRequest is a request as text: the JSON body, or method and URL.
func plainRequest(method, url string, body []byte) string {
	if len(body) > 0 {
		return ui.PrettyJSON(body)
	}
	return method + " " + url
}

// curl is the request of the current step as a curl command.
func (s *tourScreen) curl() string {
	if o := s.run.Outcome(s.run.Step); o != nil && o.Exchange != nil {
		return o.Exchange.Curl
	}
	if s.plan != nil {
		return curlOf(s.run, s.plan)
	}
	return ""
}

func (s *tourScreen) View(ctx *Context, w, h int) string {
	if s.run == nil {
		return s.listView(ctx, w)
	}
	if s.form != nil {
		r := s.run
		head := spread(tourHead(r.Tour),
			ui.Muted.Render(fmt.Sprintf(t("tour.stepof"), r.Step+1, len(r.Tour.Steps))), w)
		return strings.Join([]string{head, s.stepBar(w), "", s.form.View()}, "\n")
	}
	return s.runView(w, h)
}

func (s *tourScreen) listView(ctx *Context, w int) string {
	out := title(t("tour.title")) + "\n" + ui.Label.Render(wrap(t("tour.intro"), min(w, 80))) + "\n\n"
	nameW := 0
	for _, tr := range s.tours {
		nameW = max(nameW, lipgloss.Width(tourName(tr)))
	}
	nameW = max(nameW, lipgloss.Width(t("tour.col.flow"))) + 3
	stepsW := lipgloss.Width(t("tour.col.steps")) + 3
	out += ui.Muted.Render("  "+padTo(t("tour.col.flow"), nameW)+padTo(t("tour.col.steps"), stepsW)+t("tour.col.shows")) + "\n"
	for i, tr := range s.tours {
		row := padTo(tourName(tr), nameW) + padTo(fmt.Sprintf("%d", len(tr.Steps)), stepsW) + ui.Label.Render(tr.Shows.String())
		out += selectable(ctx, ansi.Truncate(row, max(w-4, 10), "…"), i == s.sel, w) + "\n"
	}
	out += "\n" + section(t("tour.target")) + "  "
	switch {
	case ctx.Session == nil:
		out += ui.Muted.Render(t("no.profile")) + "\n" + ui.Label.Render(wrap(t("tour.nosession"), min(w, 80)))
	default:
		p := ctx.Session.Profile
		out += ui.EnvBadge(p.Env) + " " + ui.Label.Render(ctx.Session.Name) + "\n"
		switch {
		case p.IsProd():
			out += ui.Warning.Render("! ") + ui.Label.Render(wrap(t("tour.prod"), min(w-2, 80)))
		case !p.IsDemo() && !core.LocalOrigin(p):
			out += ui.Muted.Render(wrap(t("tour.real"), min(w, 80)))
		}
	}
	if s.msg != "" {
		out += "\n\n" + ui.Danger.Render("! ") + ui.Label.Render(wrap(s.msg, min(w-2, 80)))
	}
	return out
}

// tourName is a tour's title with the preview badge of a tour not yet
// verified against a real merchant account.
func tourName(tr *tour.Tour) string {
	if tr.Preview {
		return tr.Title.String() + " " + ui.Badge(t("dcc.badge"), ui.ToneAccent)
	}
	return tr.Title.String()
}

// tourHead is the title line of a running tour.
func tourHead(tr *tour.Tour) string {
	head := title(t("tour.title") + " · " + tr.Title.String())
	if tr.Preview {
		head += " " + ui.Badge(t("dcc.badge"), ui.ToneAccent)
	}
	return head
}

func padTo(s string, w int) string {
	return s + strings.Repeat(" ", max(w-lipgloss.Width(s), 1))
}

// runView draws a step: title and step bar stay on top; below them the
// diagram with the explanation beside it, then the request, the answer or
// curl. Everything below the step bar scrolls as one page (↑ ↓, wheel), so
// no text is cut off.
func (s *tourScreen) runView(w, h int) string {
	r := s.run
	head := spread(tourHead(r.Tour),
		ui.Muted.Render(fmt.Sprintf(t("tour.stepof"), r.Step+1, len(r.Tour.Steps))), w)
	header := []string{head, s.stepBar(w), ""}

	// Diagram and explanation side by side; in a narrow window the
	// explanation follows below the diagram. The documentation link sits
	// below the step's title.
	var body []string
	docRow, docX, docW := -1, 0, 0
	diagW := min(max(w*58/100, 56), 74)
	if explW := w - diagW - 4; explW >= 34 { // "  │ "
		diag := strings.Split(tour.Diagram(r.Tour, r.Step, diagW), "\n")
		expl, doc := s.explanation(explW)
		if doc >= 0 {
			docRow, docW = doc, lipgloss.Width(expl[doc])
		}
		for i := 0; i < max(len(diag), len(expl)); i++ {
			left, right := "", ""
			if i < len(diag) {
				left = diag[i]
			}
			if i < len(expl) {
				right = expl[i]
			}
			body = append(body, padTo(ansi.Truncate(left, diagW, ""), diagW)+"  "+lipgloss.NewStyle().Foreground(ui.ColBorder).Render("│")+" "+right)
		}
		docX = diagW + 4
	} else {
		body = append(body, strings.Split(tour.Diagram(r.Tour, r.Step, min(w, 74)), "\n")...)
		body = append(body, "")
		expl, doc := s.explanation(min(w, 100))
		if doc >= 0 {
			docRow, docW = len(body)+doc, lipgloss.Width(expl[doc])
		}
		body = append(body, expl...)
	}
	body = append(body, ui.Muted.Render(strings.Repeat("╌", w)))

	// Status and pane tabs share a line; the pane follows.
	tabs := s.paneTabs()
	body = append(body, spread(s.statusLine(w-lipgloss.Width(tabs)-2), tabs, w), "")
	s.paneStart = len(body)
	for _, l := range s.paneBody(w) {
		body = append(body, ansi.Truncate(l, w, "…"))
	}

	s.room = max(h-len(header), 3)
	s.scroll = min(s.scroll, max(len(body)-s.room, 0))
	end := min(s.scroll+s.room, len(body))
	visible := append([]string(nil), body[s.scroll:end]...)
	if end < len(body) {
		visible[len(visible)-1] = ui.Muted.Render(fmt.Sprintf(t("tour.more"), len(body)-end))
	}
	s.docArea = area{}
	if docRow >= s.scroll && docRow < end {
		s.docArea = area{x: docX, y: len(header) + docRow - s.scroll, w: docW, h: 1}
	}
	return strings.Join(append(header, visible...), "\n")
}

// stepBar lists the steps: done, current and still to come.
func (s *tourScreen) stepBar(w int) string {
	var parts []string
	for i, st := range s.run.Tour.Steps {
		label := fmt.Sprintf("%d %s", i+1, st.Short)
		switch {
		case i == s.run.Step:
			parts = append(parts, lipgloss.NewStyle().Foreground(ui.ColAccent2).Bold(true).Render("● "+label))
		case s.run.Done(i):
			parts = append(parts, ui.Success.Render("✓ ")+ui.Label.Render(label))
		default:
			parts = append(parts, ui.Muted.Render("○ "+label))
		}
	}
	return ansi.Truncate(strings.Join(parts, "   "), w, "…")
}

// explanation is the text column, w wide: title and kind, the link to the
// documentation and what happens in the step. It also says which line is
// the link (-1: none). A title too long for the kind beside it gets the
// kind on a line of its own.
func (s *tourScreen) explanation(w int) ([]string, int) {
	st := s.run.Current()
	title, badge := st.Title.String(), kindBadge(st.Kind)
	var out []string
	if lipgloss.Width(title)+2+lipgloss.Width(badge) <= w {
		out = append(out, ui.Bold.Render(title)+"  "+badge)
	} else {
		for _, l := range strings.Split(wrap(title, w), "\n") {
			out = append(out, ui.Bold.Render(strings.TrimRight(l, " ")))
		}
		out = append(out, badge)
	}
	doc := -1
	if d := s.docLine(w); d != "" {
		doc = len(out)
		out = append(out, d)
	}
	out = append(out, strings.Split(wrap(st.Explain.String(), w), "\n")...)
	for i, l := range out {
		out[i] = strings.TrimRight(l, " ")
	}
	return out, doc
}

func kindBadge(k tour.Kind) string {
	switch k {
	case tour.Request:
		return ui.Badge(t("tour.kind.request"), ui.ToneAccent)
	case tour.Browser:
		return ui.Badge(t("tour.kind.browser"), ui.ToneNeutral)
	case tour.Wait:
		return ui.Badge(t("tour.kind.wait"), ui.ToneNeutral)
	case tour.Local:
		return ui.Badge(t("tour.kind.local"), ui.ToneNeutral)
	}
	return ui.Badge(t("tour.kind.explain"), ui.ToneWarning)
}

// statusLine says where the step stands.
func (s *tourScreen) statusLine(w int) string {
	o := s.run.Outcome(s.run.Step)
	switch {
	case s.busy:
		text := s.waiting
		if text == "" {
			text = t("tour.running")
		}
		return spinView(s.spin) + " " + ui.Label.Render(ansi.Truncate(text, w-2, "…"))
	case o == nil && s.planErr != nil:
		return ui.Danger.Render("! ") + ui.Label.Render(s.planErr.Error())
	case o == nil:
		return ui.Muted.Render(ansi.Truncate(t(readyKey(s.run.Current().Kind)), w, "…"))
	case o.Err != nil:
		return ui.Danger.Render("✗ ") + ui.Label.Render(ansi.Truncate(o.Err.Error(), w-2, "…"))
	case o.Skipped != "":
		return ui.Warning.Render("↷ ") + ui.Label.Render(ansi.Truncate(o.Skipped, w-2, "…"))
	case o.Status != "":
		return ansi.Truncate(o.Status, w, "…")
	}
	return ui.Success.Render("✓ ") + ui.Label.Render(t("tour.done"))
}

func readyKey(k tour.Kind) string {
	switch k {
	case tour.Request:
		return "tour.ready.request"
	case tour.Browser:
		return "tour.ready.browser"
	case tour.Wait:
		return "tour.ready.wait"
	case tour.Local:
		return "tour.ready.local"
	}
	return "tour.ready.explain"
}

func (s *tourScreen) paneTabs() string {
	names := []string{t("tour.pane.request"), t("tour.pane.response"), "curl"}
	var parts []string
	for i, n := range names {
		if i == s.pane {
			parts = append(parts, lipgloss.NewStyle().Bold(true).Foreground(ui.ColSelectionFg).Background(ui.ColSelection).Padding(0, 1).Render(n))
		} else {
			parts = append(parts, ui.Muted.Render(" "+n+" "))
		}
	}
	return strings.Join(parts, " ")
}

// paneBody is the content of the chosen pane, line by line.
func (s *tourScreen) paneBody(w int) []string {
	o := s.run.Outcome(s.run.Step)
	var x *tour.Exchange
	if o != nil {
		x = o.Exchange
	}
	switch s.pane {
	case paneCurl:
		curl := s.curl()
		if curl == "" {
			return []string{ui.Muted.Render(t("tour.nocurl"))}
		}
		return strings.Split(ui.Text.Render(curl), "\n")
	case paneRequest:
		switch {
		case x != nil:
			return requestLines(x.Method, x.URL, x.Headers, x.Body, x.Notes)
		case s.plan != nil:
			p := s.plan.Prepared
			return requestLines(p.Method, p.URL, planHeaders(s.run, s.plan), p.Body, s.plan.Notes)
		}
		return []string{ui.Muted.Render(t("tour.norequest"))}
	}
	// Response
	if o == nil {
		return []string{ui.Muted.Render(t("tour.noresponse"))}
	}
	var out []string
	// What a step found out of an answer, such as the eDCC outcome, comes
	// first: the answer itself can be long.
	extra := s.outcomeLines(o, w)
	if x != nil && x.Status != 0 {
		if len(extra) > 0 {
			out = append(out, extra...)
			out = append(out, "")
			extra = nil
		}
		out = append(out, statusTag(x.Status)+ui.Muted.Render(fmt.Sprintf(" · %s", formatDur(x.Duration))+demoTag(s.run)), "")
		out = append(out, tour.Annotate(x.Response, nil)...)
	}
	if wh := o.Webhook; wh != nil {
		out = append(out, ui.Bold.Render("POST")+" "+ui.Label.Render(t("tour.webhook.at")), "")
		for _, name := range []string{"Content-Type", "X-Paygate-Signature-Version", "X-Paygate-Timestamp", "X-Paygate-Signature"} {
			if v := wh.Header.Get(name); v != "" {
				out = append(out, ui.Label.Render(name+": ")+ui.Text.Render(v))
			}
		}
		out = append(out, "")
		out = append(out, tour.Annotate(wh.Body, nil)...)
	}
	if len(extra) > 0 {
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, extra...)
	}
	if len(out) == 0 {
		out = []string{ui.Muted.Render(t("tour.nothing"))}
	}
	return out
}

// outcomeLines are the lines of an outcome, wrapped to w. Text wraps; code
// lines such as the 3DS form are short enough.
func (s *tourScreen) outcomeLines(o *tour.Outcome, w int) []string {
	var out []string
	for _, l := range o.Lines {
		if lipgloss.Width(l) <= w {
			out = append(out, l)
			continue
		}
		for _, part := range strings.Split(wrap(l, w), "\n") {
			out = append(out, strings.TrimRight(part, " "))
		}
	}
	return out
}

func requestLines(method, url string, headers [][2]string, body []byte, notes map[string]tour.T) []string {
	out := []string{ui.Bold.Render(method) + " " + ui.Text.Render(url)}
	for _, h := range headers {
		out = append(out, ui.Label.Render(h[0]+": ")+ui.Text.Render(h[1]))
	}
	if len(body) > 0 {
		out = append(out, "")
		out = append(out, tour.Annotate(body, notes)...)
	}
	return out
}

// planHeaders are the headers a planned request will carry.
func planHeaders(r *tour.Run, p *tour.Planned) [][2]string {
	h := [][2]string{{"Authorization", "Basic ••••••"}}
	if p.Prepared.Body != nil {
		h = append(h, [2]string{"Content-Type", "application/json"})
	}
	if p.Prepared.IdempotencyKey != "" {
		h = append(h, [2]string{"Idempotency-Key", p.Prepared.IdempotencyKey})
	}
	return h
}

func curlOf(r *tour.Run, p *tour.Planned) string {
	return export.Curl(p.Prepared, r.Session.Client, false)
}

func statusTag(code int) string {
	text := fmt.Sprintf("%d %s", code, http.StatusText(code))
	if code >= 400 {
		return ui.Danger.Render(text)
	}
	return ui.Success.Render(text)
}

func demoTag(r *tour.Run) string {
	if r.Demo() {
		return " · " + t("home.demo.sim")
	}
	return ""
}

func formatDur(d time.Duration) string {
	if d < time.Millisecond {
		return "<1 ms"
	}
	return fmt.Sprintf("%d ms", d.Milliseconds())
}

// docLine is the link to the step's documentation: click it or press d.
func (s *tourScreen) docLine(w int) string {
	d := s.run.Current().Doc
	brand := s.run.Session.Profile.Brand
	if d.URL(brand) == "" {
		return ""
	}
	label := truncate(d.Label(brand), max(w-lipgloss.Width(" ↗  d"), 10))
	return ui.Link.Underline(true).Render(label+" ↗") + ui.Muted.Render("  d")
}
