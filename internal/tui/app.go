package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/krebs3r/paygate-cli/internal/ui"
)

// Options configure Run.
type Options struct {
	Version string
	Profile string // --profile flag
}

// Run starts the full-screen app and blocks until the user quits.
func Run(opts Options) error {
	ctx := &Context{Version: opts.Version, ProfileFlag: opts.Profile}
	ctx.Reload()
	m := newModel(ctx)
	_, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}

type focusArea int

const (
	focusMenu focusArea = iota
	focusContent
)

type model struct {
	ctx      *Context
	screens  []Screen
	active   int
	focus    focusArea
	help     bool
	width    int
	height   int
	toast    string
	toneT    ui.Tone
	toastSeq int
	tabStart int
}

func newModel(ctx *Context) *model {
	m := &model{
		ctx: ctx,
		screens: []Screen{
			newHomeScreen(),
			newNewPaymentScreen(),
			newSoonScreen("payments", "menu.payments", "l", 3, "soon.payments", "paygate get <payId>"),
			newCardsScreen(),
			newProfilesScreen(),
			newSoonScreen("inspect", "menu.inspect", "i", 4, "soon.inspect", "paygate pay --card visa-ok -v"),
			newSettingsScreen(),
			newAboutScreen(),
		},
	}
	if ctx.Session == nil {
		m.focus = focusContent
	}
	return m
}

func (m *model) Init() tea.Cmd {
	return m.screens[m.active].Activate(m.ctx)
}

func (m *model) current() Screen { return m.screens[m.active] }

func (m *model) indexOf(id string) int {
	for i, s := range m.screens {
		if s.ID() == id {
			return i
		}
	}
	return -1
}

func (m *model) activate(i int, focus focusArea) tea.Cmd {
	changed := i != m.active
	m.active, m.focus, m.help = i, focus, false
	if changed {
		return m.current().Activate(m.ctx)
	}
	return nil
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case switchMsg:
		i := m.indexOf(msg.id)
		if i < 0 {
			return m, nil
		}
		cmd := m.activate(i, focusContent)
		if r, ok := m.current().(Receiver); ok && msg.data != nil {
			cmd = tea.Batch(cmd, r.Receive(m.ctx, msg.data))
		}
		return m, cmd

	case sessionChangedMsg:
		m.ctx.Reload()
		var cmds []tea.Cmd
		for _, s := range m.screens {
			cmds = append(cmds, s.Update(m.ctx, msg))
		}
		return m, tea.Batch(cmds...)

	case focusMenuMsg:
		m.focus = focusMenu
		return m, nil

	case toastMsg:
		m.toastSeq++
		m.toast, m.toneT = msg.text, msg.tone
		return m, clearToastAfter(m.toastSeq)

	case clearToastMsg:
		if msg.id == m.toastSeq {
			m.toast = ""
		}
		return m, nil

	case tea.MouseMsg:
		return m, m.handleMouse(msg)

	case tea.KeyMsg:
		return m, m.handleKey(msg)
	}

	// Everything else (spinner ticks, API results) goes to all screens.
	var cmds []tea.Cmd
	for _, s := range m.screens {
		cmds = append(cmds, s.Update(m.ctx, msg))
	}
	return m, tea.Batch(cmds...)
}

func (m *model) handleKey(msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	if k == "ctrl+c" {
		return tea.Quit
	}
	if m.help {
		switch k {
		case "esc", "?", "q", "enter":
			m.help = false
		}
		return nil
	}

	capturing := m.focus == focusContent && m.current().Capturing()
	if !capturing {
		switch k {
		case "q":
			return tea.Quit
		case "?":
			m.help = true
			return nil
		case "tab":
			if m.focus == focusMenu {
				m.focus = focusContent
			} else {
				m.focus = focusMenu
			}
			return nil
		}
		for i, s := range m.screens {
			if s.Shortcut() != "" && s.Shortcut() == k && !(m.focus == focusContent && i == m.active) {
				return m.activate(i, focusContent)
			}
		}
	}

	if m.focus == focusMenu {
		switch k {
		case "up", "k":
			return m.activate((m.active+len(m.screens)-1)%len(m.screens), focusMenu)
		case "down", "j":
			return m.activate((m.active+1)%len(m.screens), focusMenu)
		case "enter", "right", "l":
			m.focus = focusContent
			return nil
		}
		return nil
	}

	if k == "esc" && !capturing {
		if !m.current().Back(m.ctx) {
			m.focus = focusMenu
		}
		return nil
	}
	if (k == "left") && !capturing {
		if !m.current().Back(m.ctx) {
			m.focus = focusMenu
		}
		return nil
	}
	return m.current().Update(m.ctx, msg)
}

func (m *model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		if m.focus == focusContent {
			return m.current().Update(m.ctx, msg)
		}
		return nil
	}
	l := m.layout()
	if l.wide && msg.X >= 1 && msg.X <= l.sidebarW && msg.Y >= l.bodyTop+1 {
		i := msg.Y - l.bodyTop - 1
		if i >= 0 && i < len(m.screens) {
			return m.activate(i, focusContent)
		}
	}
	if !l.wide && msg.Y == l.bodyTop {
		x := 1
		if m.tabStart > 0 {
			x += 2
		}
		for i := m.tabStart; i < len(m.screens); i++ {
			s := m.screens[i]
			w := len([]rune(s.Title())) + 3
			if msg.X >= x && msg.X < x+w {
				return m.activate(i, focusContent)
			}
			x += w
		}
	}
	if m.focus == focusContent {
		return m.current().Update(m.ctx, msg)
	}
	return nil
}

func (m *model) View() string {
	if m.width == 0 {
		return ""
	}
	if m.width < 70 || m.height < 20 {
		return strings.Repeat("\n", m.height/2) + ui.Warning.Render("  "+t("too.small"))
	}
	return m.render()
}
