package js_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/samyfodil/warpjs/engine"

	"github.com/taubyte/tau/pkg/invoke"
	js "github.com/taubyte/tau/pkg/invoke/js"
	"github.com/taubyte/tau/pkg/invoke/js/archive"
)

// store is an asset store holding archives by id.
type store struct {
	mu       sync.Mutex
	archives map[string][]byte
	err      error
}

type asset struct{ *bytes.Reader }

func (asset) Close() error { return nil }

func (s *store) Read(_ context.Context, id string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	data, ok := s.archives[id]
	if !ok {
		return nil, fmt.Errorf("no asset %q", id)
	}
	return asset{bytes.NewReader(data)}, nil
}

func (s *store) put(id string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.archives == nil {
		s.archives = map[string][]byte{}
	}
	s.archives[id] = data
}

// pack is an archive whose entry is worker.js.
func pack(t testing.TB, files map[string]string) []byte {
	t.Helper()
	raw := map[string][]byte{}
	for name, src := range files {
		raw[name] = []byte(src)
	}
	var buf bytes.Buffer
	require.NoError(t, archive.Write(&buf, "worker.js", raw))
	return buf.Bytes()
}

// logs collects what a function's console wrote.
type logs struct {
	mu             sync.Mutex
	stdout, stderr strings.Builder
}

type sink struct {
	l *logs
	b *strings.Builder
}

func (s sink) Write(p []byte) (int, error) {
	s.l.mu.Lock()
	defer s.l.mu.Unlock()
	return s.b.Write(p)
}

func (l *logs) out() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stdout.String()
}

func (l *logs) err() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stderr.String()
}

func (l *logs) option() js.Option {
	return js.WithOutput(func(invoke.Spec) (io.Writer, io.Writer) {
		return sink{l, &l.stdout}, sink{l, &l.stderr}
	})
}

// later(ms) is a promise resolved from another goroutine, the shape every
// host-async capability will have.
func later(rt *engine.Runtime) error {
	fn, err := rt.NewFunction("later", func(call engine.FunctionCall) (engine.Value, error) {
		ms, err := call.Argument(0).ToInteger()
		if err != nil {
			return engine.Value{}, err
		}
		p, resolve, _, err := rt.NewPromise()
		if err != nil {
			return engine.Value{}, err
		}
		time.AfterFunc(time.Duration(ms)*time.Millisecond, func() {
			rt.Post(func() { resolve(nil) })
		})
		return p, nil
	})
	if err != nil {
		return err
	}
	return rt.Set("later", fn)
}

type fixture struct {
	t     testing.TB
	store *store
	tier  *js.Runtime
	logs  *logs
	spec  invoke.Spec
	// hold is closed when the test lets every held() promise resolve.
	hold    chan struct{}
	letGoes sync.Once
}

// realm is what a fixture's realm has: later(ms), and held(), a promise resolved
// only when the test lets go, so the test, not a clock, says when work ends.
func (f *fixture) realm(rt *engine.Runtime) error {
	if err := later(rt); err != nil {
		return err
	}
	fn, err := rt.NewFunction("held", func(engine.FunctionCall) (engine.Value, error) {
		p, resolve, _, err := rt.NewPromise()
		if err != nil {
			return engine.Value{}, err
		}
		go func() {
			<-f.hold
			rt.Post(func() { resolve(nil) })
		}()
		return p, nil
	})
	if err != nil {
		return err
	}
	return rt.Set("held", fn)
}

// letGo resolves every held() promise, made already or to come.
func (f *fixture) letGo() { f.letGoes.Do(func() { close(f.hold) }) }

// tier is a js tier over one archive holding worker.js, with later() in every
// realm.
func newFixture(t testing.TB, worker string, opts ...js.Option) *fixture {
	t.Helper()
	return newFixtureFiles(t, map[string]string{"worker.js": worker}, opts...)
}

func newFixtureFiles(t testing.TB, files map[string]string, opts ...js.Option) *fixture {
	t.Helper()
	f := &fixture{t: t, store: &store{}, logs: &logs{}, hold: make(chan struct{})}
	t.Cleanup(f.letGo)
	f.store.put("asset", pack(t, files))
	tier, err := js.New(f.store, append([]js.Option{f.logs.option(), js.WithRealm(f.realm)}, opts...)...)
	require.NoError(t, err)
	f.tier = tier
	f.spec = invoke.Spec{Runtime: js.Name, Name: "shop/worker", Revision: "r1", Artifact: "asset"}
	return f
}

func (f *fixture) start() invoke.Instance {
	f.t.Helper()
	inst, err := f.tier.Start(context.Background(), f.spec)
	require.NoError(f.t, err)
	f.t.Cleanup(func() { inst.Close() })
	return inst
}

func (f *fixture) pool(opts ...invoke.Option) *invoke.Pool {
	f.t.Helper()
	p, err := invoke.NewPool(append([]invoke.Option{invoke.WithRuntime(f.tier)}, opts...)...)
	require.NoError(f.t, err)
	f.t.Cleanup(func() { p.Close() })
	return p
}

func get(path string) *invoke.Request { return &invoke.Request{Method: "GET", Path: path} }

func serve(t testing.TB, inst invoke.Instance, req *invoke.Request) *invoke.Response {
	t.Helper()
	resp, err := inst.Serve(context.Background(), req)
	require.NoError(t, err)
	return resp
}
