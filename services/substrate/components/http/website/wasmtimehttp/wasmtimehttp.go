//go:build wasmtime_component

// Package wasmtimehttp is an opt-in ComponentRuntime backend that serves WASI
// HTTP component bundles — a SpiderMonkey/StarlingMonkey JS engine compiled to a
// `wasi:http/proxy` component, which provides a near-browser Web-API surface
// (full URL/streams/SubtleCrypto and heavy React `renderToString`) that the
// Javy/QuickJS wasi-stdio tier can't.
//
// wazero (Taubyte's default VM) and the wasmtime-go bindings both run only core
// modules + WASI Preview 1 — neither hosts the Component Model. So this backend
// shells out to the `wasmtime` CLI, whose `wasmtime serve` implements the full
// wasi:http host: it spawns one `wasmtime serve` per component (keyed by DAG
// cid, lazily, cached) and reverse-proxies requests to it.
//
// Enable with `-tags wasmtime_component` and `wasmtime` on PATH; then blank-
// import this package so its init() registers the backend. See
// docs/js-runtime-roadmap.md.
package wasmtimehttp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/taubyte/tau/services/substrate/components/http/website"
)

func init() { website.RegisterComponentRuntime(New()) }

// launchFunc starts a server for the component at wasmPath and returns the
// address it listens on plus a stop function. It is a field so tests can swap in
// a stub server instead of a real `wasmtime serve` subprocess.
type launchFunc func(ctx context.Context, wasmPath string, limits website.ComponentLimits) (addr string, stop func(), err error)

// Runtime manages one server per component, keyed by cid.
type Runtime struct {
	bin    string   // wasmtime binary
	extra  []string // extra `wasmtime serve` args (default: -S cli=y)
	work   string   // temp root for component files
	launch launchFunc

	mu    sync.Mutex
	insts map[string]*instance
}

type instance struct {
	once  sync.Once
	addr  string
	proxy *httputil.ReverseProxy
	stop  func()
	err   error
}

// New builds a backend using the `wasmtime` on PATH (override with
// TAUBYTE_WASMTIME_BIN) and `-S cli=y` (StarlingMonkey's stdio feature imports
// wasi:cli; override the flags with TAUBYTE_WASMTIME_SERVE_ARGS).
func New() *Runtime {
	bin := os.Getenv("TAUBYTE_WASMTIME_BIN")
	if bin == "" {
		bin = "wasmtime"
	}
	extra := []string{"-S", "cli=y"}
	if v := strings.TrimSpace(os.Getenv("TAUBYTE_WASMTIME_SERVE_ARGS")); v != "" {
		extra = strings.Fields(v)
	}
	work, _ := os.MkdirTemp("", "tb-component-*")
	rt := &Runtime{bin: bin, extra: extra, work: work, insts: map[string]*instance{}}
	rt.launch = rt.spawnWasmtime
	return rt
}

func (rt *Runtime) Name() string { return "wasmtime/wasi-http" }

// ServeHTTP renders r through the component identified by key, lazily starting
// (and caching) its server on first use, then reverse-proxying the request.
func (rt *Runtime) ServeHTTP(ctx context.Context, key string, component []byte, w http.ResponseWriter, r *http.Request, limits website.ComponentLimits) error {
	inst := rt.instanceFor(key)
	inst.once.Do(func() { rt.start(ctx, inst, key, component, limits) })
	if inst.err != nil {
		return inst.err
	}
	inst.proxy.ServeHTTP(w, r)
	return nil
}

func (rt *Runtime) instanceFor(key string) *instance {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	inst, ok := rt.insts[key]
	if !ok {
		inst = &instance{}
		rt.insts[key] = inst
	}
	return inst
}

func (rt *Runtime) start(ctx context.Context, inst *instance, key string, component []byte, limits website.ComponentLimits) {
	wasmPath := filepath.Join(rt.work, sanitize(key)+".wasm")
	if err := os.WriteFile(wasmPath, component, 0o644); err != nil {
		inst.err = fmt.Errorf("writing component failed with: %w", err)
		return
	}
	addr, stop, err := rt.launch(ctx, wasmPath, limits)
	if err != nil {
		inst.err = fmt.Errorf("launching component server failed with: %w", err)
		return
	}
	target, err := url.Parse("http://" + addr)
	if err != nil {
		stop()
		inst.err = err
		return
	}
	inst.addr = addr
	inst.stop = stop
	inst.proxy = httputil.NewSingleHostReverseProxy(target)
}

// spawnWasmtime starts `wasmtime serve` on a free port and waits until it
// accepts connections.
func (rt *Runtime) spawnWasmtime(ctx context.Context, wasmPath string, limits website.ComponentLimits) (string, func(), error) {
	port, err := freePort()
	if err != nil {
		return "", nil, err
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	args := append([]string{"serve"}, rt.extra...)
	args = append(args, "--addr", addr, wasmPath)
	// Detach from the request ctx: the server is cached across requests.
	cmd := exec.Command(rt.bin, args...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		return "", nil, fmt.Errorf("starting `%s serve` failed (is wasmtime on PATH?): %w", rt.bin, err)
	}
	stop := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}

	if err := waitReady(addr, 15*time.Second); err != nil {
		stop()
		return "", nil, err
	}
	return addr, stop, nil
}

// Close stops every spawned server and removes the work directory.
func (rt *Runtime) Close() {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for _, inst := range rt.insts {
		if inst.stop != nil {
			inst.stop()
		}
	}
	rt.insts = map[string]*instance{}
	_ = os.RemoveAll(rt.work)
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func waitReady(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			c.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("component server at %s did not become ready in %s", addr, timeout)
}

// sanitize maps a cid to a safe filename.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, s)
}
