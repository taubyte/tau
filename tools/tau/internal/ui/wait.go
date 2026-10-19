package ui

import (
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// Poll is one look at whatever is being waited for: done once it has arrived,
// with status to show meanwhile.
type Poll func() (status string, done bool, err error)

type pollMsg struct {
	status string
	done   bool
	err    error
}

type tickMsg struct{}

// Wait is a spinner beside a status line, polling until done.
type Wait struct {
	title    string
	poll     Poll
	every    time.Duration
	spin     spinner.Model
	status   string
	done     bool
	Err      error
	theme    Theme
	quit     key.Binding
	Canceled bool
}

// NewWait builds the model, polling every interval.
func NewWait(title string, every time.Duration, poll Poll, theme Theme) *Wait {
	return &Wait{
		title: title, poll: poll, every: every, theme: theme,
		spin: spinner.New(spinner.WithSpinner(spinner.Dot)),
		quit: key.NewBinding(key.WithKeys("q", "ctrl+c", "esc")),
	}
}

func (m *Wait) check() tea.Cmd {
	return func() tea.Msg {
		status, done, err := m.poll()
		return pollMsg{status: status, done: done, err: err}
	}
}

// Init polls at once and starts the spinner.
func (m *Wait) Init() tea.Cmd { return tea.Batch(m.spin.Tick, m.check()) }

// Update is the model's one state transition.
func (m *Wait) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case pollMsg:
		m.status, m.done, m.Err = msg.status, msg.done, msg.err
		if m.done || m.Err != nil {
			return m, tea.Quit
		}
		return m, tea.Tick(m.every, func(time.Time) tea.Msg { return tickMsg{} })
	case tickMsg:
		return m, m.check()
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		if key.Matches(msg, m.quit) {
			m.Canceled = true
			return m, tea.Quit
		}
	}
	return m, nil
}

// View draws the spinner and status.
func (m *Wait) View() string {
	switch {
	case m.Err != nil:
		return m.theme.Bad.Render("✗ "+m.Err.Error()) + "\n"
	case m.done:
		return m.theme.OK.Render("✓ ") + m.status + "\n"
	}
	return m.spin.View() + " " + m.theme.Title.Render(m.title) + " " + m.theme.Muted.Render(m.status) + "\n"
}

// Status is the last status seen.
func (m *Wait) Status() string { return m.status }

// Done reports whether the wait ended with its thing arrived.
func (m *Wait) Done() bool { return m.done }
