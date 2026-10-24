// Package js runs a JavaScript function on warpjs, in the worker's own
// process, the way pkg/invoke/wasm runs a component on wazy.
//
// # One realm per instance, one request at a time
//
// An instance is one engine.Runtime: one realm with its own arena. A realm is
// used from one goroutine at a time, so an instance serves one request at a
// time, and concurrency comes from the pool starting more of them. Module state
// persists across the requests one instance serves, as it does per isolate on
// Workers. A realm only ever runs one revision of one resource, so nothing
// crosses tenants through it.
//
// # The handler is the Workers shape
//
// The entry module's default export (or the one `execution/call` names) is an
// object with `fetch(request, env, ctx)`. The platform's prelude gives the realm
// Request, Response, Headers, TextEncoder, TextDecoder and console, which warpjs
// does not have. `env` is empty until host capabilities reach JS, and
// `ctx.waitUntil` keeps the instance checked out until what it was handed
// settles or the deadline hits.
//
// # Limits are the engine's, and a realm that hits one is not reused
//
// `execution/memory` is the arena cap and `execution/timeout` the deadline. A
// call that runs out of either fails with a typed error and retires the
// instance: a realm interrupted mid-mutation is not handed another request.
//
// THE DEADLINE IS ARMED THREE WAYS until warpjs bounds its job drains: promise
// jobs drain automatically at the end of every outermost call (never
// WithManualJobs, whose drains have no limit), every request runs under
// RunContext with its deadline, and a timer interrupts the realm at the
// deadline. A timer that fired retires the instance even when the call
// finished, since its interrupt would otherwise kill the next one.
//
// # Nothing of the host reaches a tenant realm
//
// No nodejs Registry and no `process` are installed: warpjs's core modules
// register process-wide and `process.env` is the host's. The module host serves
// what the archive holds and refuses every other specifier by name.
package js

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"warpjs/engine"

	_ "embed"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
)

// Name is the string the config vocabulary uses for this tier.
const Name = specs.WorkloadBlockRuntimeJs

// The settings this tier reads, spelled as the vocabulary spells them.
const (
	// SettingCall names the export the handler is. Empty is DefaultExport.
	SettingCall = specs.WorkloadBlockKeyExecutionCall

	// SettingImplements is which host interface the function was written
	// against. This tier serves the handler and refuses the rest by name.
	SettingImplements = specs.WorkloadBlockKeyImplements
)

// WorldHandler is the one host interface this tier serves: a request in, a
// response out.
const WorldHandler = specs.WorkloadBlockImplementsHandler

// DefaultExport is the export a Workers module is: `export default { fetch }`.
const DefaultExport = "default"

// DefaultMemory is the arena of a spec that sets no `execution/memory`: the
// engine's own default.
const DefaultMemory = 256 << 20

// maxStack is the call-stack budget a realm gets, out of its arena. The
// engine's default is 1/2048 of the arena, 32 KiB at a 64 MiB limit, which
// ordinary library code overflows; V8's is about 1 MiB.
const maxStack = 1 << 20

var (
	ErrNoAssets    = errors.New("js: nothing to fetch an archive from")
	ErrNoArtifact  = errors.New("js: the spec names no artifact")
	ErrUnknownMode = errors.New("js: unknown engine mode")
	ErrNoRoom      = errors.New("js: an archive ceiling must be positive")

	// ErrUnsupportedWorld is a function written against a host interface this
	// tier does not serve yet.
	ErrUnsupportedWorld = errors.New("js: this tier serves the handler interface and no other yet")

	// ErrNoMounts is a function that declared a filesystem. A realm has none,
	// so what it wrote would vanish with the instance.
	ErrNoMounts = errors.New("js: a realm is given no filesystem, and the workload declared a mount")

	// ErrNoHandler is an entry module that does not export a handler.
	ErrNoHandler = errors.New("js: the entry exports no handler")

	// ErrEngine is the engine refusing to start a realm.
	ErrEngine = errors.New("js: the engine did not start")

	// ErrDeadline is a call that ran past its deadline. The instance is retired.
	ErrDeadline = errors.New("js: the call ran past its deadline")

	// ErrOutOfMemory is a call that ran out of arena. The instance is retired.
	ErrOutOfMemory = errors.New("js: the instance ran out of memory")
)

// Mode is how the engine runs code: warpjs's three tiers.
type Mode string

const (
	// ModeBytecode interprets. It is the only mode on an arch with no native
	// backend.
	ModeBytecode Mode = "bytecode"

	// ModeNative compiles a function to machine code, on the calling
	// goroutine, when it goes hot.
	ModeNative Mode = "native"

	// ModeJIT is native with the compile on a background goroutine.
	ModeJIT Mode = "native+jit"
)

// DefaultNativeMode is what an arch with a native backend runs when nothing
// names a mode.
//
// PROVISIONAL. The numbers it was chosen on measured the request path and JIT
// warm-up rather than how fast code runs, so it is this one constant, and
// nothing else depends on which mode it names.
const DefaultNativeMode = ModeJIT

// DefaultMode is DefaultNativeMode where the engine has a native backend for
// this arch, and bytecode elsewhere.
func DefaultMode() Mode {
	if engine.NativeSupported() {
		return DefaultNativeMode
	}
	return ModeBytecode
}

func (m Mode) options() []engine.Option {
	switch m {
	case ModeNative:
		return []engine.Option{engine.WithNative()}
	case ModeJIT:
		return []engine.Option{engine.WithNative(), engine.WithJITCompile()}
	}
	return nil
}

// Assets is where a built archive is fetched from.
type Assets interface {
	Get(ctx context.Context, id string) (io.ReadSeekCloser, error)
}

//go:embed prelude.js
var preludeSource string

// preludeName is the file name the prelude's stack frames report.
const preludeName = "tau:prelude"

// Runtime is the js tier.
type Runtime struct {
	assets Assets
	mode   Mode

	// maxArchive bounds an archive, packed and unpacked.
	maxArchive int64

	// prelude is compiled once and run in every realm.
	prelude *engine.Program

	// output is where a function's console goes, per workload: one writer for
	// the whole tier would interleave every function on the node.
	output func(spec invoke.Spec) (stdout, stderr io.Writer)

	warn func(format string, args ...any)

	// realm installs more into each realm before its bundle runs. Only tests
	// set it, to give a handler a host-async call to wait on.
	realm func(*engine.Runtime) error
}

var _ invoke.Runtime = (*Runtime)(nil)

// Option configures a Runtime.
type Option func(*Runtime)

// WithMode picks the engine mode; empty is DefaultMode. A native mode on an
// arch with no native backend runs bytecode, and Mode reports that.
func WithMode(m Mode) Option {
	return func(r *Runtime) { r.mode = m }
}

// WithMaxArchive bounds the archive this tier fetches and what it unpacks to,
// so a member that claims to be small and inflates to gigabytes is refused
// rather than read. Default DefaultMaxArchive.
func WithMaxArchive(bytes int64) Option {
	return func(r *Runtime) { r.maxArchive = bytes }
}

// WithOutput is where a function's console goes, chosen per workload:
// console.log, info, debug and trace to stdout, warn and error to stderr. A nil
// function, or a nil writer, discards.
func WithOutput(sink func(spec invoke.Spec) (stdout, stderr io.Writer)) Option {
	return func(r *Runtime) { r.output = sink }
}

// WithWarn is where this tier says what an operator has to know but that is
// not a reason to refuse a workload.
func WithWarn(warn func(format string, args ...any)) Option {
	return func(r *Runtime) { r.warn = warn }
}

// New builds the tier.
func New(assets Assets, opts ...Option) (*Runtime, error) {
	if assets == nil {
		return nil, ErrNoAssets
	}
	r := &Runtime{assets: assets, mode: DefaultMode(), maxArchive: DefaultMaxArchive}
	for _, apply := range opts {
		apply(r)
	}
	if r.mode == "" {
		r.mode = DefaultMode()
	}
	switch r.mode {
	case ModeBytecode:
	case ModeNative, ModeJIT:
		if !engine.NativeSupported() {
			r.say("js: %s has no native backend on this arch; running bytecode", r.mode)
			r.mode = ModeBytecode
		}
	default:
		return nil, fmt.Errorf("%w: %q (want %s, %s or %s)", ErrUnknownMode, r.mode, ModeBytecode, ModeNative, ModeJIT)
	}
	if r.maxArchive <= 0 {
		return nil, fmt.Errorf("%w: %d bytes", ErrNoRoom, r.maxArchive)
	}

	prelude, err := engine.Compile(preludeName, preludeSource)
	if err != nil {
		return nil, fmt.Errorf("js: the prelude does not compile: %w", err)
	}
	r.prelude = prelude
	return r, nil
}

func (r *Runtime) Name() string { return Name }

// Mode is the mode this tier runs realms in, after any fallback to bytecode.
func (r *Runtime) Mode() Mode { return r.mode }

func (r *Runtime) say(format string, args ...any) {
	if r.warn != nil {
		r.warn(format, args...)
	}
}

// Start fetches an archive, starts a realm, evaluates the entry module and
// finds its handler. Its latency IS the cold start.
func (r *Runtime) Start(ctx context.Context, spec invoke.Spec) (invoke.Instance, error) {
	if spec.Runtime != "" && spec.Runtime != Name {
		return nil, fmt.Errorf("%w: %s runs on the %q tier, and this is the %s tier",
			invoke.ErrNoRuntime, spec.Name, spec.Runtime, Name)
	}
	if spec.Artifact == "" {
		return nil, fmt.Errorf("%w: %s", ErrNoArtifact, spec.Name)
	}
	if world := spec.Settings[SettingImplements]; world != "" && world != WorldHandler {
		return nil, fmt.Errorf("%w: %s is %q", ErrUnsupportedWorld, spec.Name, world)
	}
	if mounted := spec.Mounted(); len(mounted) > 0 {
		return nil, fmt.Errorf("%w: %s wants %s", ErrNoMounts, spec.Name, mounted[0].Label)
	}
	r.unserved(spec)

	b, err := r.fetch(ctx, spec.Artifact)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", spec.Name, err)
	}

	export := spec.Settings[SettingCall]
	if export == "" {
		export = DefaultExport
	}
	// Not returned directly: a nil *instance in an Instance is not nil.
	inst, err := r.instantiate(ctx, spec, b, export)
	if err != nil {
		return nil, err
	}
	return inst, nil
}

func (r *Runtime) fetch(ctx context.Context, artifact string) (*bundle, error) {
	content, err := r.assets.Get(ctx, artifact)
	if err != nil {
		return nil, fmt.Errorf("js: fetching %s: %w", artifact, err)
	}
	defer content.Close()

	data, err := io.ReadAll(io.LimitReader(content, r.maxArchive+1))
	if err != nil {
		return nil, fmt.Errorf("js: reading %s: %w", artifact, err)
	}
	if int64(len(data)) > r.maxArchive {
		return nil, fmt.Errorf("%w: %s is larger than %d bytes", ErrBadArchive, artifact, r.maxArchive)
	}
	return readBundle(data, r.maxArchive)
}

// unserved says which of the workload's grants have no host interface in
// JS yet. None does until the WIT bridge lands, so the handler's env is empty.
func (r *Runtime) unserved(spec invoke.Spec) {
	kinds := map[string]bool{}
	for _, g := range spec.Reaches {
		if g.Kind != invoke.MountKind {
			kinds[g.Kind] = true
		}
	}
	if len(kinds) == 0 {
		return
	}
	names := make([]string, 0, len(kinds))
	for kind := range kinds {
		names = append(names, kind)
	}
	sort.Strings(names)
	r.say("js: %s declares access this tier has no host interface for yet (%s); its env is empty",
		spec.Name, strings.Join(names, ", "))
}

// timeoutOf is the spec's own bound, or the pool's default when it states none.
func timeoutOf(spec invoke.Spec) time.Duration {
	if spec.Timeout > 0 {
		return spec.Timeout
	}
	return invoke.DefaultTimeout
}

// stackFor is the call-stack budget for an arena: maxStack, but never more
// than an eighth of the arena, which must leave room for the heap.
func stackFor(memory int64) int {
	return int(min(maxStack, memory/8))
}
