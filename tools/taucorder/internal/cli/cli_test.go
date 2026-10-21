package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	tea "github.com/charmbracelet/bubbletea"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"google.golang.org/protobuf/proto"

	"github.com/taubyte/tau/p2p/sectls"
	discoveryv2 "github.com/taubyte/tau/pkg/proto/gen/tau/discovery/v2"
	registerv2 "github.com/taubyte/tau/pkg/proto/gen/tau/register/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/register/v2/registerv2connect"
	"github.com/taubyte/tau/services/discovery"
	"github.com/taubyte/tau/tools/taucorder/internal/bridge"
	"github.com/taubyte/tau/tools/taucorder/internal/fixture"
	"github.com/taubyte/tau/tools/taucorder/internal/query"
)

type seat struct{ *bridge.Bridge }

func (s seat) Close() { s.Bridge.Close() }

type world struct {
	dir  string
	key  []string
	boot string
	join func(context.Context, bridge.Config) (Seat, error)
	got  bridge.Config
}

func gossip(t *testing.T, h host.Host) *pubsub.PubSub {
	ps, err := pubsub.NewGossipSub(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

// newWorld is one node serving register, auth and build, gossiping its
// report, and a Join that seats a bridge beside it.
func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{dir: t.TempDir()}
	ca, err := sectls.NewCA("c1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	w.key = admission(t, ca, "c1")

	node := fixture.Host(t)
	cloud := fixture.NewCloud()
	fixture.Serve(t, node, cloud.Register)
	fixture.Serve(t, node, cloud.Build)
	w.boot = node.Addrs()[0].String() + "/p2p/" + node.ID().String()
	psNode := gossip(t, node)
	bus, _ := discovery.NewBus(psNode, "c1")
	raw, _ := proto.Marshal(&discoveryv2.Report{Peer: node.ID().String(), Incarnation: 1, Ready: true, Serving: []string{"register", "auth"}})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		for ctx.Err() == nil {
			bus.Publish(ctx, raw)
			time.Sleep(50 * time.Millisecond)
		}
	}()

	w.join = func(ctx context.Context, cfg bridge.Config) (Seat, error) {
		w.got = cfg
		me := fixture.Host(t)
		b, err := bridge.New(ctx, me, gossip(t, me), nil, cfg.Cloud)
		if err != nil {
			return nil, err
		}
		fixture.Connect(t, me, node)
		return seat{b}, nil
	}
	return w
}

func (w *world) args(rest ...string) []string {
	return append(append([]string{"taucorder"}, w.key...), rest...)
}

func (w *world) flag(name string) string {
	for i := 0; i < len(w.key); i += 2 {
		if w.key[i] == "--"+name {
			return w.key[i+1]
		}
	}
	return ""
}

// replaced is the arguments with one admission file swapped for path.
func (w *world) replaced(name, path string, rest ...string) []string {
	out := []string{"taucorder"}
	for i := 0; i < len(w.key); i += 2 {
		value := w.key[i+1]
		if w.key[i] == "--"+name {
			value = path
		}
		out = append(out, w.key[i], value)
	}
	return append(out, rest...)
}

func (w *world) run(t *testing.T, env Env, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	env.Out = &out
	if env.Join == nil {
		env.Join = w.join
	}
	base := w.args("--bootstrap", w.boot, "--cloud", "c1")
	err := App(env).RunContext(context.Background(), append(base, args...))
	return out.String(), err
}

func TestReadCommands(t *testing.T) {
	w := newWorld(t)
	out, err := w.run(t, Env{}, "projects")
	if err != nil || !strings.Contains(out, "github/10") || !strings.Contains(out, "main,staging") {
		t.Fatal(out, err)
	}
	if w.got.Cloud != "c1" || len(w.got.Bootstrap) != 1 || len(w.got.Key) == 0 || len(w.got.CA) == 0 || len(w.got.Certificate) == 0 {
		t.Fatalf("%+v", w.got)
	}
	out, err = w.run(t, Env{}, "jobs", "--project", "p1", "-o", "json")
	var jobs []query.Job
	if err != nil || json.Unmarshal([]byte(out), &jobs) != nil || len(jobs) != 2 {
		t.Fatal(out, err)
	}
	out, err = w.run(t, Env{}, "nodes", "--wait", "3s")
	if err != nil || !strings.Contains(out, "auth,register") {
		t.Fatal(out, err)
	}
	out, err = w.run(t, Env{}, "services", "--wait", "3s", "-o", "json")
	if err != nil || !strings.Contains(out, `"name": "auth"`) {
		t.Fatal(out, err)
	}
}

func TestReadErrors(t *testing.T) {
	w := newWorld(t)
	failing := Env{Join: func(context.Context, bridge.Config) (Seat, error) {
		me := fixture.Host(t)
		b, err := bridge.New(context.Background(), me, gossip(t, me), nil, "")
		return seat{b}, err
	}}
	old := bridge.PickWait
	bridge.PickWait = 100 * time.Millisecond
	defer func() { bridge.PickWait = old }()
	if _, err := w.run(t, failing, "projects"); err == nil {
		t.Fatal("projects with nobody to ask")
	}
	if _, err := w.run(t, failing, "jobs"); err == nil {
		t.Fatal("jobs with nobody to ask")
	}
	boom := Env{Join: func(context.Context, bridge.Config) (Seat, error) { return nil, errors.New("boom") }}
	if _, err := w.run(t, boom, "projects"); err == nil {
		t.Fatal("join error swallowed")
	}
	if _, err := w.run(t, boom, "serve"); err == nil {
		t.Fatal("join error swallowed by serve")
	}
}

func TestConfigErrors(t *testing.T) {
	w := newWorld(t)
	bad := filepath.Join(w.dir, "bad.key")
	os.WriteFile(bad, []byte("nope"), 0o600)
	badYAML := filepath.Join(w.dir, "bad.yaml")
	os.WriteFile(badYAML, []byte("cloud: [\n"), 0o600)
	cases := [][]string{
		{"taucorder", "projects"},
		{"taucorder", "--private-key", w.flag("private-key"), "projects"},
		w.replaced("private-key", filepath.Join(w.dir, "missing"), "projects"),
		w.replaced("ca", filepath.Join(w.dir, "missing"), "projects"),
		w.replaced("private-key", bad, "projects"),
		w.args("--bootstrap", "not-a-multiaddr", "projects"),
		w.args("--bootstrap", "/ip4/127.0.0.1/tcp/1", "projects"),
		{"taucorder", "--profile", filepath.Join(w.dir, "missing.yaml"), "projects"},
		{"taucorder", "--profile", badYAML, "projects"},
		w.replaced("private-key", bad, "serve"),
	}
	for _, args := range cases {
		env := Env{Out: &bytes.Buffer{}, Join: w.join}
		if err := App(env).RunContext(context.Background(), args); err == nil {
			t.Errorf("%v ran", args)
		}
	}
}

func TestProfile(t *testing.T) {
	w := newWorld(t)
	prof := filepath.Join(w.dir, "p.yaml")
	os.WriteFile(prof, []byte("cloud: c1\nprivate_key: "+w.flag("private-key")+"\nca: "+w.flag("ca")+"\ncertificate: "+w.flag("certificate")+"\nbootstrap: ["+w.boot+"]\n"), 0o600)
	var out bytes.Buffer
	env := Env{Out: &out, Join: w.join}
	if err := App(env).RunContext(context.Background(), []string{"taucorder", "-p", prof, "projects"}); err != nil {
		t.Fatal(err)
	}
	if w.got.Cloud != "c1" || !strings.Contains(out.String(), "github/20") {
		t.Fatal(w.got, out.String())
	}
}

func TestServe(t *testing.T) {
	w := newWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	addrs := make(chan string, 1)
	env := Env{Out: &bytes.Buffer{}, Join: w.join, Listening: func(a string) { addrs <- a }}
	done := make(chan error, 1)
	go func() {
		done <- App(env).RunContext(ctx, w.args("--bootstrap", w.boot,
			"serve", "--listen", "127.0.0.1:0", "--origin", "http://localhost:5173"))
	}()
	var addr string
	select {
	case addr = <-addrs:
	case err := <-done:
		t.Fatal(err)
	}
	r := registerv2connect.NewRegisterServiceClient(http.DefaultClient, "http://"+addr)
	got, err := r.Environments(context.Background(), connect.NewRequest(&registerv2.ProjectRequest{Project: "p1"}))
	if err != nil || len(got.Msg.GetEnvironments()) != 2 {
		t.Fatal(got, err)
	}
	_, err = r.Publish(context.Background(), connect.NewRequest(&registerv2.PublishRequest{}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"serve", "--listen", "127.0.0.1:0", "--origin", "*"},
		{"serve", "--listen", "256.0.0.1:0"},
	} {
		if _, err := w.run(t, Env{}, args...); err == nil {
			t.Errorf("%v served", args)
		}
	}
}

func TestUI(t *testing.T) {
	w := newWorld(t)
	var ran tea.Model
	env := Env{RunUI: func(m tea.Model) error { ran = m; return nil }}
	if _, err := w.run(t, env, "ui", "--refresh", "0"); err != nil || ran == nil {
		t.Fatal(err)
	}
	if d := Default(); d.Out == nil || d.Join == nil || d.RunUI == nil {
		t.Fatal("default wiring")
	}
}
