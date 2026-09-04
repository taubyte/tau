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
// # The sandbox is empty by default, and that is the whole posture
//
// A guest gets no filesystem, no sockets, no environment beyond what the
// resource declared, and no clock beyond WASI's. Everything a function is
// allowed to reach is something the configuration granted it — which is the
// same rule the container tier's restricted egress states, arrived at from the
// other direction: there, a firewall takes reach away; here, there was never
// any to take.
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

	"github.com/samyfodil/wazy"
	"github.com/samyfodil/wazy/component"
	"github.com/samyfodil/wazy/imports/wasip2"

	"github.com/taubyte/tau/pkg/invoke"
)

// Name is the string the config vocabulary uses for this tier.
const Name = "wasm"

// SettingCall is where the export's name arrives, spelled as the vocabulary
// spells it.
const SettingCall = "execution/call"

// pageSize is WebAssembly's, and the unit a memory ceiling is expressed in.
const pageSize = 64 << 10

// DefaultExport is called when the configuration names none. It is the world's
// own export name, so a component built straight from wit/handler.wit runs
// without the configuration having to repeat what the world already says.
const DefaultExport = "handle"

var (
	ErrNoAssets   = errors.New("wasm: nothing to fetch a component from")
	ErrNoArtifact = errors.New("wasm: the spec names no artifact")

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

	// stdout and stderr are where a guest's own writes go.
	//
	// THEY GO NOWHERE BY DEFAULT, and that is a gap rather than a decision: a
	// function's println is the first thing anybody reaches for and
	// invoke.Instance has nowhere to put it — Serve returns a response, not a
	// log. Closing it means a per-invocation sink on that interface, which is
	// a change to every tier.
	stdout io.Writer
	stderr io.Writer
}

var _ invoke.Runtime = (*Runtime)(nil)

// Option configures a Runtime.
type Option func(*Runtime)

// WithOutput is where a guest's stdout and stderr go. Both nil discards them.
func WithOutput(stdout, stderr io.Writer) Option {
	return func(r *Runtime) {
		r.stdout, r.stderr = stdout, stderr
	}
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

	return &function{runtime: runtime, instance: instance, export: export}, nil
}

// sandbox is what a guest may reach.
//
// AN EMPTY WASIConfig IS THE POINT: no preopened directory, no sockets, and no
// environment but the resource's own. Every one of those is a grant somebody
// has to make rather than something to be taken away later.
func (r *Runtime) sandbox(spec invoke.Spec) []component.Option {
	return wasip2.WithWASI(wasip2.WASIConfig{
		Stdout: r.stdout,
		Stderr: r.stderr,
		Env:    environment(spec.Env),
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
