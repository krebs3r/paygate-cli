package tui

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/krebs3r/paygate-cli/internal/cards"
	"github.com/krebs3r/paygate-cli/internal/history"
	"github.com/krebs3r/paygate-cli/internal/i18n"
	"github.com/krebs3r/paygate-cli/internal/reqlog"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

// paletteCmd is one entry of the command palette. keywords hold extra search
// terms in both languages, so "refu" and "erst" both find refunds.
type paletteCmd struct {
	label    string
	keywords string
	run      func(m *model) tea.Cmd
}

type palette struct {
	open    bool
	input   textinput.Model
	items   []paletteCmd
	matches []paletteCmd
	sel     int
}

const paletteRows = 10

func (m *model) openPalette() tea.Cmd {
	in := textinput.New()
	in.Placeholder = t("pal.placeholder")
	in.Prompt = "› "
	in.PromptStyle = ui.Accent
	in.TextStyle = ui.Text
	in.PlaceholderStyle = ui.Muted
	in.Focus()
	m.pal = palette{open: true, input: in, items: m.paletteCommands()}
	m.pal.filter()
	return textinput.Blink
}

func (m *model) paletteCommands() []paletteCmd {
	var cmds []paletteCmd
	add := func(label, keywords string, run func(m *model) tea.Cmd) {
		cmds = append(cmds, paletteCmd{label: label, keywords: keywords, run: run})
	}
	add(t("act.new"), "new payment pay card neue zahlung bezahlen karte", func(*model) tea.Cmd { return switchTo("new", nil) })
	add(t("pal.lookup"), "look up find search payid transid zahlung suchen finden", func(*model) tea.Cmd { return switchTo("payments", "lookup") })
	add(t("pal.last"), "last payment refund capture reversal reverse cancel release letzte zahlung erstatten einziehen freigeben stornieren", func(*model) tea.Cmd {
		r, ok := m.lastPayment()
		if !ok {
			return toast(t("pal.nolast"), ui.ToneWarning)
		}
		return switchTo("payments", r)
	})
	add(t("pal.copylast"), "copy last payid clipboard kopieren letzte", func(*model) tea.Cmd {
		r, ok := m.lastPayment()
		if !ok || r.PayID == "" {
			return toast(t("pal.nolast"), ui.ToneWarning)
		}
		if err := clipboard.WriteAll(r.PayID); err != nil {
			return toast(err.Error(), ui.ToneDanger)
		}
		return toast(t("copied.payid"), ui.ToneSuccess)
	})
	for _, s := range m.screens {
		id := s.ID()
		add(fmt.Sprintf(t("pal.go"), s.Title()), "go open screen gehe öffnen "+id, func(*model) tea.Cmd { return switchTo(id, nil) })
	}
	add(t("pal.addprofile"), "add profile merchant credentials profil hinzufügen händler zugangsdaten", func(*model) tea.Cmd { return switchTo("profiles", "add") })
	add(t("pal.reset"), "factory reset delete all data wipe werksreset zurücksetzen alle daten löschen", func(*model) tea.Cmd { return switchTo("settings", "reset") })
	if m.ctx.Cfg != nil {
		for _, name := range m.ctx.Cfg.Names() {
			n := name
			if m.ctx.Session != nil && n == m.ctx.Session.Name {
				continue
			}
			env := "test"
			if m.ctx.Cfg.Profiles[n].IsProd() {
				env = "prod production produktion"
			}
			add(fmt.Sprintf(t("pal.switch"), n), "switch profile wechseln "+env, func(m *model) tea.Cmd {
				m.ctx.Cfg.ActiveProfile = n
				if err := m.ctx.Cfg.Save(); err != nil {
					return toast(err.Error(), ui.ToneDanger)
				}
				return tea.Batch(sessionChanged(), toast(fmt.Sprintf(t("prof.switched"), n), ui.ToneSuccess))
			})
		}
	}
	other, otherName := "de", "Deutsch"
	if i18n.Current() == i18n.DE {
		other, otherName = "en", "English"
	}
	add(fmt.Sprintf(t("pal.lang"), otherName), "language sprache english deutsch", func(m *model) tea.Cmd {
		m.ctx.Cfg.UILanguage = other
		i18n.Set(i18n.Detect(other))
		if err := m.ctx.Cfg.Save(); err != nil {
			return toast(err.Error(), ui.ToneDanger)
		}
		return toast(t("set.saved"), ui.ToneSuccess)
	})
	for _, th := range ui.Themes {
		id := th.ID
		if id == ui.ThemeID() {
			continue
		}
		add(fmt.Sprintf(t("pal.theme"), themeLabel(id)), "theme colour color scheme farbschema farben design "+id, func(m *model) tea.Cmd {
			setTheme(m.ctx.Cfg, id)
			if err := m.ctx.Cfg.Save(); err != nil {
				return toast(err.Error(), ui.ToneDanger)
			}
			return toast(t("set.saved"), ui.ToneSuccess)
		})
	}
	if m.ctx.Listen != nil {
		add(t("pal.stoplisten"), "webhooks stop listening tunnel cloudflared zuhören beenden", func(*model) tea.Cmd { return switchTo("webhooks", "stop") })
	} else if m.ctx.Session != nil {
		add(t("pal.listen"), "webhooks listen receive tunnel cloudflared zuhören empfangen", func(*model) tea.Cmd { return switchTo("webhooks", "listen") })
	}
	add(t("pal.clearhooks"), "webhooks clear delete received empfangene löschen leeren", func(*model) tea.Cmd { return switchTo("webhooks", "clear") })
	add(t("pal.clearlog"), "clear request log inspector leeren protokoll", func(m *model) tea.Cmd {
		if err := reqlog.Clear(); err != nil {
			return toast(err.Error(), ui.ToneDanger)
		}
		return toast(t("insp.cleared"), ui.ToneSuccess)
	})
	for _, c := range cards.Cards {
		c := c
		label := fmt.Sprintf(t("pal.card"), brandName(c.Brand), formatPAN(c.Number), cardOutcome(c))
		keywords := "test card testkarte copy kopieren number nummer " + c.Alias + " " + c.Scenario + " " + c.Expect + " " + c.Number
		add(label, keywords, func(*model) tea.Cmd {
			if err := clipboard.WriteAll(c.Number); err != nil {
				return toast(err.Error(), ui.ToneDanger)
			}
			return toast(t("cards.copied"), ui.ToneSuccess)
		})
	}
	add(t("pal.quit"), "quit exit beenden", func(m *model) tea.Cmd { return m.quit() })
	return cmds
}

// cardOutcome describes a test card in a few words: the outcome, and for
// 3-D Secure cards the kind of authentication.
func cardOutcome(c cards.Card) string {
	switch {
	case !c.ThreeDS:
		return t(c.Expect)
	case c.Scenario == "3DS challenge":
		return t(c.Scenario) + ", OTP 1234"
	case c.Expect == "authenticated":
		return t(c.Scenario)
	}
	return "3DS, " + t(c.Expect)
}

func (m *model) lastPayment() (history.Record, bool) {
	if m.ctx.Session == nil {
		return history.Record{}, false
	}
	all, _ := history.Load()
	for _, r := range all {
		if r.Profile == m.ctx.Session.Name {
			return r, true
		}
	}
	return history.Record{}, false
}

func (p *palette) filter() {
	q := strings.TrimSpace(p.input.Value())
	if q == "" {
		p.matches = p.items
		p.sel = 0
		return
	}
	type scored struct {
		cmd   paletteCmd
		score int
	}
	var out []scored
	for _, c := range p.items {
		best := -1
		if sc, ok := fuzzy(q, c.label); ok {
			best = sc + 10 // label matches win ties against keyword matches
		}
		for _, w := range strings.Fields(c.keywords) {
			if sc, ok := fuzzy(q, w); ok && sc > best {
				best = sc
			}
		}
		if best >= 0 {
			out = append(out, scored{c, best})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	// A new slice: with an empty query matches shares the array of items,
	// and appending in place would overwrite the commands.
	p.matches = make([]paletteCmd, 0, len(out))
	for _, s := range out {
		p.matches = append(p.matches, s.cmd)
	}
	p.sel = 0
}

// fuzzy scores how well query matches text: whole-word prefixes beat
// substrings, which beat loose subsequences; subsequences with large gaps
// do not match at all.
func fuzzy(query, text string) (int, bool) {
	q := strings.ToLower(strings.TrimSpace(query))
	t := strings.ToLower(text)
	if q == "" {
		return 0, true
	}
	if i := strings.Index(t, q); i >= 0 {
		if i == 0 || !unicode.IsLetter(rune(t[i-1])) {
			return 300 - i, true
		}
		return 200 - i, true
	}
	qr, tr := []rune(q), []rune(t)
	qi, gaps, last := 0, 0, -1
	for i := 0; i < len(tr) && qi < len(qr); i++ {
		if tr[i] != qr[qi] {
			continue
		}
		if last >= 0 {
			gaps += i - last - 1
		}
		last = i
		qi++
	}
	if qi < len(qr) || gaps > 2*len(qr) {
		return 0, false
	}
	return 100 - gaps*5, true
}

func (m *model) updatePalette(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+k", "ctrl+p":
		m.pal.open = false
		return nil
	case "up", "ctrl+up":
		m.pal.sel = clamp(m.pal.sel-1, len(m.pal.matches))
		return nil
	case "down", "tab", "ctrl+down":
		m.pal.sel = clamp(m.pal.sel+1, len(m.pal.matches))
		return nil
	case "enter":
		m.pal.open = false
		if len(m.pal.matches) == 0 {
			return nil
		}
		return m.pal.matches[m.pal.sel].run(m)
	}
	var cmd tea.Cmd
	m.pal.input, cmd = m.pal.input.Update(msg)
	m.pal.filter()
	return cmd
}

func (m *model) paletteView(w int) string {
	boxW := min(w, 84)
	inner := boxW - 4 // border and padding
	var b strings.Builder
	b.WriteString(m.pal.input.View() + "\n")
	b.WriteString(ui.Muted.Render(strings.Repeat("─", inner)) + "\n")
	if len(m.pal.matches) == 0 {
		b.WriteString(ui.Muted.Render(t("pal.none")))
	}
	start := 0
	if m.pal.sel >= paletteRows {
		start = m.pal.sel - paletteRows + 1
	}
	for i := start; i < len(m.pal.matches) && i < start+paletteRows; i++ {
		b.WriteString(selectable(nil, truncate(m.pal.matches[i].label, inner-3), i == m.pal.sel, inner) + "\n")
	}
	if extra := len(m.pal.matches) - start - paletteRows; extra > 0 {
		b.WriteString(ui.Muted.Render(fmt.Sprintf("  … +%d", extra)))
	}
	return box(strings.TrimRight(b.String(), "\n"), boxW, ui.ColAccent)
}
