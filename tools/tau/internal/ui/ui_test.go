package ui

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"

	plmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/session"
	"github.com/taubyte/tau/tools/tau/internal/project"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func plain() Theme { return NewTheme(&bytes.Buffer{}) }

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "run with -update to create it")
	require.Equal(t, string(want), got)
}

func TestReportRendersFindingsWorstFirst(t *testing.T) {
	r := project.Report{
		Findings: []session.Note{
			{Severity: "note", Kind: "worker", Name: "drain", Title: "a queue consumer no ingress names"},
			{Severity: "broken", Kind: "service", Name: "web", Scope: "shop", Title: "no ingress names it", Detail: "an http service\nanswers nobody"},
			{Severity: "check", Kind: "gateway", Name: "front", Title: "probably not what was meant"},
		},
		Deferred: []engine.NextValidation{{Validator: "dns", Value: "example.com"}},
	}
	require.True(t, r.Broken())
	golden(t, "report-findings", plain().Report(r))

	golden(t, "report-error", plain().Report(project.Report{Error: "/function/a.yaml:1:1: required attribute 'x'\nsecond line"}))
	require.Contains(t, plain().Report(project.Report{}), "✓ compiles")
}

func TestJobRendering(t *testing.T) {
	th := plain()
	for _, s := range []plmv2.JobStatus{plmv2.JobStatus_JobStatusOpen, plmv2.JobStatus_JobStatusClaimed, plmv2.JobStatus_JobStatusSuccess, plmv2.JobStatus_JobStatusFailed, plmv2.JobStatus_JobStatusCancelled} {
		require.Equal(t, Status(s), th.status(s))
	}
	require.Equal(t, "-", when(0))
	require.Contains(t, th.Job(&plmv2.Job{Id: "j"}), "queued      -")
}

func TestViewsRendering(t *testing.T) {
	th := plain()
	whole := session.Viewed{Views: []session.View{{Object: map[string]any{"a": 1, "b": 2}}}}
	require.Equal(t, "everywhere\n  2 keys, every resource whole\n", th.Views(whole))

	named := session.Viewed{Views: []session.View{{
		Destination: "region-1",
		Roles:       map[string]string{"id1": "anywhere", "id2": "needed", "id3": "needed"},
		Object: map[string]any{"object": map[string]any{
			"function": map[string]any{"id1": map[string]any{"name": "hello"}, "bad": "x"},
			"zone":     map[string]any{"id2": map[string]any{"name": "z"}},
			"name":     "not-a-kind",
		}},
	}}}
	golden(t, "views-named", th.Views(named))
}

func TestIssuesAndKinds(t *testing.T) {
	th := plain()
	require.Equal(t, "• execution.call  required\n", th.Issues([]project.Issue{{Field: []string{"execution", "call"}, Message: "required"}}))
	require.Contains(t, th.Kinds([]session.Kind{{Name: "namespace", Container: true}, {Name: "function"}}), "(a scope")
}

type chunks struct {
	parts  []string
	err    error
	offset int64
}

func (c *chunks) Line() ([]byte, error) {
	if len(c.parts) == 0 {
		if c.err != nil {
			return nil, c.err
		}
		return nil, io.EOF
	}
	p := c.parts[0]
	c.parts = c.parts[1:]
	c.offset += int64(len(p))
	return []byte(p), nil
}

func (c *chunks) Offset() int64 { return c.offset }

func press(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestFollowAppendsChunksAndStaysPinned(t *testing.T) {
	src := &chunks{parts: []string{"one\n", "two\n"}}
	m := NewFollow("job j", src, plain())
	require.Contains(t, m.View(), "connecting")
	require.NotNil(t, m.Init())

	m.Update(tea.WindowSizeMsg{Width: 40, Height: 6})
	m.Update(tea.WindowSizeMsg{Width: 50, Height: 8})

	// Each chunk is written, and asks for the next read.
	for {
		msg := readNext(src)()
		_, cmd := m.Update(msg)
		if _, end := msg.(endMsg); end {
			break
		}
		require.NotNil(t, cmd)
	}
	require.Equal(t, "one\ntwo\n", m.Log())
	require.True(t, m.Done())
	require.Contains(t, m.View(), "log complete")
	require.Contains(t, m.View(), "G follow")
	require.Len(t, m.keys.FullHelp()[0], 4)

	// A done view stops animating.
	_, cmd := m.Update(spinner.TickMsg{})
	require.Nil(t, cmd)

	// Scrolling up unpins; G pins again; q quits.
	m.Update(press("k"))
	m.Update(press("G"))
	require.True(t, m.pinned)
	m.Update(press("j"))
	_, cmd = m.Update(press("q"))
	require.Equal(t, tea.QuitMsg{}, cmd())
}

func TestFollowShowsAFailedStream(t *testing.T) {
	src := &chunks{err: errors.New("gone")}
	m := NewFollow("job j", src, plain())
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 6})
	_, cmd := m.Update(spinner.TickMsg{ID: m.spin.ID()})
	require.NotNil(t, cmd)
	m.Update(readNext(src)())
	require.EqualError(t, m.Err, "gone")
	require.Contains(t, m.View(), "✗ gone")
	require.Contains(t, m.View(), "job j")
}

func TestFollowBeforeItsSize(t *testing.T) {
	m := NewFollow("job j", &chunks{parts: []string{"x"}}, plain())
	m.Update(chunkMsg("x"))
	require.Equal(t, "x", m.Log())
	require.Contains(t, m.View(), "connecting")
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 6})
	require.Contains(t, m.View(), "x")
	require.Contains(t, m.View(), "job j")
}

func TestWaitPollsUntilDone(t *testing.T) {
	n := 0
	m := NewWait("waiting", time.Millisecond, func() (string, bool, error) {
		n++
		return "look " + string(rune('0'+n)), n == 2, nil
	}, plain())
	require.NotNil(t, m.Init())

	_, cmd := m.Update(m.check()())
	require.NotNil(t, cmd, "not done: ticks again")
	require.Contains(t, m.View(), "look 1")
	_, cmd = m.Update(tickMsg{})
	_, cmd = m.Update(cmd())
	require.Equal(t, tea.QuitMsg{}, cmd())
	require.True(t, m.Done())
	require.Equal(t, "look 2", m.Status())
	require.Contains(t, m.View(), "✓ look 2")

	_, cmd = m.Update(spinner.TickMsg{ID: m.spin.ID()})
	require.NotNil(t, cmd)
	_, cmd = m.Update(press("x"))
	require.Nil(t, cmd)
}

func TestWaitFailsAndCanBeStopped(t *testing.T) {
	m := NewWait("waiting", time.Millisecond, func() (string, bool, error) { return "", false, errors.New("down") }, plain())
	m.Update(m.check()())
	require.EqualError(t, m.Err, "down")
	require.Contains(t, m.View(), "✗ down")

	m = NewWait("waiting", time.Millisecond, func() (string, bool, error) { return "", false, nil }, plain())
	_, cmd := m.Update(press("q"))
	require.True(t, m.Canceled)
	require.Equal(t, tea.QuitMsg{}, cmd())
}

func TestHuhPrompts(t *testing.T) {
	h := func(in string) Huh { return Huh{In: strings.NewReader(in), Out: io.Discard, Accessible: true} }

	v, err := h("hello\n").Input("Name", "", nil)
	require.NoError(t, err)
	require.Equal(t, "hello", v)

	v, err = h("bad\ngood\n").Input("Name", "", func(s string) error {
		if s == "bad" {
			return errors.New("no")
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, "good", v)

	// huh reads an accessible password from the terminal itself, never from
	// the input it was given, so off a terminal it answers empty.
	_, err = h("s3cret\n").Secret("Token", "")
	require.NoError(t, err)

	ok, err := h("y\n").Confirm("Sure?")
	require.NoError(t, err)
	require.True(t, ok)

	v, err = h("2\n").Select("Kind", []string{"a", "b"})
	require.NoError(t, err)
	require.Equal(t, "b", v)

}

func TestTheJobAndListRenderers(t *testing.T) {
	th := plain()
	require.Contains(t, th.Jobs(nil), "no jobs yet")
	jobs := th.Jobs([]*plmv2.Job{
		{Id: "old", Status: plmv2.JobStatus_JobStatusSuccess, CommitSha: "0123456789abcdef0123", Timestamp: 1700000000, Attempt: 1},
		{Id: "new", Status: plmv2.JobStatus_JobStatusFailed, CommitSha: "abc"},
	})
	require.Less(t, strings.Index(jobs, "old"), strings.Index(jobs, "new"), "newest first, and a job never queued sorts last")
	require.Contains(t, jobs, "0123456789ab")
	require.Contains(t, jobs, "2023-11-14T22:13:20Z")
	require.Contains(t, jobs, "-")

	one := th.Job(&plmv2.Job{Id: "j", Status: plmv2.JobStatus_JobStatusCancelled, AssetCid: map[string]string{"b": "2", "a": "1"}})
	require.Less(t, strings.Index(one, "a → 1"), strings.Index(one, "b → 2"))

	table := th.Table([]string{"A", "B"}, [][]string{{"one", "two"}})
	require.Contains(t, table, "one")
	require.Contains(t, table, "B")

	require.Contains(t, th.Secrets("prod", nil), "no secrets set in prod")
	listed := th.Secrets("prod", []string{"Z", "A"})
	require.Less(t, strings.Index(listed, "A"), strings.Index(listed, "Z"))
	require.Equal(t, "✓ built\n", th.Done("built"))
}
