package ui

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// PollFunc fetches the current state. done=true ends the wait.
type PollFunc func(ctx context.Context) (status string, done bool, err error)

// WaitOptions configures Wait.
type WaitOptions struct {
	URL      string
	Interval time.Duration
	Timeout  time.Duration
	Poll     PollFunc
	Open     func() error
	// Plain disables the live view (e.g. while HTTP logging is on).
	Plain bool
	// Fatal reports errors that should stop polling (e.g. bad credentials).
	Fatal func(error) bool
}

// WaitResult tells how the wait ended.
type WaitResult struct {
	Done     bool
	Stopped  bool
	TimedOut bool
	Err      error
	Status   string
	Elapsed  time.Duration
}

// Wait polls until done, timeout or user abort. In a terminal it shows a live
// status line with shortcuts; otherwise it polls silently.
func Wait(opts WaitOptions) WaitResult {
	if opts.Interval <= 0 {
		opts.Interval = 2 * time.Second
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Minute
	}
	if opts.Plain || !Interactive() || !StderrIsTTY() {
		return waitPlain(opts)
	}
	m := newWaitModel(opts)
	p := tea.NewProgram(m, tea.WithOutput(os.Stderr))
	final, err := p.Run()
	if err != nil {
		return waitPlain(opts)
	}
	return final.(waitModel).result
}

func waitPlain(opts WaitOptions) WaitResult {
	Eprintln(Info(Label.Render("Waiting for the payment to complete (polling every " + opts.Interval.String() + ", ctrl+c to stop) …")))
	start := time.Now()
	var res WaitResult
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		st, done, err := opts.Poll(ctx)
		cancel()
		res.Status, res.Err = st, err
		if err != nil && opts.Fatal != nil && opts.Fatal(err) {
			res.Elapsed = time.Since(start)
			return res
		}
		if done {
			res.Done, res.Err, res.Elapsed = true, nil, time.Since(start)
			return res
		}
		if time.Since(start) > opts.Timeout {
			res.TimedOut, res.Elapsed = true, time.Since(start)
			return res
		}
		time.Sleep(opts.Interval)
	}
}

type pollMsg struct {
	status string
	done   bool
	err    error
}

type waitModel struct {
	opts    WaitOptions
	spin    spinner.Model
	start   time.Time
	status  string
	lastErr error
	notice  string
	result  WaitResult
	quit    bool
}

type clockMsg struct{}

func newWaitModel(opts WaitOptions) waitModel {
	s := spinner.New()
	s.Spinner = spinner.Spinner{Frames: spinnerFrames, FPS: 80 * time.Millisecond}
	s.Style = Accent
	return waitModel{opts: opts, spin: s, start: time.Now()}
}

func (m waitModel) poll(delay time.Duration) tea.Cmd {
	return tea.Tick(delay, func(time.Time) tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		st, done, err := m.opts.Poll(ctx)
		return pollMsg{st, done, err}
	})
}

func (m waitModel) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, m.poll(500*time.Millisecond))
}

func (m waitModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			m.result = WaitResult{Stopped: true, Status: m.status, Elapsed: time.Since(m.start)}
			m.quit = true
			return m, tea.Quit
		case "c":
			if err := clipboard.WriteAll(m.opts.URL); err != nil {
				m.notice = "clipboard unavailable"
			} else {
				m.notice = "URL copied"
			}
		case "o":
			if m.opts.Open != nil {
				if err := m.opts.Open(); err != nil {
					m.notice = "could not open browser"
				} else {
					m.notice = "opened in browser"
				}
			}
		}
		return m, nil
	case pollMsg:
		m.status, m.lastErr = msg.status, msg.err
		if msg.err != nil && m.opts.Fatal != nil && m.opts.Fatal(msg.err) {
			m.result = WaitResult{Err: msg.err, Status: m.status, Elapsed: time.Since(m.start)}
			m.quit = true
			return m, tea.Quit
		}
		if msg.done {
			m.result = WaitResult{Done: true, Status: m.status, Elapsed: time.Since(m.start)}
			m.quit = true
			return m, tea.Quit
		}
		if time.Since(m.start) > m.opts.Timeout {
			m.result = WaitResult{TimedOut: true, Status: m.status, Elapsed: time.Since(m.start)}
			m.quit = true
			return m, tea.Quit
		}
		return m, m.poll(m.opts.Interval)
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m waitModel) View() string {
	if m.quit {
		return ""
	}
	el := time.Since(m.start).Round(time.Second)
	status := m.status
	if status == "" {
		status = "waiting for customer"
	}
	line := fmt.Sprintf(" %s %s  %s  %s", m.spin.View(), Text.Render("Waiting for payment …"),
		Bold.Render(fmt.Sprintf("%d:%02d", int(el.Minutes()), int(el.Seconds())%60)),
		Muted.Render("status "+status+" · polling every "+m.opts.Interval.String()))
	help := "   " + Muted.Render("o open in browser · c copy URL · q stop waiting")
	if m.notice != "" {
		help += "   " + Success.Render(m.notice)
	}
	if m.lastErr != nil {
		help += "\n   " + Warning.Render("last poll: "+m.lastErr.Error())
	}
	return line + "\n" + help + "\n"
}
