// Package tui is `taucorder ui`: the operator's view of a cloud, as a
// bubbletea program.
//
// It is the Elm architecture as bubbletea draws it. All I/O is a tea.Cmd
// returning a message (loadedMsg); Update is the only place the model changes;
// View is a pure function of the model. Widgets are bubbles components —
// table for every list, viewport for drill-down, spinner while loading, help
// over key.Bindings — styled with lipgloss.
//
// Every view reads through the same query.Client the non-interactive commands
// print from, so `taucorder nodes` and the Fleet tab cannot disagree.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/taubyte/tau/tools/taucorder/internal/query"
)

type Source interface {
	Nodes() []query.Node
	Services() []query.Service
	Projects(ctx context.Context) ([]query.Project, error)
	Jobs(ctx context.Context, project string) ([]query.Job, error)
}

type Tab int

const (
	Fleet Tab = iota
	Services
	Projects
	Jobs
)

var tabNames = []string{"Fleet", "Services", "Projects", "Jobs"}

func (t Tab) String() string { return tabNames[t] }

type keyMap struct {
	Next, Prev, Refresh, Open, Back, Quit key.Binding
	Tab1, Tab2, Tab3, Tab4                key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Next, k.Open, k.Back, k.Refresh, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Next, k.Prev, k.Tab1, k.Tab2, k.Tab3, k.Tab4}, {k.Open, k.Back, k.Refresh, k.Quit}}
}

var keys = keyMap{
	Next:    key.NewBinding(key.WithKeys("tab", "right", "l"), key.WithHelp("tab", "next view")),
	Prev:    key.NewBinding(key.WithKeys("shift+tab", "left", "h"), key.WithHelp("shift+tab", "previous view")),
	Refresh: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
	Open:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open")),
	Back:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	Tab1:    key.NewBinding(key.WithKeys("1"), key.WithHelp("1", "fleet")),
	Tab2:    key.NewBinding(key.WithKeys("2"), key.WithHelp("2", "services")),
	Tab3:    key.NewBinding(key.WithKeys("3"), key.WithHelp("3", "projects")),
	Tab4:    key.NewBinding(key.WithKeys("4"), key.WithHelp("4", "jobs")),
}

var (
	activeTab   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212")).Padding(0, 1).Underline(true)
	inactiveTab = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Padding(0, 1)
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	boxStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240"))
)

type loadedMsg struct {
	tab   Tab
	table query.Table
	rows  any
	err   error
}

type tickMsg time.Time

type Model struct {
	src      Source
	interval time.Duration
	timeout  time.Duration

	active  Tab
	tables  [4]table.Model
	rows    [4]any
	errs    [4]error
	loading [4]bool

	// project filters the Jobs view, set by opening a project.
	project string

	detail     viewport.Model
	detailOpen bool
	detailName string

	spinner spinner.Model
	help    help.Model
	width   int
	height  int
}

// New is a model reading src, refreshing every interval (zero: never).
func New(src Source, interval time.Duration) Model {
	m := Model{src: src, interval: interval, timeout: 15 * time.Second}
	for i := range m.tables {
		t := table.New(table.WithFocused(true), table.WithHeight(10))
		t.SetStyles(tableStyles())
		m.tables[i] = t
	}
	m.spinner = spinner.New(spinner.WithSpinner(spinner.Dot))
	m.help = help.New()
	m.detail = viewport.New(80, 10)
	return m
}

func tableStyles() table.Styles {
	s := table.DefaultStyles()
	s.Header = s.Header.BorderStyle(lipgloss.NormalBorder()).BorderBottom(true).Bold(true)
	s.Selected = s.Selected.Foreground(lipgloss.Color("229")).Background(lipgloss.Color("57"))
	return s
}

// Init starts the spinner, loads every view, and starts the clock.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick, m.loadAll()}
	if m.interval > 0 {
		cmds = append(cmds, m.tick())
	}
	return tea.Batch(cmds...)
}

func (m Model) tick() tea.Cmd {
	return tea.Tick(m.interval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) loadAll() tea.Cmd {
	var cmds []tea.Cmd
	for t := Fleet; t <= Jobs; t++ {
		cmds = append(cmds, m.load(t))
	}
	return tea.Batch(cmds...)
}

// load is the tea.Cmd fetching one view.
func (m *Model) load(t Tab) tea.Cmd {
	m.loading[t] = true
	src, project, timeout := m.src, m.project, m.timeout
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		switch t {
		case Fleet:
			ns := src.Nodes()
			return loadedMsg{tab: t, table: query.NodesTable(ns), rows: ns}
		case Services:
			ss := src.Services()
			return loadedMsg{tab: t, table: query.ServicesTable(ss), rows: ss}
		case Projects:
			ps, err := src.Projects(ctx)
			return loadedMsg{tab: t, table: query.ProjectsTable(ps), rows: ps, err: err}
		default:
			js, err := src.Jobs(ctx, project)
			return loadedMsg{tab: t, table: query.JobsTable(js), rows: js, err: err}
		}
	}
}

// Update is the only place the model changes.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.Width = msg.Width
		for i := range m.tables {
			m.tables[i].SetWidth(msg.Width)
			m.tables[i].SetHeight(max(msg.Height-8, 3))
		}
		m.detail.Width = max(msg.Width-2, 10)
		m.detail.Height = max(msg.Height-8, 3)
		return m, nil

	case loadedMsg:
		m.loading[msg.tab] = false
		m.errs[msg.tab] = msg.err
		if msg.err == nil {
			m.rows[msg.tab] = msg.rows
			setTable(&m.tables[msg.tab], msg.table, m.width)
		}
		return m, nil

	case tickMsg:
		return m, tea.Batch(m.load(m.active), m.tick())

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, keys.Back):
		if m.detailOpen {
			m.detailOpen = false
			return m, nil
		}
		if m.active == Jobs && m.project != "" {
			m.project = ""
			return m, m.load(Jobs)
		}
		return m, nil
	}

	if m.detailOpen {
		var cmd tea.Cmd
		m.detail, cmd = m.detail.Update(msg)
		return m, cmd
	}

	switch {
	case key.Matches(msg, keys.Next):
		m.active = (m.active + 1) % 4
		return m, nil
	case key.Matches(msg, keys.Prev):
		m.active = (m.active + 3) % 4
		return m, nil
	case key.Matches(msg, keys.Tab1):
		m.active = Fleet
		return m, nil
	case key.Matches(msg, keys.Tab2):
		m.active = Services
		return m, nil
	case key.Matches(msg, keys.Tab3):
		m.active = Projects
		return m, nil
	case key.Matches(msg, keys.Tab4):
		m.active = Jobs
		return m, nil
	case key.Matches(msg, keys.Refresh):
		return m, m.load(m.active)
	case key.Matches(msg, keys.Open):
		return m.open()
	}

	var cmd tea.Cmd
	m.tables[m.active], cmd = m.tables[m.active].Update(msg)
	return m, cmd
}

// open drills into the selected row: a project opens its jobs, anything else
// opens its detail.
func (m Model) open() (tea.Model, tea.Cmd) {
	i := m.tables[m.active].Cursor()
	switch rows := m.rows[m.active].(type) {
	case []query.Project:
		if i < 0 || i >= len(rows) {
			return m, nil
		}
		m.project = rows[i].ID
		m.active = Jobs
		return m, m.load(Jobs)
	case []query.Node:
		if i >= 0 && i < len(rows) {
			n := rows[i]
			m.openDetail("Node "+query.Short(n.Peer), fields(
				"peer", n.Peer, "ready", fmt.Sprint(n.Ready), "load", fmt.Sprintf("%.2f", n.Load),
				"age", n.Age.String(), "serving", strings.Join(n.Serving, "\n"),
				"addresses", strings.Join(n.Address, "\n"),
				"databases pending", fmt.Sprint(n.Pending),
			))
		}
	case []query.Service:
		if i >= 0 && i < len(rows) {
			s := rows[i]
			m.openDetail("Service "+s.Name, fields("name", s.Name, "ready", fmt.Sprintf("%d of %d", s.Ready, len(s.Nodes)), "nodes", strings.Join(s.Nodes, "\n")))
		}
	case []query.Job:
		if i >= 0 && i < len(rows) {
			j := rows[i]
			m.openDetail("Job "+j.ID, fields(
				"id", j.ID, "project", j.Project, "status", j.Status, "repository", j.Repository,
				"ref", j.Ref, "commit", j.Commit, "attempt", fmt.Sprint(j.Attempt), "time", j.Time.Format(time.RFC3339),
			))
		}
	}
	return m, nil
}

func (m *Model) openDetail(name, body string) {
	m.detailName = name
	m.detail.SetContent(body)
	m.detail.GotoTop()
	m.detailOpen = true
}

var labelStyle = lipgloss.NewStyle().Bold(true).Width(18)

func fields(kv ...string) string {
	var rows []string
	for i := 0; i+1 < len(kv); i += 2 {
		v := kv[i+1]
		if v == "" {
			v = "-"
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, labelStyle.Render(kv[i]), v))
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func setTable(t *table.Model, q query.Table, width int) {
	widths := make([]int, len(q.Headers))
	for i, h := range q.Headers {
		widths[i] = lipgloss.Width(h)
	}
	for _, r := range q.Rows {
		for i, c := range r {
			widths[i] = min(max(widths[i], lipgloss.Width(c)), 40)
		}
	}
	cols := make([]table.Column, len(q.Headers))
	for i, h := range q.Headers {
		cols[i] = table.Column{Title: h, Width: widths[i]}
	}
	rows := make([]table.Row, len(q.Rows))
	for i, r := range q.Rows {
		rows[i] = table.Row(r)
	}
	// Each view's columns are fixed, and its table starts with no rows, so
	// columns are always set before the rows that must fit them.
	t.SetColumns(cols)
	t.SetRows(rows)
	if width > 0 {
		t.SetWidth(width)
	}
}

func (m Model) View() string {
	var tabs []string
	for t := Fleet; t <= Jobs; t++ {
		name := t.String()
		if t == Jobs && m.project != "" {
			name += " · " + m.project
		}
		if t == m.active {
			tabs = append(tabs, activeTab.Render(name))
		} else {
			tabs = append(tabs, inactiveTab.Render(name))
		}
	}
	header := lipgloss.JoinHorizontal(lipgloss.Top, titleStyle.Render("taucorder"), "  ", lipgloss.JoinHorizontal(lipgloss.Top, tabs...))

	var body string
	switch {
	case m.detailOpen:
		body = titleStyle.Render(m.detailName) + "\n" + boxStyle.Render(m.detail.View())
	case m.errs[m.active] != nil:
		body = errStyle.Render("error: " + m.errs[m.active].Error())
	case m.rows[m.active] == nil:
		body = m.spinner.View() + " loading " + m.active.String() + "…"
	case len(m.tables[m.active].Rows()) == 0:
		body = dimStyle.Render("nothing to show")
	default:
		body = m.tables[m.active].View()
	}

	status := ""
	if m.loading[m.active] && m.rows[m.active] != nil {
		status = m.spinner.View() + " refreshing"
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, "", body, "", dimStyle.Render(status), m.help.View(keys))
}

// Options are the program options `taucorder ui` runs with.
func Options() []tea.ProgramOption { return []tea.ProgramOption{tea.WithAltScreen()} }
