// Package wasm runs a component.
//
// # It is the differentiator, and it is the component model
//
// A tau function compiles to a WebAssembly component and this runs it. Not a
// core module with a hand-rolled host ABI — v1's shape, where the guest and the
// host agreed by convention and a guest written in any other language had to
// reimplement that convention — but a component with a declared WIT world, so
// what a guest exports is described by the artifact itself and any toolchain
// that emits a component can produce one.
//
// wit/handler.wit is that world. The types live in the world rather than in an
// imported interface for a reason that is mechanical: a `use` from an imported
// interface leaves the export's signature referring to a type a host reading
// the component's own type section cannot resolve, and the call cannot be
// flattened at all.
//
// # The export is named by the author
//
// `execution/call` names it, so a project with two functions in one component
// is two exports rather than two components. The world declares the SHAPE; the
// host calls whatever the configuration says.
//
// # Two worlds, and a component is recognised by what it exports
//
// A component exporting `wasi:http/incoming-handler` is the standard world —
// what wasmtime serve, Spin and wasmCloud run — and it runs here unmodified.
// One exporting a bare function is the world above. Nothing declares which; the
// instance says what it exports and this reads it.
//
// # The sandbox is empty by default, and that is the whole posture
//
// A guest gets no filesystem, no sockets, no environment beyond what the
// resource declared, and no clock beyond WASI's. The one door out is HTTP, and
// it is GOVERNED rather than absent: `egress` on the resource says where, and
// empty means nowhere (egress.go). A component that exports the standard world
// usually imports the outgoing half beside it, so refusing that outright would
// refuse a large share of what this tier exists to be able to run.
//
// It is the same rule the container tier states with a firewall, arrived at
// with the only mechanism a wasm guest has — there is no socket to filter,
// because the guest never gets one.
//
// # One runtime per instance, and why
//
// A memory ceiling is a property of a wazy runtime rather than of a module, and
// every resource declares its own — so a runtime shared across specs could only
// carry one of them. The compilation cache is shared instead, which is where
// the cost actually is: two instances of one revision compile once.
package wasm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"

	"net/http"
	"strings"

	"github.com/samyfodil/wazy"
	"github.com/samyfodil/wazy/component"
	"github.com/samyfodil/wazy/imports/wasip2"

	"github.com/taubyte/tau/pkg/invoke"
)

// Name is the string the config vocabulary uses for this tier.
const Name = "wasm"

// The two settings this tier reads, spelled as the vocabulary spells them.
const (
	// SettingCall is the exported symbol to enter the component at.
	SettingCall = "execution/call"

	// SettingImplements is which host interface the component was built
	// against. The schema defaults it to WorldHandler, so a resource that says
	// nothing still says handler.
	SettingImplements = "implements"
)

// WorldHandler is the only host interface this tier serves: a component that
// takes a request and answers with a response.
//
// THE OTHERS ARE REFUSED BY NAME. An authorizer takes a caller and an
// operation; a gateway-transformer takes a parsed HTTP request; a router
// answers with a destination. Calling one of them with a handler's signature
// would not fail cleanly — it would hand a component bytes it reads as
// something else, which is the failure the vocabulary added `implements` to
// prevent.
const WorldHandler = "handler"

// httpWorld is the standard world's export. A component exporting it is driven
// as an HTTP handler; anything else is called by name.
//
// IT IS MATCHED WITHOUT ITS VERSION, because a component is built against
// whichever wasi:http its toolchain shipped — 0.2.0 through 0.2.12 and onward —
// and refusing one for its minor version would refuse components that work.
const httpWorld = "wasi:http/incoming-handler"

// pageSize is WebAssembly's, and the unit a memory ceiling is expressed in.
const pageSize = 64 << 10

// DefaultExport is called when the configuration names none. It is the world's
// own export name, so a component built straight from wit/handler.wit runs
// without the configuration having to repeat what the world already says.
const DefaultExport = "handle"

var (
	ErrNoAssets   = errors.New("wasm: nothing to fetch a component from")
	ErrNoArtifact = errors.New("wasm: the spec names no artifact")

	// ErrNotAHandler is a component built against a host interface this tier
	// does not serve.
	ErrUnsupportedWorld = errors.New("wasm: this tier runs the handler interface and nothing else yet")

	// ErrNotAHandler is a component whose export does not have the shape
	// wit/handler.wit declares. It is reported against the EXPORT rather than
	// the component, because a component may export several and only one of
	// them is what the configuration named.
	ErrNotAHandler = errors.New("wasm: that export does not answer with a response")
)

// Assets is where a built component is fetched from.
type Assets interface {
	Get(ctx context.Context, id string) (io.ReadSeekCloser, error)
}

// Runtime is the wasm tier.
type Runtime struct {
	assets Assets
	cache  wazy.CompilationCache

	// transport is what an allowed outbound request is finally sent over. Nil
	// is the process default; a test points it at a scratch backend.
	transport http.RoundTripper

	// output is where a guest's own writes go, per workload.
	//
	// IT IS A FUNCTION OF THE SPEC because a tier is built once and runs many
	// workloads: a single writer handed in at construction would interleave
	// every function on the node into one stream with nothing saying which
	// wrote what.
	//
	// WHAT IS STILL MISSING is per-INVOCATION attribution. stdout belongs to an
	// instance rather than to a call, so at a concurrency above one — which the
	// Lambda model's default of one is deliberately not — two requests' output
	// interleaves. Closing that means a sink on invoke.Instance.Serve, which is
	// a change to every tier.
	output func(spec invoke.Spec) (stdout, stderr io.Writer)
}

var _ invoke.Runtime = (*Runtime)(nil)

// Option configures a Runtime.
type Option func(*Runtime)

// WithOutput is where a guest's stdout and stderr go, chosen per workload.
//
// A nil function, or one that answers nil, discards them — which is what a node
// with nowhere to put them does, rather than holding a buffer nothing reads.
func WithOutput(sink func(spec invoke.Spec) (stdout, stderr io.Writer)) Option {
	return func(r *Runtime) { r.output = sink }
}

// WithTransport is what an allowed outbound request is sent over. It does not
// change WHICH requests are allowed — that is the resource's egress and is not
// a knob — only where an allowed one goes.
func WithTransport(t http.RoundTripper) Option {
	return func(r *Runtime) { r.transport = t }
}

// New builds the tier.
func New(assets Assets, opts ...Option) (*Runtime, error) {
	if assets == nil {
		return nil, ErrNoAssets
	}

	r := &Runtime{assets: assets, cache: wazy.NewCompilationCache()}
	for _, apply := range opts {
		apply(r)
	}
	return r, nil
}

func (r *Runtime) Name() string { return Name }

// Close releases the compilation cache. A Runtime is usable until it is called.
func (r *Runtime) Close() error { return r.cache.Close(context.Background()) }

// Start fetches a component and instantiates it. Its latency IS the cold start.
func (r *Runtime) Start(ctx context.Context, spec invoke.Spec) (invoke.Instance, error) {
	if spec.Artifact == "" {
		return nil, fmt.Errorf("%w: %s", ErrNoArtifact, spec.Name)
	}

	// THE WORLD IS CHECKED BEFORE ANYTHING IS FETCHED. A component built as an
	// authorizer would instantiate perfectly well and then be called with a
	// handler's signature, which is the failure `implements` exists to prevent
	// — so it is refused here rather than discovered at the first request.
	if world := spec.Settings[SettingImplements]; world != "" && world != WorldHandler {
		return nil, fmt.Errorf("%w: %s is %q", ErrUnsupportedWorld, spec.Name, world)
	}

	content, err := r.assets.Get(ctx, spec.Artifact)
	if err != nil {
		return nil, fmt.Errorf("wasm: fetching %s: %w", spec.Artifact, err)
	}
	defer content.Close()

	code, err := io.ReadAll(content)
	if err != nil {
		return nil, fmt.Errorf("wasm: reading %s: %w", spec.Artifact, err)
	}

	config := wazy.NewRuntimeConfig().
		WithCompilationCache(r.cache).
		// A GUEST THAT WILL NOT STOP IS STOPPED. The pool bounds a request
		// with a context; without this a guest in a loop ignores it and holds
		// the goroutine until the process ends.
		WithCloseOnContextDone(true)
	if pages := memoryPages(spec.Memory); pages > 0 {
		config = config.WithMemoryLimitPages(pages)
	}

	runtime := wazy.NewRuntimeWithConfig(ctx, config)

	instance, err := component.Instantiate(ctx, runtime, code, r.sandbox(spec)...)
	if err != nil {
		runtime.Close(ctx)
		return nil, fmt.Errorf("wasm: instantiating %s: %w", spec.Name, err)
	}

	export := spec.Settings[SettingCall]
	if export == "" {
		export = DefaultExport
	}

	return &function{
		runtime:  runtime,
		instance: instance,
		export:   export,
		http:     exportsHTTPWorld(instance),
	}, nil
}

// exportsHTTPWorld reports whether this component is the standard world.
//
// IT ASKS THE INSTANCE rather than the configuration, because which world a
// component implements is a property of the artifact and nothing an author
// should have to restate — a component built for wasmtime serve carries the
// answer with it.
func exportsHTTPWorld(instance *component.Instance) bool {
	for _, name := range instance.InstanceExports() {
		if strings.HasPrefix(name, httpWorld) {
			return true
		}
	}
	return false
}

// sandbox is what a guest may reach.
//
// AN EMPTY WASIConfig IS THE POINT: no preopened directory, no sockets, and no
// environment but the resource's own. Every one of those is a grant somebody
// has to make rather than something to be taken away later.
func (r *Runtime) sandbox(spec invoke.Spec) []component.Option {
	var stdout, stderr io.Writer
	if r.output != nil {
		stdout, stderr = r.output(spec)
	}

	return wasip2.WithWASI(wasip2.WASIConfig{
		Stdout: stdout,
		Stderr: stderr,
		Env:    environment(spec.Env),

		// THE STANDARD WORLD IS ALWAYS REGISTERED, because whether a component
		// needs it cannot be known before instantiating it — its imports are
		// what instantiation resolves. What that would otherwise hand a guest,
		// an unrestricted HTTP client, is closed by the allowlist rather than
		// by leaving the world out.
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

// memoryPages is a byte ceiling as whole WebAssembly pages, rounded UP.
//
// ROUNDED UP because a limit below what the author asked for is a workload that
// fails for a reason the configuration does not state — and the overshoot is at
// most one page.
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

// requestValue lowers a Request into the world's `request` record.
//
// A RECORD IS ITS FIELDS IN ORDER, which is the component model's own value
// shape — so this is the one place the field ORDER in wit/handler.wit is
// load-bearing, and reordering it there without reordering it here would be a
// silent transposition of two strings.
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

// responseFrom lifts the world's `response` record back.
//
// EVERY FIELD IS CHECKED. What comes back was produced by a stranger's
// component, so a shape that is not this world's is an error naming the export
// rather than a panic three frames later.
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
