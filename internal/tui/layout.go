package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/krebs3r/paygate-cli/internal/i18n"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

func t(key string) string { return i18n.T(key) }

const (
	sidebarW = 31
	wideMinW = 100
	// sidebarItemsTop is the first menu row below the logo.
	sidebarItemsTop = 4
)

type layoutInfo struct {
	wide     bool
	innerW   int
	bodyH    int
	bodyTop  int // absolute screen row of the first body line
	sidebarW int
	contentW int
}

// The frame: the top border carries version and profile, the bottom border a
// small stamp; menu, content and footer are joined with proper junctions.
//
//	╭─ ◆ 0.2.0 ──────┬── TEST nexi · shop ─╮
//	│ menu           │ content             │
//	├────────────────┴─────────────────────┤
//	│ key hints                            │
//	╰────────────────── ▰ PAYGATE · CLI ▰ ─╯
func (m *model) layout() layoutInfo {
	l := layoutInfo{innerW: m.width - 2, bodyTop: 1, sidebarW: sidebarW}
	l.wide = m.width >= wideMinW
	if l.wide {
		l.bodyH = m.height - 4 // top border, footer separator, footer, bottom border
		l.contentW = l.innerW - sidebarW - 1
	} else {
		l.bodyH = m.height - 6 // plus tabs and their separator
		l.contentW = l.innerW
	}
	return l
}

func frame(s string) string { return lipgloss.NewStyle().Foreground(ui.ColFrame).Render(s) }

func (m *model) render() string {
	l := m.layout()
	rows := []string{m.topBorder(l)}
	if l.wide {
		side := strings.Split(m.renderSidebar(l.bodyH), "\n")
		content := strings.Split(m.renderContent(l.contentW, l.bodyH), "\n")
		for i := 0; i < l.bodyH; i++ {
			rows = append(rows, frame("│")+side[i]+frame("│")+content[i]+frame("│"))
		}
		rows = append(rows, frame("├"+strings.Repeat("─", sidebarW)+"┴"+strings.Repeat("─", l.contentW)+"┤"))
	} else {
		rows = append(rows, frame("│")+m.renderTabs(l.innerW)+frame("│"))
		rows = append(rows, frame("├"+strings.Repeat("─", l.innerW)+"┤"))
		for _, c := range strings.Split(m.renderContent(l.contentW, l.bodyH), "\n") {
			rows = append(rows, frame("│")+c+frame("│"))
		}
		rows = append(rows, frame("├"+strings.Repeat("─", l.innerW)+"┤"))
	}
	rows = append(rows, frame("│")+m.renderFooter(l.innerW)+frame("│"), m.bottomBorder(l))
	return strings.Join(rows, "\n")
}

// topBorder embeds version (left) and the active profile (right).
func (m *model) topBorder(l layoutInfo) string {
	left := " " + ui.Accent.Render("◆") + " " + ui.Label.Render(shortVersion(m.ctx.Version)) + " "
	right := " " + ui.Muted.Render(t("no.profile")) + " "
	if s := m.ctx.Session; s != nil {
		right = " " + ui.EnvBadge(s.Profile.IsProd()) + " " + ui.Label.Render(s.Profile.Brand+" · "+s.Name) + " " + ui.Muted.Render(s.Profile.MerchantID) + " "
	}
	lw, rw := lipgloss.Width(left), lipgloss.Width(right)
	if l.wide {
		leftFill := max(sidebarW-1-lw, 0)
		rightFill := l.contentW - 1 - rw
		if rightFill < 0 {
			right, rightFill = "", l.contentW-1
		}
		return frame("╭─") + left + frame(strings.Repeat("─", leftFill)+"┬"+strings.Repeat("─", rightFill)) + right + frame("─╮")
	}
	fill := l.innerW - 2 - lw - rw
	if fill < 0 {
		right, fill = "", l.innerW-2-lw
	}
	return frame("╭─") + left + frame(strings.Repeat("─", max(fill, 0))) + right + frame("─╮")
}

// bottomBorder ends the frame with a small brand stamp.
func (m *model) bottomBorder(l layoutInfo) string {
	stamp := " " + ui.Stamp() + " "
	fill := l.innerW - 2 - lipgloss.Width(stamp)
	if fill < 2 {
		return frame("╰" + strings.Repeat("─", l.innerW) + "╯")
	}
	return frame("╰"+strings.Repeat("─", fill)) + stamp + frame("──╯")
}

// shortVersion hides Go pseudo-versions of development builds.
func shortVersion(v string) string {
	if strings.HasPrefix(v, "v0.0.0-") || strings.Contains(v, "+dirty") {
		return "dev"
	}
	return v
}

func (m *model) renderSidebar(h int) string {
	logo := strings.Split(ui.SmallLogo(), "\n")
	lines := []string{"  " + logo[0], "  " + logo[1], " " + ui.CLIDivider(sidebarW-2), ""}
	for i, s := range m.screens {
		title := truncate(s.Title(), sidebarW-6)
		key := ui.Muted.Render(s.Shortcut())
		pad := sidebarW - 4 - lipgloss.Width(title) - lipgloss.Width(s.Shortcut())
		if pad < 1 {
			pad = 1
		}
		switch {
		case i == m.active && m.focus == focusMenu:
			row := "▌ " + title + strings.Repeat(" ", pad) + s.Shortcut() + " "
			lines = append(lines, lipgloss.NewStyle().Background(ui.ColSelection).Foreground(ui.ColAccent2).Bold(true).Width(sidebarW).Render(row))
		case i == m.active:
			lines = append(lines, ui.Accent.Render("▸ ")+lipgloss.NewStyle().Foreground(ui.ColAccent2).Render(title)+strings.Repeat(" ", pad)+key+" ")
		default:
			lines = append(lines, "  "+ui.Label.Render(title)+strings.Repeat(" ", pad)+key+" ")
		}
	}
	return fit(strings.Join(lines, "\n"), sidebarW, h)
}

func (m *model) renderTabs(w int) string {
	// Start late enough that the active tab is always visible.
	start := 0
	for {
		total := 0
		for i := start; i <= m.active; i++ {
			total += len([]rune(m.screens[i].Title())) + 3
		}
		if total <= w || start == m.active {
			break
		}
		start++
	}
	m.tabStart = start
	var parts []string
	if start > 0 {
		parts = append(parts, ui.Muted.Render("‹"))
	}
	for i := start; i < len(m.screens); i++ {
		label := " " + m.screens[i].Title() + " "
		if i == m.active {
			parts = append(parts, lipgloss.NewStyle().Background(ui.ColSelection).Foreground(ui.ColAccent2).Bold(true).Render(label))
		} else {
			parts = append(parts, ui.Label.Render(label))
		}
	}
	return fit(strings.Join(parts, " "), w, 1)
}

func (m *model) renderContent(w, h int) string {
	cw, ch := w-4, h-2
	var content string
	switch {
	case m.pal.open:
		content = m.paletteView(cw)
	case m.help:
		content = m.helpView(cw)
	default:
		content = m.current().View(m.ctx, cw, ch)
	}
	return lipgloss.NewStyle().Padding(1, 2).Render(fit(content, cw, ch))
}

func (m *model) renderFooter(w int) string {
	var hints []Hint
	switch {
	case m.pal.open:
		hints = []Hint{{"↑↓", t("key.select")}, {"enter", t("key.open")}, {"esc", t("key.close")}}
	case m.help:
		hints = []Hint{{"esc", t("key.close")}}
	case m.focus == focusMenu:
		hints = []Hint{{"↑↓", t("key.navigate")}, {"enter", t("key.open")}}
	default:
		hints = m.current().Hints(m.ctx)
		hasEsc := false
		for _, h := range hints {
			hasEsc = hasEsc || h.Key == "esc"
		}
		if !m.current().Capturing() && !hasEsc {
			hints = append(hints, Hint{"esc", t("key.menu")})
		}
	}
	if !m.pal.open && (!m.current().Capturing() || m.focus == focusMenu) {
		hints = append(hints, Hint{"ctrl+k", t("key.commands")}, Hint{"?", t("key.help")}, Hint{"q", t("key.quit")})
	}
	var parts []string
	for _, h := range hints {
		parts = append(parts, lipgloss.NewStyle().Foreground(ui.ColMuted2).Bold(true).Render(h.Key)+" "+ui.Muted.Render(h.Desc))
	}
	left := " " + strings.Join(parts, ui.Muted.Render("  ·  "))
	right := ""
	if m.toast != "" {
		style := ui.Success
		switch m.toneT {
		case ui.ToneDanger:
			style = ui.Danger
		case ui.ToneWarning:
			style = ui.Warning
		}
		right = style.Render(m.toast) + " "
		if avail := w - lipgloss.Width(right) - 1; lipgloss.Width(left) > avail {
			left = fit(left, max(avail, 0), 1)
		}
	}
	if right == "" && lipgloss.Width(left) > w {
		return fit(left, w, 1)
	}
	return spread(left, right, w)
}

func (m *model) helpView(w int) string {
	rows := [][2]string{
		{"↑ ↓  j k", t("help.move")},
		{"enter", t("help.open")},
		{"esc", t("help.back")},
		{"tab", t("help.tab")},
		{"ctrl+k ctrl+p", t("help.palette")},
	}
	var keys []string
	for _, s := range m.screens {
		if s.Shortcut() != "" {
			keys = append(keys, s.Shortcut())
		}
	}
	rows = append(rows,
		[2]string{strings.Join(keys, " "), t("help.jump")},
		[2]string{t("help.clickkey"), t("help.mouse")},
		[2]string{"q  ctrl+c", t("help.quit")},
	)
	var b strings.Builder
	b.WriteString(ui.Bold.Render(t("help.title")) + "\n\n")
	for _, r := range rows {
		b.WriteString(lipgloss.NewStyle().Foreground(ui.ColAccent2).Width(16).Render(r[0]) + ui.Label.Render(r[1]) + "\n")
	}
	for _, s := range m.screens {
		hints := s.Hints(m.ctx)
		if len(hints) == 0 {
			continue
		}
		b.WriteString("\n" + ui.Muted.Render(strings.ToUpper(s.Title())) + "  ")
		var parts []string
		for _, h := range hints {
			parts = append(parts, ui.Label.Render(h.Key)+" "+ui.Muted.Render(h.Desc))
		}
		b.WriteString(strings.Join(parts, ui.Muted.Render(" · ")))
	}
	return box(b.String(), min(w, 78), ui.ColAccent)
}

func (m *model) renderSplash() string {
	frame := m.splashFrame
	logo := ui.BigLogo(frame/2) + "\n" + ui.CLILabel(ui.BigLogoWidth)
	if m.width < ui.BigLogoWidth+6 || m.height < 18 {
		logo = ui.SmallLogo() + "\n" + ui.CLILabel(ui.SmallLogoWidth)
	}
	// The bar fills over the minimum splash time and completes once the
	// credential check has answered.
	bar := 24
	filled := int(float64(bar) * float64(time.Since(m.splashStart)) / float64(splashMin))
	filled = max(0, min(bar-1, filled))
	status := ui.Muted.Render(t("splash.verifying"))
	switch {
	case m.ctx.Session == nil:
		status = ui.Label.Render(t("splash.noprofile"))
	case m.ctx.VerifyDone && m.ctx.Verified:
		status = ui.Success.Render("✓ " + t("splash.ok"))
	case m.ctx.VerifyDone:
		status = ui.Danger.Render("✗ " + t("splash.fail"))
	}
	if (m.ctx.Session == nil || m.ctx.VerifyDone) && time.Since(m.splashStart) >= splashMin {
		filled = bar
	}
	progress := ui.Link.Render(strings.Repeat("▰", filled)) + ui.Muted.Render(strings.Repeat("▱", bar-filled)) + "  " + status

	profile := ""
	if s := m.ctx.Session; s != nil {
		profile = ui.EnvBadge(s.Profile.IsProd()) + "  " + ui.Label.Render(s.Profile.Brand+" · "+s.Name+" · "+s.Profile.MerchantID)
	}
	lines := []string{
		logo,
		"",
		ui.Label.Render(t("splash.tagline")),
		"",
		ui.Accent.Render("◆") + " " + ui.Bold.Render(shortVersion(m.ctx.Version)) + "  " + profile,
		progress,
		"",
		ui.Muted.Render(t("splash.skip")),
	}
	block := lipgloss.NewStyle().Align(lipgloss.Left).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, block)
}

// spread puts left and right on one line of width w.
func spread(left, right string, w int) string {
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return fit(left, w, 1)
	}
	return left + strings.Repeat(" ", gap) + right
}

// fit pads or clips a block to exactly w × h cells.
func fit(s string, w, h int) string {
	if h < 1 || w < 1 {
		return ""
	}
	return lipgloss.NewStyle().Width(w).MaxWidth(w).Height(h).MaxHeight(h).Render(s)
}

func truncate(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w <= 1 {
		return string(r[:w])
	}
	return string(r[:w-1]) + "…"
}
