// Package wasm runs a component: a WebAssembly component with a declared WIT world,
// so any toolchain emitting one works, unlike v1's hand-rolled host ABI.
//
// wit/handler.wit is the world. Request and response types live in the world
// itself, because a type `use`d from an imported interface leaves the export's
// signature unresolvable from the component's own type section. Every imported
// capability is its own versioned WIT package under wit/deps, matched by one row
// in host.go.
//
// `execution/call` names the export, so one component can hold several functions.
// A component exporting `wasi:http/incoming-handler` is the standard world (what
// wasmtime serve, Spin and wasmCloud run) and runs unmodified; the instance's
// exports say which.
//
// The sandbox is empty by default: no filesystem, sockets or environment beyond the
// resource's. Outbound HTTP is governed by the resource's `egress` over the node's
// floor (egress.go). Each instance gets its own wazy runtime since a memory ceiling
// is per runtime; the compilation cache is shared.
package wasm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/taubyte/tau/pkg/specs"

	"net/http"
	"strings"

	"github.com/samyfodil/wazy"
	"github.com/samyfodil/wazy/api"
	"github.com/samyfodil/wazy/component"
	"github.com/samyfodil/wazy/imports/wasip2"

	"github.com/taubyte/tau/pkg/invoke"
)

// Name is the string the config vocabulary uses for this tier.
const Name = specs.PackageTypeWasm

// The two settings this tier reads, spelled as the vocabulary spells them.
const (
	// SettingCall is the exported symbol to enter the component at.
	SettingCall = specs.WorkloadBlockKeyExecutionCall

	// SettingImplements is the host interface the component was built against;
	// schema-defaulted to WorldHandler.
	SettingImplements = specs.WorkloadBlockKeyImplements
)

// WorldHandler is a component taking a request and answering a response. Worlds
// this tier does not start are refused by name, since calling one with a handler's
// signature would misread its bytes.
const WorldHandler = specs.WorkloadBlockImplementsHandler

// pooledWorlds is what Start can bring up, and worldExports each one's export; only
// the handler takes the configured export.
var (
	pooledWorlds = map[string]bool{
		WorldHandler:            true,
		WorldGatewayTransformer: true,
		WorldGatewayRouter:      true,
		WorldProxyTransformer:   true,
		WorldProxyRouter:        true,
	}

	worldExports = map[string]string{
		WorldGatewayTransformer: transformExport,
		WorldGatewayRouter:      routeExport,
		WorldProxyTransformer:   shapeExport,
		WorldProxyRouter:        directExport,
	}
)

// startableWorlds is what Start can bring up, sorted, for messages.
func startableWorlds() []string {
	out := make([]string, 0, len(pooledWorlds))
	for world := range pooledWorlds {
		out = append(out, world)
	}
	sort.Strings(out)
	return out
}

// httpWorld is the standard world's export, matched without its version, since
// toolchains ship different wasi:http minors.
const httpWorld = "wasi:http/incoming-handler"

// pageSize is WebAssembly's, and the unit a memory ceiling is expressed in.
const pageSize = 64 << 10

// DefaultExport is called when no export is configured: the world's own name.
const DefaultExport = "handle"

var (
	ErrNoAssets    = errors.New("wasm: nothing to fetch a component from")
	ErrNoArtifact  = errors.New("wasm: the spec names no artifact")
	ErrUnknownMode = errors.New("wasm: unknown engine mode")

	// ErrNoMounts is a component declaring a filesystem, refused (see the container
	// tier's ErrNoMounts).
	ErrNoMounts = errors.New("wasm: a component is given no filesystem, and the workload declared a mount")

	// ErrUnsupportedWorld is a component built against a host interface this tier does
	// not start; the startable set is named at the call site so the message never goes
	// stale.
	ErrUnsupportedWorld = errors.New("wasm: this tier does not start that host interface")

	// ErrNotAHandler is an export not shaped as wit/handler.wit declares, reported
	// against the export.
	ErrNotAHandler = errors.New("wasm: that export does not answer with a response")
)

// Mode is how wazy runs a component.
type Mode string

const (
	// ModeCompiler compiles a component to machine code ahead of its first
	// call, where wazy has a compiler for this arch, and interprets it where
	// it has none.
	ModeCompiler Mode = "compiler"

	// ModeInterpreter interprets on every arch: the compiler turned off.
	ModeInterpreter Mode = "interpreter"
)

func (m Mode) config() wazy.RuntimeConfig {
	if m == ModeInterpreter {
		return wazy.NewRuntimeConfigInterpreter()
	}
	return wazy.NewRuntimeConfig()
}

// Assets is where a built component is fetched from.
type Assets interface {
	Get(ctx context.Context, id string) (io.ReadSeekCloser, error)
}

// Runtime is the wasm tier.
type Runtime struct {
	assets Assets
	cache  wazy.CompilationCache
	mode   Mode

	// transport is what an allowed outbound request is finally sent over. Nil
	// is the process default; a test points it at a scratch backend.
	transport http.RoundTripper

	// warn tells an operator about a capability handed over that cannot be served; nil
	// says nothing.
	warn func(format string, args ...any)

	// output is where a guest's writes go, per workload so functions do not interleave.
	// Per-invocation attribution is still missing: above concurrency one, two requests'
	// output interleaves.
	output func(spec invoke.Spec) (stdout, stderr io.Writer)

	// caches backs `taubyte:cache/store`. Nil is a tier that runs everything
	// reaching no cache — see cache.go.
	caches Caches

	// streams backs `taubyte:stream/publisher`. Nil is the same story.
	streams Streams

	// bus backs `taubyte:bus/publisher` — the same sending, a different kind.
	bus Streams

	// sqlStore backs `taubyte:sql/store`, and the same again.
	sqlStore SQL

	// kv backs `taubyte:kv/store` — the same kind at a different data model.
	kv KVs

	// documents backs `taubyte:document/store`, and the same again.
	documents DocumentStores

	// vectors backs `taubyte:vector/store` — the fourth and last data model
	// the `database` kind declares.
	vectors VectorStores

	// queues backs `taubyte:queue/producer`.
	queues Queues

	// storage is the object store this tier serves `taubyte:storage/objects`
	// from.
	storage Buckets

	// admission decides whether one instance of a store that holds many may
	// exist. Nil refuses every one — see instance.go.
	admission Admitter

	// secrets backs `taubyte:secret/vault` — the rotation path, beside the
	// environment variable that carries a secret at start.
	secrets   Secrets
	inference Inferences

	// logs backs `taubyte:log/records` — the `sync` kind's other type.
	logs Logs

	// channels backs `taubyte:channel/dialer` — in-project addressing, which
	// v1 called p2p.
	channels Channels

	// authorizers answers what a caller may do to a guarded target. Nil on a
	// tier that cannot ask, which is a spec with a guarded grant refused at
	// Start rather than denied at the first call.
	authorizers Authorizers

	// node is the peer this tier runs on, for `taubyte:workload/identity`. "" is a
	// tier composed without one, which answers "" rather than inventing a name.
	node string
}

var _ invoke.Runtime = (*Runtime)(nil)

// Option configures a Runtime.
type Option func(*Runtime)

// WithOutput is where a guest's stdout and stderr go, per workload; nil discards.
func WithOutput(sink func(spec invoke.Spec) (stdout, stderr io.Writer)) Option {
	return func(r *Runtime) { r.output = sink }
}

// WithTransport is what an allowed outbound request is sent over. It does not
// change WHICH requests are allowed — that is the resource's egress and is not
// a knob — only where an allowed one goes.
func WithTransport(t http.RoundTripper) Option {
	return func(r *Runtime) { r.transport = t }
}

// WithMode picks how wazy runs a component; empty is ModeCompiler.
func WithMode(m Mode) Option {
	return func(r *Runtime) { r.mode = m }
}

// WithWarn is where this tier says something an operator has to know but that
// is not a reason to refuse a workload. See unserved.
func WithWarn(warn func(format string, args ...any)) Option {
	return func(r *Runtime) { r.warn = warn }
}

// New builds the tier.
func New(assets Assets, opts ...Option) (*Runtime, error) {
	if assets == nil {
		return nil, ErrNoAssets
	}

	r := &Runtime{assets: assets}
	for _, apply := range opts {
		apply(r)
	}
	switch r.mode {
	case "":
		r.mode = ModeCompiler
	case ModeCompiler, ModeInterpreter:
	default:
		return nil, fmt.Errorf("%w: %q (want %s or %s)", ErrUnknownMode, r.mode, ModeCompiler, ModeInterpreter)
	}
	r.cache = wazy.NewCompilationCache()
	return r, nil
}

func (r *Runtime) Name() string { return Name }

// Mode is how this tier runs components.
func (r *Runtime) Mode() Mode { return r.mode }

// Close releases the compilation cache. A Runtime is usable until it is called.
func (r *Runtime) Close() error { return r.cache.Close(context.Background()) }

// Start fetches a component and instantiates it. Its latency IS the cold start.
func (r *Runtime) Start(ctx context.Context, spec invoke.Spec) (invoke.Instance, error) {
	if spec.Artifact == "" {
		return nil, fmt.Errorf("%w: %s", ErrNoArtifact, spec.Name)
	}

	// Which world this spec is. Handler, transformers and routers are pooled (none of
	// their answers can be kept), and the instance remembers its world so the wrong
	// export is never called; authorizers and validators have their own entry points.
	world := spec.Settings[SettingImplements]
	if world == "" {
		world = WorldHandler
	}
	if !pooledWorlds[world] {
		return nil, fmt.Errorf("%w: %s is %q, and this tier starts %s",
			ErrUnsupportedWorld, spec.Name, world, strings.Join(startableWorlds(), ", "))
	}

	instance, runtime, err := r.instantiate(ctx, spec, world)
	if err != nil {
		return nil, err
	}

	export := spec.Settings[SettingCall]
	if export == "" {
		export = DefaultExport
	}

	if fixed, ok := worldExports[world]; ok {
		// Fixed: a gateway or proxy names the function, not an export.
		export = fixed
	}

	return &function{
		runtime:  runtime,
		instance: instance,
		export:   export,
		world:    world,
		http:     world == WorldHandler && exportsHTTPWorld(instance),
	}, nil
}

// instantiate fetches a component, checks its declared world matches the one the
// caller will drive, and brings it up. It checks the declaration, not the
// artifact: a component mis-built against its own declaration surfaces on the first
// call. An empty `implements` is the handler.
func (r *Runtime) instantiate(ctx context.Context, spec invoke.Spec, world string) (*component.Instance, wazy.Runtime, error) {
	// A spec for another tier is refused: guards, validators and resolvers reach this
	// tier directly, not through the pool.
	if spec.Runtime != "" && spec.Runtime != Name {
		return nil, nil, fmt.Errorf("%w: %s runs on the %q tier, and this is the %s tier",
			invoke.ErrNoRuntime, spec.Name, spec.Runtime, Name)
	}
	if spec.Artifact == "" {
		return nil, nil, fmt.Errorf("%w: %s", ErrNoArtifact, spec.Name)
	}

	declared := spec.Settings[SettingImplements]
	if declared == "" {
		declared = WorldHandler
	}
	if declared != world {
		return nil, nil, fmt.Errorf("%w: %s is %q and this is the %q interface",
			ErrUnsupportedWorld, spec.Name, declared, world)
	}

	// A declared filesystem is refused: no directory is preopened, so writes would
	// vanish with the instance.
	if mounted := spec.Mounted(); len(mounted) > 0 {
		return nil, nil, fmt.Errorf("%w: %s wants %s, and a component is given no filesystem",
			ErrNoMounts, spec.Name, mounted[0].Label)
	}

	// A guarded grant needs something to ask; checked here since an authorizer hangs off
	// every kind rather than being a capability.
	if guarded := guardedGrants(spec); len(guarded) > 0 && r.authorizers == nil {
		return nil, nil, fmt.Errorf("%w: %s reaches %s", ErrNoAuthorizers, spec.Name, guarded[0].Label)
	}

	// Every capability's host half (host.go).
	r.unserved(spec)

	imports, err := r.imports(spec)
	if err != nil {
		return nil, nil, err
	}

	content, err := r.assets.Get(ctx, spec.Artifact)
	if err != nil {
		return nil, nil, fmt.Errorf("wasm: fetching %s: %w", spec.Artifact, err)
	}
	defer content.Close()

	code, err := io.ReadAll(content)
	if err != nil {
		return nil, nil, fmt.Errorf("wasm: reading %s: %w", spec.Artifact, err)
	}

	config := r.mode.config().
		WithCompilationCache(r.cache).
		// A GUEST THAT WILL NOT STOP IS STOPPED. The pool bounds a request
		// with a context; without this a guest in a loop ignores it and holds
		// the goroutine until the process ends.
		WithCloseOnContextDone(true).
		// WebAssembly 3.0, because toolchains emit it: componentize-py's
		// CPython needs extended-const.
		WithCoreFeatures(api.CoreFeaturesV3)
	if pages := memoryPages(spec.Memory); pages > 0 {
		config = config.WithMemoryLimitPages(pages)
	}

	runtime := wazy.NewRuntimeWithConfig(ctx, config)

	instance, err := component.Instantiate(ctx, runtime, code, append(r.sandbox(spec), imports...)...)
	if err != nil {
		runtime.Close(ctx)
		return nil, nil, fmt.Errorf("wasm: instantiating %s: %w", spec.Name, err)
	}
	return instance, runtime, nil
}

// exportsHTTPWorld reports whether this component is the standard world, read from
// the instance.
func exportsHTTPWorld(instance *component.Instance) bool {
	for _, name := range instance.InstanceExports() {
		if strings.HasPrefix(name, httpWorld) {
			return true
		}
	}
	return false
}

// sandbox is what a guest may reach: an empty WASIConfig, with only the resource's
// environment.
func (r *Runtime) sandbox(spec invoke.Spec) []component.Option {
	var stdout, stderr io.Writer
	if r.output != nil {
		stdout, stderr = r.output(spec)
	}

	return wasip2.WithWASI(wasip2.WASIConfig{
		Stdout: stdout,
		Stderr: stderr,
		Env:    environment(spec.Env),

		// The standard world is always registered, since imports resolve only at
		// instantiation; the allowlist governs the client.
		EnableHTTP: true,
		HTTPClient: egressFor(spec.Egress, r.transport),
	})
}

// environment is the resource's variables in the "KEY=VALUE" shape WASI wants,
// sorted so that two instances of one revision are handed the same list.
func environment(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}

	out := make([]string, 0, len(env))
	for key, value := range env {
		out = append(out, key+"="+value)
	}
	sort.Strings(out)
	return out
}

// memoryPages is a byte ceiling in whole pages, rounded up so the limit is never
// below what was asked.
func memoryPages(bytes int64) uint32 {
	if bytes <= 0 {
		return 0
	}
	pages := (bytes + pageSize - 1) / pageSize
	if pages > 1<<16 {
		// The 32-bit address space is the real ceiling; a larger number would
		// panic inside wazy rather than be honoured.
		return 1 << 16
	}
	return uint32(pages)
}

// function is one instantiated component.
type function struct {
	runtime  wazy.Runtime
	instance *component.Instance
	export   string

	// world is which started world this is, so the wrong export is never called.
	world string

	// http says this component is the standard world, so it is driven as an
	// HTTP handler rather than called by name.
	http bool
}

// Close takes the instance and its runtime with it.
func (f *function) Close() error {
	ctx := context.Background()
	err := f.instance.Close(ctx)
	if closeErr := f.runtime.Close(ctx); err == nil {
		err = closeErr
	}
	return err
}

// Serve hands one request to the guest.
func (f *function) Serve(ctx context.Context, req *invoke.Request) (*invoke.Response, error) {
	if f.world != WorldHandler {
		return nil, fmt.Errorf("%w: this is a %s and answers no request", ErrUnsupportedWorld, f.world)
	}
	if f.http {
		return f.serveHTTP(ctx, req)
	}

	out, err := f.instance.Call(ctx, f.export, requestValue(req))
	if err != nil {
		return nil, fmt.Errorf("wasm: calling %s: %w", f.export, err)
	}
	if len(out) != 1 {
		return nil, fmt.Errorf("%w: %s returned %d values", ErrNotAHandler, f.export, len(out))
	}
	return responseFrom(out[0], f.export)
}

// requestValue lowers a Request into the `request` record. Fields in order, so
// wit/handler.wit's field order is load-bearing here.
func requestValue(req *invoke.Request) component.Value {
	if req == nil {
		req = &invoke.Request{}
	}

	headers := make([]component.Value, 0, len(req.Headers))
	for _, name := range sortedKeys(req.Headers) {
		for _, value := range req.Headers[name] {
			headers = append(headers, []component.Value{name, value})
		}
	}

	body := req.Body
	if body == nil {
		// A nil body is an empty list and not a null: the world says list<u8>,
		// which has no absent case.
		body = []byte{}
	}

	return []component.Value{req.Method, req.Path, headers, body}
}

// responseFrom lifts the `response` record back, checking every field.
func responseFrom(value component.Value, export string) (*invoke.Response, error) {
	fields, ok := value.([]component.Value)
	if !ok || len(fields) != 3 {
		return nil, fmt.Errorf("%w: %s", ErrNotAHandler, export)
	}

	status, ok := fields[0].(uint32)
	if !ok {
		return nil, fmt.Errorf("%w: %s answered with no status", ErrNotAHandler, export)
	}

	headers, err := headersFrom(fields[1], export)
	if err != nil {
		return nil, err
	}

	body, ok := fields[2].([]byte)
	if !ok && fields[2] != nil {
		return nil, fmt.Errorf("%w: %s answered with a body that is not bytes", ErrNotAHandler, export)
	}

	return &invoke.Response{Status: int(status), Headers: headers, Body: body}, nil
}

func headersFrom(value component.Value, export string) (map[string][]string, error) {
	list, ok := value.([]component.Value)
	if !ok {
		if value == nil {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: %s answered with headers that are not a list", ErrNotAHandler, export)
	}
	if len(list) == 0 {
		return nil, nil
	}

	out := make(map[string][]string, len(list))
	for _, entry := range list {
		pair, ok := entry.([]component.Value)
		if !ok || len(pair) != 2 {
			return nil, fmt.Errorf("%w: %s answered with a header that is not a pair", ErrNotAHandler, export)
		}
		name, nameOK := pair[0].(string)
		body, valueOK := pair[1].(string)
		if !nameOK || !valueOK {
			return nil, fmt.Errorf("%w: %s answered with a header that is not two strings", ErrNotAHandler, export)
		}
		out[name] = append(out[name], body)
	}
	return out, nil
}

func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
