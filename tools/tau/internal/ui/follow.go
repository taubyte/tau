package ui

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Chunks is a log read a chunk at a time (api.LogStream). Next blocks, so it
// is only ever called from a tea.Cmd.
type Chunks interface {
	Next() ([]byte, error)
	Offset() int64
}

type chunkMsg []byte
type endMsg struct{ err error }

func readNext(c Chunks) tea.Cmd {
	return func() tea.Msg {
		chunk, err := c.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = nil
			}
			return endMsg{err: err}
		}
		return chunkMsg(chunk)
	}
}

type followKeys struct {
	Up, Down, Bottom, Quit key.Binding
}

func (k followKeys) ShortHelp() []key.Binding  { return []key.Binding{k.Up, k.Down, k.Bottom, k.Quit} }
func (k followKeys) FullHelp() [][]key.Binding { return [][]key.Binding{k.ShortHelp()} }

var defaultFollowKeys = followKeys{
	Up:     key.NewBinding(key.WithKeys("up", "k", "pgup"), key.WithHelp("↑/k", "scroll up")),
	Down:   key.NewBinding(key.WithKeys("down", "j", "pgdown"), key.WithHelp("↓/j", "scroll down")),
	Bottom: key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G", "follow")),
	Quit:   key.NewBinding(key.WithKeys("q", "ctrl+c", "esc"), key.WithHelp("q", "quit")),
}

// Follow is the live log view: the log in a viewport that stays pinned to the
// bottom until the developer scrolls up, a spinner while the job runs, and the
// key help.
type Follow struct {
	title  string
	source Chunks
	log    strings.Builder
	view   viewport.Model
	spin   spinner.Model
	help   help.Model
	keys   followKeys
	ready  bool
	pinned bool
	done   bool
	Err    error
	theme  Theme
}

// NewFollow builds the model for source, titled title.
func NewFollow(title string, source Chunks, theme Theme) *Follow {
	return &Follow{
		title:  title,
		source: source,
		spin:   spinner.New(spinner.WithSpinner(spinner.Dot)),
		help:   help.New(),
		keys:   defaultFollowKeys,
		pinned: true,
		theme:  theme,
	}
}

// Init starts the spinner and the first read.
func (m *Follow) Init() tea.Cmd { return tea.Batch(m.spin.Tick, readNext(m.source)) }

// Update is the model's one state transition.
func (m *Follow) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		height := max(msg.Height-3, 1)
		if !m.ready {
			m.view = viewport.New(msg.Width, height)
			m.ready = true
		} else {
			m.view.Width, m.view.Height = msg.Width, height
		}
		m.help.Width = msg.Width
		m.refresh()
		return m, nil
	case chunkMsg:
		m.log.Write(msg)
		m.refresh()
		return m, readNext(m.source)
	case endMsg:
		m.done, m.Err = true, msg.err
		return m, nil
	case spinner.TickMsg:
		if m.done {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Bottom):
			m.pinned = true
			m.view.GotoBottom()
			return m, nil
		case key.Matches(msg, m.keys.Up):
			m.pinned = false
		}
	}
	var cmd tea.Cmd
	m.view, cmd = m.view.Update(msg)
	if m.view.AtBottom() {
		m.pinned = true
	}
	return m, cmd
}

func (m *Follow) refresh() {
	if !m.ready {
		return
	}
	m.view.SetContent(m.log.String())
	if m.pinned {
		m.view.GotoBottom()
	}
}

// View draws the log, the status line and the help.
func (m *Follow) View() string {
	if !m.ready {
		return m.spin.View() + " connecting…"
	}
	status := m.spin.View() + " " + m.theme.Title.Render(m.title) + m.theme.Muted.Render(fmt.Sprintf("  %d bytes", m.source.Offset()))
	switch {
	case m.Err != nil:
		status = m.theme.Bad.Render("✗ "+m.Err.Error()) + "  " + m.theme.Title.Render(m.title)
	case m.done:
		status = m.theme.OK.Render("✓ log complete") + "  " + m.theme.Title.Render(m.title)
	}
	return lipgloss.JoinVertical(lipgloss.Left, m.view.View(), status, m.help.View(m.keys))
}

// Log is everything read so far.
func (m *Follow) Log() string { return m.log.String() }

// Done reports whether the log ended.
func (m *Follow) Done() bool { return m.done }
