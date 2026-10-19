package query_test

import (
	"bytes"
	"context"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taubyte/tau/pkg/stats"
	"github.com/taubyte/tau/tools/taucorder/internal/fixture"
	"github.com/taubyte/tau/tools/taucorder/internal/query"
)

var update = flag.Bool("update", false, "rewrite golden files")

type fleet []stats.Node

func (f fleet) Nodes() []stats.Node { return f }

// Fleet is a fixed fleet for golden output.
var Fleet = fleet{
	{Peer: "12D3KooWBbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Ready: true, Load: 0.25, Age: 3 * time.Second, Serving: []string{"register", "worker"}, Addresses: []string{"10.0.0.2"}},
	{Peer: "12D3KooWAaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Ready: false, Load: 0.9, Age: 21 * time.Second, Serving: []string{"register"}},
}

func client(t *testing.T) (*query.Client, *fixture.Cloud) {
	t.Helper()
	cloud := fixture.NewCloud()
	mux := http.NewServeMux()
	for _, m := range []func() (string, http.Handler){cloud.Register, cloud.Auth, cloud.Build} {
		name, h := m()
		mux.Handle("/"+name+"/", h)
	}
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return query.New(Fleet, http.DefaultClient, s.URL), cloud
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s differs:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func render(t *testing.T, format string, tab query.Table, v any) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := query.Write(&b, format, tab, v); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestGolden(t *testing.T) {
	c, _ := client(t)
	ctx := context.Background()

	nodes := c.Nodes()
	golden(t, "nodes.txt", render(t, "table", query.NodesTable(nodes), nodes))
	golden(t, "nodes.json", render(t, "json", query.NodesTable(nodes), nodes))

	svcs := c.Services()
	golden(t, "services.txt", render(t, "", query.ServicesTable(svcs), svcs))
	golden(t, "services.json", render(t, "json", query.ServicesTable(svcs), svcs))

	projects, err := c.Projects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "projects.txt", render(t, "table", query.ProjectsTable(projects), projects))
	golden(t, "projects.json", render(t, "json", query.ProjectsTable(projects), projects))

	jobs, err := c.Jobs(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "jobs.txt", render(t, "table", query.JobsTable(jobs), jobs))
	golden(t, "jobs.json", render(t, "json", query.JobsTable(jobs), jobs))

	one, err := c.Jobs(ctx, "p2")
	if err != nil || len(one) != 1 || one[0].Status != "running" {
		t.Fatal(one, err)
	}
}

func TestErrors(t *testing.T) {
	ctx := context.Background()
	if err := query.Write(&bytes.Buffer{}, "yaml", query.Table{}, nil); err == nil {
		t.Fatal("yaml")
	}
	c, cloud := client(t)
	if _, err := c.Job(ctx, ""); err == nil {
		t.Fatal("empty job id")
	}
	if _, err := c.Job(ctx, "nope"); err == nil {
		t.Fatal("missing job")
	}
	// A project that is gone is no longer listed.
	delete(cloud.Projects, "p1")
	if _, err := c.Projects(ctx); err != nil {
		t.Fatal(err)
	}
	cloud.Ghosts = map[string][]string{"p2": {"missing"}}
	if _, err := c.Jobs(ctx, "p2"); err == nil {
		t.Fatal("job listed but not gettable")
	}

	dead := query.New(Fleet, http.DefaultClient, "http://127.0.0.1:1")
	if _, err := dead.Projects(ctx); err == nil {
		t.Fatal("projects from nowhere")
	}
	if _, err := dead.Jobs(ctx, ""); err == nil {
		t.Fatal("jobs from nowhere")
	}
	if _, err := dead.Jobs(ctx, "p1"); err == nil {
		t.Fatal("jobs of p1 from nowhere")
	}
}

func TestDurationRoundTrips(t *testing.T) {
	var d query.Duration
	if err := d.UnmarshalText([]byte("3s")); err != nil || time.Duration(d) != 3*time.Second {
		t.Fatal(d, err)
	}
	if err := d.UnmarshalText([]byte("x")); err == nil {
		t.Fatal("parsed x")
	}
}

func TestShortAndStatus(t *testing.T) {
	if query.Short("abc") != "abc" {
		t.Fatal("short")
	}
	tab := query.JobsTable([]query.Job{{ID: "x", Commit: "abc"}})
	if tab.Rows[0][5] != "abc" {
		t.Fatal(tab.Rows)
	}
}
