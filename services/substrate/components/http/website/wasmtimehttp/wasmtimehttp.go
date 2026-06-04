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
// wasi:http host. Per component (keyed by DAG cid) it manages a pool of
// `wasmtime serve` processes: requests round-robin across the pool, dead
// processes are respawned, idle components are evicted, and responses stream
// back through the proxy. Enable with `-tags wasmtime_component` and `wasmtime`
// on PATH, then blank-import this package. See docs/js-runtime-roadmap.md.
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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/taubyte/tau/services/substrate/components/http/website"
)

var (
	singleton    *Runtime
	shutdownOnce sync.Once
)

func init() {
	singleton = New()
	website.RegisterComponentRuntime(singleton)
}

// ShutdownAll stops every spawned component process and removes the work dir.
// The runtime is otherwise process-global (it lives for the substrate's
// lifetime); this is for environments that run many substrates in one process —
// notably dream-based tests — so they don't leak `wasmtime serve` children.
// Idempotent.
func ShutdownAll() {
	shutdownOnce.Do(func() {
		if singleton != nil {
			singleton.Close()
		}
	})
}

// launchFunc starts a server for the component at wasmPath and returns the
// address it listens on, a stop function, and a liveness flag the launcher
// clears when the process exits. It is a field so tests can swap in a stub.
type launchFunc func(wasmPath string, limits website.ComponentLimits) (addr string, stop func(), alive *atomic.Bool, err error)

// Runtime manages a pool of component servers per cid.
type Runtime struct {
	bin      string        // wasmtime binary
	extra    []string      // extra `wasmtime serve` args (default: -S cli=y)
	work     string        // temp root for component files
	poolSize int           // wasmtime processes per component
	idleTTL  time.Duration // evict a component idle longer than this
	maxComps int           // cap on live components (LRU evicted)
	flush    time.Duration // ReverseProxy flush interval (<0 = stream each write)
	launch   launchFunc

	mu    sync.Mutex
	pools map[string]*pool
	stop  chan struct{}
}

// New builds a backend from the `wasmtime` on PATH (override TAUBYTE_WASMTIME_BIN)
// and `-S cli=y` (StarlingMonkey's stdio feature imports wasi:cli; override with
// TAUBYTE_WASMTIME_SERVE_ARGS). Pool size, idle TTL and the component cap are
// tunable via TAUBYTE_COMPONENT_{POOL_SIZE,IDLE_TTL,MAX}.
func New() *Runtime {
	bin := envOr("TAUBYTE_WASMTIME_BIN", "wasmtime")
	extra := []string{"-S", "cli=y"}
	if v := strings.TrimSpace(os.Getenv("TAUBYTE_WASMTIME_SERVE_ARGS")); v != "" {
		extra = strings.Fields(v)
	}
	work, _ := os.MkdirTemp("", "tb-component-*")
	rt := &Runtime{
		bin:      bin,
		extra:    extra,
		work:     work,
		poolSize: envInt("TAUBYTE_COMPONENT_POOL_SIZE", 1),
		idleTTL:  envDur("TAUBYTE_COMPONENT_IDLE_TTL", 5*time.Minute),
		maxComps: envInt("TAUBYTE_COMPONENT_MAX", 32),
		flush:    -1, // stream: flush each write to the client
		pools:    map[string]*pool{},
		stop:     make(chan struct{}),
	}
	rt.launch = rt.spawnWasmtime
	if rt.poolSize < 1 {
		rt.poolSize = 1
	}
	go rt.janitor()
	return rt
}

func (rt *Runtime) Name() string { return "wasmtime/wasi-http" }

// ServeHTTP renders r through the component identified by key, lazily starting
// (and caching) a pool of servers on first use, then reverse-proxying — with
// streaming — through a round-robin, liveness-checked instance.
func (rt *Runtime) ServeHTTP(ctx context.Context, key string, component []byte, w http.ResponseWriter, r *http.Request, limits website.ComponentLimits) error {
	p, err := rt.poolFor(key, component, limits)
	if err != nil {
		return err
	}
	p.lastUsed.Store(time.Now().UnixNano())
	inst, err := p.acquire(rt)
	if err != nil {
		return err
	}
	inst.proxy.ServeHTTP(w, r)
	return nil
}

// Close stops every spawned server and removes the work directory.
func (rt *Runtime) Close() {
	close(rt.stop)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for _, p := range rt.pools {
		p.shutdown()
	}
	rt.pools = map[string]*pool{}
	_ = os.RemoveAll(rt.work)
}

// pool is the set of server processes for one component.
type pool struct {
	wasmPath string
	limits   website.ComponentLimits
	lastUsed atomic.Int64 // unix nanos

	mu    sync.Mutex
	insts []*instance
	rr    uint64
}

type instance struct {
	addr  string
	proxy *httputil.ReverseProxy
	stop  func()
	alive *atomic.Bool
}

func (rt *Runtime) poolFor(key string, component []byte, limits website.ComponentLimits) (*pool, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if p, ok := rt.pools[key]; ok {
		return p, nil
	}
	// Evict the least-recently-used component when at capacity.
	if rt.maxComps > 0 && len(rt.pools) >= rt.maxComps {
		rt.evictLRULocked()
	}
	wasmPath := filepath.Join(rt.work, sanitize(key)+".wasm")
	if err := os.WriteFile(wasmPath, component, 0o644); err != nil {
		return nil, fmt.Errorf("writing component failed with: %w", err)
	}
	p := &pool{wasmPath: wasmPath, limits: limits}
	p.lastUsed.Store(time.Now().UnixNano())
	rt.pools[key] = p
	return p, nil
}

// acquire returns a live instance, pruning dead ones and lazily filling the pool
// up to poolSize (spawning on demand). It tolerates partial pools as long as one
// instance is up.
func (p *pool) acquire(rt *Runtime) (*instance, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	live := p.insts[:0]
	for _, in := range p.insts {
		if in.alive.Load() {
			live = append(live, in)
		} else {
			in.stop()
		}
	}
	p.insts = live

	for len(p.insts) < rt.poolSize {
		in, err := rt.spawnInstance(p.wasmPath, p.limits)
		if err != nil {
			if len(p.insts) == 0 {
				return nil, err
			}
			break // at least one is up; serve from it
		}
		p.insts = append(p.insts, in)
	}
	if len(p.insts) == 0 {
		return nil, fmt.Errorf("no component instances available")
	}
	n := atomic.AddUint64(&p.rr, 1)
	return p.insts[int(n%uint64(len(p.insts)))], nil
}

func (p *pool) shutdown() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, in := range p.insts {
		in.stop()
	}
	p.insts = nil
}

// spawnInstance launches one server and wraps it in a streaming reverse proxy.
func (rt *Runtime) spawnInstance(wasmPath string, limits website.ComponentLimits) (*instance, error) {
	addr, stop, alive, err := rt.launch(wasmPath, limits)
	if err != nil {
		return nil, err
	}
	target, err := url.Parse("http://" + addr)
	if err != nil {
		stop()
		return nil, err
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = rt.flush // stream responses to the client
	return &instance{addr: addr, proxy: proxy, stop: stop, alive: alive}, nil
}

// spawnWasmtime starts `wasmtime serve` on a free port, watches the process for
// exit (clearing the liveness flag), and waits until it accepts connections.
func (rt *Runtime) spawnWasmtime(wasmPath string, limits website.ComponentLimits) (string, func(), *atomic.Bool, error) {
	port, err := freePort()
	if err != nil {
		return "", nil, nil, err
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	args := append([]string{"serve"}, rt.extra...)
	args = append(args, "--addr", addr, wasmPath)
	cmd := exec.Command(rt.bin, args...)
	// Capture the child's output into a small per-instance buffer rather than
	// inheriting the host's os.Stderr: a long-lived `wasmtime serve` that inherits
	// the parent's stdio holds that pipe open, which (e.g. under `go test`) stalls
	// the parent's exit. The buffer is bounded and surfaces startup failures.
	logBuf := &cappedBuffer{limit: 8 << 10}
	cmd.Stdout, cmd.Stderr = logBuf, logBuf
	// On Kill, abandon the process's I/O after a short grace period so Wait (and
	// the host) don't block on a child that's slow to release its pipes.
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Start(); err != nil {
		return "", nil, nil, fmt.Errorf("starting `%s serve` failed (is wasmtime on PATH?): %w", rt.bin, err)
	}

	alive := &atomic.Bool{}
	alive.Store(true)
	go func() { _ = cmd.Wait(); alive.Store(false) }()
	stop := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}

	if err := waitReady(addr, 15*time.Second); err != nil {
		stop()
		return "", nil, nil, fmt.Errorf("`%s serve` did not become ready: %w; output: %s", rt.bin, err, strings.TrimSpace(logBuf.String()))
	}
	return addr, stop, alive, nil
}

// cappedBuffer is a thread-safe io.Writer that retains only the first `limit`
// bytes written (enough to surface a child process's startup error) and discards
// the rest, so a long-running child can't grow it unbounded.
type cappedBuffer struct {
	mu    sync.Mutex
	buf   []byte
	limit int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if room := c.limit - len(c.buf); room > 0 {
		if room > len(p) {
			room = len(p)
		}
		c.buf = append(c.buf, p[:room]...)
	}
	return len(p), nil
}

func (c *cappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.buf)
}

// janitor periodically evicts components idle longer than idleTTL.
func (rt *Runtime) janitor() {
	interval := rt.idleTTL / 2
	if interval < 30*time.Second {
		interval = 30 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-rt.stop:
			return
		case <-t.C:
			rt.evictIdle()
		}
	}
}

func (rt *Runtime) evictIdle() {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	cutoff := time.Now().Add(-rt.idleTTL).UnixNano()
	for k, p := range rt.pools {
		if p.lastUsed.Load() < cutoff {
			p.shutdown()
			delete(rt.pools, k)
		}
	}
}

// evictLRULocked stops and drops the least-recently-used pool. Caller holds mu.
func (rt *Runtime) evictLRULocked() {
	var oldestKey string
	var oldest int64 = 1<<63 - 1
	for k, p := range rt.pools {
		if u := p.lastUsed.Load(); u < oldest {
			oldest, oldestKey = u, k
		}
	}
	if oldestKey != "" {
		rt.pools[oldestKey].shutdown()
		delete(rt.pools, oldestKey)
	}
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

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, s)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDur(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
