package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/krebs3r/paygate-cli/internal/ui"
)

func title(s string) string   { return ui.Bold.Render(s) }
func section(s string) string { return ui.Muted.Render(s) }

// box draws a rounded panel of total width w.
func box(content string, w int, border lipgloss.TerminalColor) string {
	if w < 10 {
		w = 10
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Padding(0, 1).
		Width(w - 2).
		Render(content)
}

// kv renders aligned label/value rows; empty values are skipped.
func kv(rows [][2]string) string {
	w := 0
	for _, r := range rows {
		if lw := lipgloss.Width(r[0]); lw > w {
			w = lw
		}
	}
	var out []string
	for _, r := range rows {
		if r[1] == "" {
			continue
		}
		out = append(out, ui.Label.Render(r[0]+strings.Repeat(" ", w-lipgloss.Width(r[0])))+"   "+r[1])
	}
	return strings.Join(out, "\n")
}

// selectable renders one list row; the selected one as a band. While the
// keyboard is in the menu, only a bar marks it, so it is clear where keys
// go. A nil ctx always shows the band (overlays such as the palette).
func selectable(ctx *Context, row string, selected bool, w int) string {
	switch {
	case !selected:
		return "  " + row
	case ctx != nil && ctx.MenuFocused:
		return ui.SelectedIdle(row)
	}
	return ui.Selected(row, w)
}

// wrap word-wraps text to width w.
func wrap(s string, w int) string {
	return lipgloss.NewStyle().Width(w).Render(s)
}

func newSpinner() spinner.Model {
	s := spinner.New()
	s.Spinner = spinner.Spinner{Frames: []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}, FPS: spinner.Dot.FPS}
	return s
}

// spinView draws a spinner in the accent colour of the active theme.
func spinView(s spinner.Model) string {
	s.Style = ui.Accent
	return s.View()
}

// formKeyMap lets esc cancel an embedded form instead of quitting the app
// and translates the key help shown below each form.
func formKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", t("key.cancel")))
	h := func(b *key.Binding, k string) { b.SetHelp(b.Help().Key, t(k)) }

	h(&km.Input.Next, "key.next")
	h(&km.Input.Prev, "key.back")
	h(&km.Input.Submit, "form.submit")
	h(&km.Text.Next, "key.next")
	h(&km.Text.Prev, "key.back")
	h(&km.Text.Submit, "form.submit")
	h(&km.Note.Next, "key.next")
	h(&km.Note.Prev, "key.back")
	h(&km.Note.Submit, "form.submit")

	s := &km.Select
	for b, k := range map[*key.Binding]string{
		&s.Next: "key.select", &s.Prev: "key.back", &s.Submit: "form.submit", &s.Up: "form.up", &s.Down: "form.down",
		&s.Left: "form.left", &s.Right: "form.right", &s.Filter: "key.filter", &s.SetFilter: "form.setfilter",
		&s.ClearFilter: "form.clearfilter", &s.HalfPageUp: "form.pageup", &s.HalfPageDown: "form.pagedown",
		&s.GotoTop: "form.top", &s.GotoBottom: "form.bottom",
	} {
		h(b, k)
	}
	ms := &km.MultiSelect
	for b, k := range map[*key.Binding]string{
		&ms.Next: "form.confirm", &ms.Prev: "key.back", &ms.Submit: "form.submit", &ms.Toggle: "form.toggle",
		&ms.Up: "form.up", &ms.Down: "form.down", &ms.Filter: "key.filter", &ms.SetFilter: "form.setfilter",
		&ms.ClearFilter: "form.clearfilter", &ms.HalfPageUp: "form.pageup", &ms.HalfPageDown: "form.pagedown",
		&ms.GotoTop: "form.top", &ms.GotoBottom: "form.bottom", &ms.SelectAll: "form.selectall", &ms.SelectNone: "form.selectnone",
	} {
		h(b, k)
	}
	c := &km.Confirm
	for b, k := range map[*key.Binding]string{
		&c.Next: "key.next", &c.Prev: "key.back", &c.Submit: "form.submit", &c.Toggle: "form.toggle", &c.Accept: "yes", &c.Reject: "no",
	} {
		h(b, k)
	}
	return km
}

// newForm builds an embedded form in the app's look.
func newForm(w int, groups ...*huh.Group) *huh.Form {
	if w > 76 {
		w = 76
	}
	f := huh.NewForm(groups...).
		WithTheme(ui.FormTheme()).
		WithKeyMap(formKeyMap()).
		WithShowHelp(true).
		WithWidth(w)
	// huh measures a group when it is created, before it knows the width,
	// so a description that wraps later would push the last options out of
	// view. Measure again at the real width.
	for _, g := range groups {
		h := lipgloss.Height(g.Content())
		for _, s := range []string{g.Header(), g.Footer()} {
			if s != "" {
				h += lipgloss.Height(s)
			}
		}
		g.WithHeight(h)
	}
	return f
}

func clamp(i, n int) int {
	if n == 0 {
		return 0
	}
	if i < 0 {
		return n - 1
	}
	if i >= n {
		return 0
	}
	return i
}
