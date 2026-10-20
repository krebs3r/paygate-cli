package tui

import (
	"os"
	"strings"
	"time"

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

	pal palette

	splash      bool
	splashStart time.Time
	splashFrame int
}

// Splash timing: shown at least splashMin, at most splashMax.
var (
	splashMin = 2500 * time.Millisecond
	splashMax = 4 * time.Second
)

func newModel(ctx *Context) *model {
	m := &model{
		ctx: ctx,
		screens: []Screen{
			newHomeScreen(),
			newNewPaymentScreen(),
			newPaymentsScreen(),
			newCardsScreen(),
			newProfilesScreen(),
			newInspectorScreen(),
			newSettingsScreen(),
			newAboutScreen(),
		},
	}
	if ctx.Session == nil {
		m.focus = focusContent
	}
	m.splash = !(ctx.Cfg != nil && ctx.Cfg.NoSplash) && os.Getenv("PAYGATE_NO_SPLASH") == ""
	m.splashStart = time.Now()
	return m
}

func (m *model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.screens[m.active].Activate(m.ctx), verifySession(m.ctx)}
	if m.splash {
		cmds = append(cmds, splashTick())
	}
	return tea.Batch(cmds...)
}

func splashTick() tea.Cmd {
	return tea.Tick(90*time.Millisecond, func(time.Time) tea.Msg { return splashTickMsg{} })
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

	case splashTickMsg:
		if !m.splash {
			return m, nil
		}
		m.splashFrame++
		el := time.Since(m.splashStart)
		if el > splashMax || (el > splashMin && (m.ctx.VerifyDone || m.ctx.Session == nil)) {
			m.splash = false
			return m, nil
		}
		return m, splashTick()

	case sessionVerifiedMsg:
		if m.ctx.Session == nil || msg.name != m.ctx.Session.Name {
			return m, nil
		}
		m.ctx.VerifyDone, m.ctx.Verified, m.ctx.VerifyErr = true, msg.err == nil, msg.err
		if msg.err != nil && !m.splash {
			return m, toast(t("splash.fail"), ui.ToneDanger)
		}
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
		cmds := []tea.Cmd{verifySession(m.ctx)}
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
	if m.pal.open {
		var cmd tea.Cmd
		m.pal.input, cmd = m.pal.input.Update(msg)
		cmds = append(cmds, cmd)
	}
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
	if m.splash {
		m.splash = false
		if m.ctx.VerifyErr != nil {
			return toast(t("splash.fail"), ui.ToneDanger)
		}
		return nil
	}
	if m.pal.open {
		return m.updatePalette(msg)
	}
	if m.help {
		switch k {
		case "esc", "?", "q", "enter":
			m.help = false
		}
		return nil
	}

	capturing := m.focus == focusContent && m.current().Capturing()
	if k == "ctrl+p" || (k == "ctrl+k" && !capturing) {
		m.help = false
		return m.openPalette()
	}
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
	if l.wide && msg.X >= 1 && msg.X <= l.sidebarW && msg.Y >= l.bodyTop+sidebarItemsTop {
		i := msg.Y - l.bodyTop - sidebarItemsTop
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
	if m.splash {
		return m.renderSplash()
	}
	return m.render()
}
