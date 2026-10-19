package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/taubyte/tau/tools/taucorder/internal/query"
)

type src struct {
	err      error
	projects []query.Project
	jobs     map[string][]query.Job
	asked    []string
}

func (s *src) Nodes() []query.Node {
	return []query.Node{{Peer: "12D3KooWAaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Ready: true, Load: 0.5, Serving: []string{"register"}}}
}

func (s *src) Services() []query.Service {
	return []query.Service{{Name: "register", Nodes: []string{"12D3KooWAaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, Ready: 1}}
}

func (s *src) Projects(context.Context) ([]query.Project, error) { return s.projects, s.err }

func (s *src) Jobs(_ context.Context, p string) ([]query.Job, error) {
	s.asked = append(s.asked, p)
	if p == "" {
		var all []query.Job
		for _, js := range s.jobs {
			all = append(all, js...)
		}
		return all, s.err
	}
	return s.jobs[p], s.err
}

func newSrc() *src {
	return &src{
		projects: []query.Project{{ID: "p1", Config: "github/10", Environments: []string{"main"}}},
		jobs:     map[string][]query.Job{"p1": {{ID: "j1", Project: "p1", Status: "success", Commit: "abc"}}},
	}
}

// drain runs cmd and every command its messages produce, as bubbletea's
// runtime would, except the clocks: spinner and refresh ticks are dropped so
// the test never sleeps.
func drain(m tea.Model, cmd tea.Cmd) tea.Model {
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		switch msg := msg.(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
			continue
		case spinner.TickMsg, tickMsg, nil:
			continue
		}
		var next tea.Cmd
		m, next = m.Update(msg)
		queue = append(queue, next)
	}
	return m
}

func press(m tea.Model, keys ...string) tea.Model {
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "shift+tab":
			msg = tea.KeyMsg{Type: tea.KeyShiftTab}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		var cmd tea.Cmd
		m, cmd = m.Update(msg)
		m = drain(m, cmd)
	}
	return m
}

func start(s Source) tea.Model {
	var m tea.Model = New(s, 0)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return drain(m, m.Init())
}

func TestViews(t *testing.T) {
	s := newSrc()
	m := start(s)
	v := m.View()
	for _, want := range []string{"taucorder", "Fleet", "12D3…aaaaaa", "register", "0.50"} {
		if !strings.Contains(v, want) {
			t.Fatalf("fleet view lacks %q:\n%s", want, v)
		}
	}
	m = press(m, "tab")
	if !strings.Contains(m.View(), "SERVICE") {
		t.Fatal(m.View())
	}
	m = press(m, "3")
	if !strings.Contains(m.View(), "github/10") {
		t.Fatal(m.View())
	}
	m = press(m, "4")
	if !strings.Contains(m.View(), "j1") {
		t.Fatal(m.View())
	}
	m = press(m, "shift+tab", "1", "2", "r")
	if !strings.Contains(m.View(), "register") {
		t.Fatal(m.View())
	}
}

func TestDrillDown(t *testing.T) {
	s := newSrc()
	m := start(s)

	// A node opens its detail; esc closes it.
	m = press(m, "enter")
	if !strings.Contains(m.View(), "Node 12D3…aaaaaa") || !strings.Contains(m.View(), "fleet revision") {
		t.Fatal(m.View())
	}
	m = press(m, "down", "esc")
	if strings.Contains(m.View(), "fleet revision") {
		t.Fatal("detail still open")
	}

	// A service opens its detail.
	m = press(m, "2", "enter")
	if !strings.Contains(m.View(), "1 of 1") {
		t.Fatal(m.View())
	}
	m = press(m, "esc")

	// A project opens its jobs; esc drops the filter.
	m = press(m, "3", "enter")
	if !strings.Contains(m.View(), "Jobs · p1") || s.asked[len(s.asked)-1] != "p1" {
		t.Fatal(m.View(), s.asked)
	}
	m = press(m, "enter")
	if !strings.Contains(m.View(), "Job j1") {
		t.Fatal(m.View())
	}
	m = press(m, "esc", "esc")
	if strings.Contains(m.View(), "· p1") || s.asked[len(s.asked)-1] != "" {
		t.Fatal(m.View(), s.asked)
	}
	m = press(m, "esc") // nothing to back out of
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}); cmd == nil || cmd() != tea.Quit() {
		t.Fatal("q does not quit")
	}
}

func TestErrorsAndEmpty(t *testing.T) {
	s := newSrc()
	s.err = errors.New("auth unreachable")
	m := press(start(s), "3")
	if !strings.Contains(m.View(), "error: auth unreachable") {
		t.Fatal(m.View())
	}
	s.err = nil
	s.projects = nil
	m = press(m, "r")
	if !strings.Contains(m.View(), "nothing to show") {
		t.Fatal(m.View())
	}
	m = press(m, "enter") // no row to open
	if strings.Contains(m.View(), "Jobs ·") {
		t.Fatal("opened a project that is not there")
	}
}

func TestLoadingAndTick(t *testing.T) {
	var m tea.Model = New(newSrc(), time.Millisecond)
	if !strings.Contains(m.View(), "loading Fleet") {
		t.Fatal(m.View())
	}
	if m.Init() == nil {
		t.Fatal("no init")
	}
	m, cmd := m.Update(tickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("tick did not reload")
	}
	mm := m.(Model)
	if !mm.loading[Fleet] || !strings.Contains(mm.View(), "loading") {
		t.Fatal("not loading after tick")
	}
	m = drain(m, cmd)
	if !strings.Contains(m.View(), "12D3") {
		t.Fatal(m.View())
	}
	// A refresh in flight over shown rows says so.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if !strings.Contains(m.View(), "refreshing") {
		t.Fatal(m.View())
	}
	_, cmd = m.Update(spinner.TickMsg{})
	if cmd == nil {
		t.Fatal("spinner stopped")
	}
	if m, _ = m.Update(struct{}{}); m == nil {
		t.Fatal("unknown message")
	}
	if Jobs.String() != "Jobs" || len(Options()) != 1 {
		t.Fatal("names/options")
	}
	if len(keys.ShortHelp()) == 0 || len(keys.FullHelp()) == 0 {
		t.Fatal("help")
	}
}
