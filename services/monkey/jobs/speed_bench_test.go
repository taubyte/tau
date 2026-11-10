//go:build speedbench

package jobs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	"github.com/taubyte/tau/core/services/monkey"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/p2p/peer"
	tnsMocks "github.com/taubyte/tau/services/tns/mocks"
	"gotest.tools/v3/assert"
)

type benchMonkey struct {
	monkey.Service
	dev bool
}

func (m benchMonkey) Dev() bool                    { return m.dev }
func (m benchMonkey) Hoarder() hoarderIface.Client { return benchHoarder{} }

type benchHoarder struct{ hoarderIface.Client }

func (benchHoarder) Stash(cid string, data io.Reader, opts ...hoarderIface.StashOption) error {
	_, err := io.Copy(io.Discard, data)
	return err
}

// TestCodeJobSpeed times the code job's build of every function in a project,
// the way a push runs it. SPEEDBENCH_FUNCS holds function directories; each
// is used twice (the copy with a one-line change) so the project has twice as
// many functions. SPEEDBENCH_DEV=1 runs it as a development node does, and
// SPEEDBENCH_ONE=1 stops after the first push.
func TestCodeJobSpeed(t *testing.T) {
	p := newBenchProject(t, "")

	p.push("first push")
	if p.dev && os.Getenv("SPEEDBENCH_ONE") == "" {
		p.push("push, nothing changed")
		p.push("push, one function edited", p.ops[0].name)
		p.push("push, two functions edited", p.ops[1].name, p.ops[2].name)
	}
}

// TestConcurrentProjectsSpeed pushes SPEEDBENCH_PROJECTS projects at once,
// each with sources of its own, as several pushes landing together do.
func TestConcurrentProjectsSpeed(t *testing.T) {
	n, err := strconv.Atoi(os.Getenv("SPEEDBENCH_PROJECTS"))
	assert.NilError(t, err)

	projects := make([]*benchProject, n)
	for i := range projects {
		projects[i] = newBenchProject(t, fmt.Sprintf("p%d", i))
	}

	start := time.Now()
	var wg sync.WaitGroup
	for _, p := range projects {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.push("pushed together")
		}()
	}
	wg.Wait()
	t.Logf("%d projects x %d functions, all done in %.1fs", n, len(projects[0].ops), time.Since(start).Seconds())
}

type benchProject struct {
	t     *testing.T
	ctx   context.Context
	dev   bool
	tag   string
	src   string
	names []string
	ops   []Op
	node  peer.Node
	tns   tns.Client
}

// newBenchProject is a project with every directory of SPEEDBENCH_FUNCS as a
// function, twice: as is and as a copy. tag makes its sources differ from
// another project's.
func newBenchProject(t *testing.T, tag string) *benchProject {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	p := &benchProject{
		t:    t,
		ctx:  ctx,
		dev:  os.Getenv("SPEEDBENCH_DEV") == "1",
		tag:  tag,
		src:  os.Getenv("SPEEDBENCH_FUNCS"),
		node: peer.Mock(ctx),
		tns:  tnsMocks.New(),
	}
	entries, err := os.ReadDir(p.src)
	assert.NilError(t, err)
	for _, e := range entries {
		for _, suffix := range []string{"", "_copy"} {
			name := e.Name() + suffix
			p.names = append(p.names, e.Name())
			p.ops = append(p.ops, Op{id: name, name: name, pathVariable: "functions"})
		}
	}
	return p
}

// clone lays the project out afresh, as each job's git clone does; edited
// names the functions whose sources this push changed.
func (p *benchProject) clone(edited ...string) string {
	root := p.t.TempDir()
	for i, op := range p.ops {
		dst := filepath.Join(root, "functions", op.name)
		assert.NilError(p.t, os.CopyFS(dst, os.DirFS(filepath.Join(p.src, p.names[i]))))
		note := "// " + p.tag + op.name + "\n"
		for _, name := range edited {
			if name == op.name {
				note += fmt.Sprintf("// edited %d\n", time.Now().UnixNano())
			}
		}
		f, err := os.OpenFile(filepath.Join(dst, "go.mod"), os.O_APPEND|os.O_WRONLY, 0)
		assert.NilError(p.t, err)
		f.WriteString(note)
		f.Close()
	}
	return root
}

func (p *benchProject) push(label string, edited ...string) {
	logFile, err := os.CreateTemp("", "speedbench-log-*")
	assert.NilError(p.t, err)
	defer os.Remove(logFile.Name())

	job := &patrick.Job{Id: p.tag + label, Logs: map[string]string{}, AssetCid: map[string]string{}}
	job.Meta.Repository.Branch = "main"

	c := code{Context{
		Node:      p.node,
		Tns:       p.tns,
		Monkey:    benchMonkey{dev: p.dev},
		Job:       job,
		LogFile:   logFile,
		ProjectID: "speedbench" + p.tag,
		gitDir:    p.clone(edited...),
	}}
	c.ForceContext(p.ctx)

	start := time.Now()
	err = c.handleOps(p.ops)
	took := time.Since(start)
	if err != nil {
		logFile.Seek(0, io.SeekStart)
		log, _ := io.ReadAll(logFile)
		p.t.Errorf("%s failed after %s: %v\n%s", label, took, err, log)
		return
	}
	assert.Equal(p.t, len(job.AssetCid), len(p.ops))
	p.t.Logf("dev=%v %s %-34s %3d functions  %6.1fs", p.dev, p.tag, label, len(p.ops), took.Seconds())
}
