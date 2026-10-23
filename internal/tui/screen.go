// Package tui is the full-screen app that opens when paygate runs without a
// command: a sidebar with all areas, contextual key hints and embedded forms.
package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/i18n"
	"github.com/krebs3r/paygate-cli/internal/listen"
	"github.com/krebs3r/paygate-cli/internal/reqlog"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

// Hint is one entry of the footer key help.
type Hint struct{ Key, Desc string }

// Screen is one area of the app.
type Screen interface {
	ID() string
	Title() string
	Shortcut() string
	// Activate runs when the screen becomes visible.
	Activate(ctx *Context) tea.Cmd
	Update(ctx *Context, msg tea.Msg) tea.Cmd
	View(ctx *Context, w, h int) string
	Hints(ctx *Context) []Hint
	// Capturing is true while a form or text input owns the keyboard, so
	// single-letter shortcuts must not fire.
	Capturing() bool
	// Back handles esc inside the screen (closing a sub-view, going back a
	// step) and may return a command, e.g. to start a form. Returning false
	// moves the focus back to the menu.
	Back(ctx *Context) (bool, tea.Cmd)
}

// FullScreener screens can temporarily take over the whole terminal, e.g. to
// show a large QR code in a small window.
type FullScreener interface {
	FullScreen(ctx *Context, w, h int) (string, bool)
}

// Quitter screens say what quitting the app now would end, if anything,
// so q asks first.
type Quitter interface {
	QuitWarning(ctx *Context) string
}

// Receiver screens accept data when another screen switches to them.
type Receiver interface {
	Receive(ctx *Context, data any) tea.Cmd
}

// Context is the state shared by all screens.
type Context struct {
	Cfg         *config.Config
	Session     *core.Session
	SessionErr  error
	Version     string
	Commit      string
	ProfileFlag string
	// Width and Height are the size of the content area, so screens can
	// build forms before their first View.
	Width, Height int
	// MenuFocused is true while the keyboard is in the menu.
	MenuFocused bool
	// ContentX and ContentY are the screen position of the content area,
	// so screens can tell what a mouse click hit.
	ContentX, ContentY int

	// Background credential check of the active profile.
	Verified   bool
	VerifyDone bool
	VerifyErr  error

	// Listen is the webhook listener this app runs; Listening is the one
	// of the active profile, here or in another terminal.
	Listen    *listen.Session
	Listening *listen.State
	opening   *listenOpening
}

// Reload reads the configuration again and reopens the active profile.
func (c *Context) Reload() {
	cfg, err := config.Load()
	if err != nil {
		c.SessionErr = err
		return
	}
	c.Cfg = cfg
	c.Session, c.SessionErr = core.OpenSession(cfg, c.ProfileFlag, core.ClientOptions{Version: c.Version})
	c.Verified, c.VerifyDone, c.VerifyErr = false, false, nil
	i18n.Set(i18n.Detect(cfg.UILanguage))
	ui.SetTheme(cfg.Theme)
}

// verifySession checks the active profile's credentials in the background.
func verifySession(ctx *Context) tea.Cmd {
	if ctx.Session == nil {
		return nil
	}
	client, name := ctx.Session.Client, ctx.Session.Name
	return func() tea.Msg {
		c, cancel := context.WithTimeout(reqlog.WithoutLog(context.Background()), 10*time.Second)
		defer cancel()
		return sessionVerifiedMsg{name: name, err: client.Verify(c)}
	}
}

// IsProd reports whether the active profile targets production.
func (c *Context) IsProd() bool {
	return c.Session != nil && c.Session.Profile.IsProd()
}

// Messages between screens and the root model.
type (
	switchMsg struct {
		id   string
		data any
	}
	sessionChangedMsg  struct{}
	focusMenuMsg       struct{}
	sessionVerifiedMsg struct {
		name string
		err  error
	}
	splashTickMsg struct{}
	toastMsg      struct {
		text string
		tone ui.Tone
	}
	clearToastMsg struct{ id int }
)

func switchTo(id string, data any) tea.Cmd {
	return func() tea.Msg { return switchMsg{id: id, data: data} }
}

func sessionChanged() tea.Cmd {
	return func() tea.Msg { return sessionChangedMsg{} }
}

func toast(text string, tone ui.Tone) tea.Cmd {
	return func() tea.Msg { return toastMsg{text: text, tone: tone} }
}

func clearToastAfter(id int) tea.Cmd {
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg { return clearToastMsg{id: id} })
}
