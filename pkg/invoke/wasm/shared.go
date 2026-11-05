package wasm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/samyfodil/wazy"
	"github.com/samyfodil/wazy/api"
	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// shared.go is one wazy runtime per compiled artifact, which every instance of it
// shares. A component's core modules compile once per runtime, and wazy's compile
// cache is held to the runtime that compiled it, so a runtime per instance compiled
// every cold start from scratch: 58ms for the handler fixture, against 2.7ms on a
// shared one (BenchmarkStartCold). A runtime no instance holds closes after Keep.

// DefaultKeep is how long a runtime no instance holds stays compiled.
const DefaultKeep = 5 * time.Minute

// WithKeep is how long a runtime no instance holds stays compiled, for a cold start
// soon after to find it; zero is DefaultKeep.
func WithKeep(d time.Duration) Option {
	return func(r *Runtime) { r.keep = d }
}

// runtimeKey is what a runtime is compiled for: the artifact, which is a content id
// and so never changes under its name, and the memory its instances may grow to,
// which wazy fixes per runtime.
type runtimeKey struct {
	artifact string
	pages    uint32
}

// compiled is one runtime and what it keeps: the component's bytes and the core
// modules compiled from them.
type compiled struct {
	key     runtimeKey
	runtime wazy.Runtime
	cache   *component.CompileCache
	code    []byte

	// refs and idle are guarded by the Runtime's mu.
	refs int
	idle *time.Timer
}

func (c *compiled) close(ctx context.Context) error {
	return errors.Join(c.cache.Close(ctx), c.runtime.Close(ctx))
}

// held is one instance on a compiled runtime, released when it closes.
type held struct {
	r        *Runtime
	compiled *compiled
	instance *component.Instance
}

func (h *held) Close() error {
	err := h.instance.Close(context.Background())
	h.r.release(h.compiled)
	return err
}

// acquire is the runtime spec's artifact runs on, compiled at most once while any
// instance holds it or within Keep of the last one closing.
func (r *Runtime) acquire(ctx context.Context, spec invoke.Spec) (*compiled, error) {
	key := runtimeKey{artifact: spec.Artifact, pages: memoryPages(spec.Memory)}
	if c := r.take(key); c != nil {
		return c, nil
	}

	content, err := r.assets.Read(ctx, spec.Artifact)
	if err != nil {
		return nil, fmt.Errorf("wasm: fetching %s: %w", spec.Artifact, err)
	}
	defer content.Close()
	code, err := io.ReadAll(content)
	if err != nil {
		return nil, fmt.Errorf("wasm: reading %s: %w", spec.Artifact, err)
	}

	config := r.mode.config().
		WithCompilationCache(r.cache).
		// WebAssembly 3.0, because toolchains emit it: componentize-py's
		// CPython needs extended-const.
		WithCoreFeatures(api.CoreFeaturesV3)
	if key.pages > 0 {
		config = config.WithMemoryLimitPages(key.pages)
	}
	made := &compiled{
		key:     key,
		runtime: wazy.NewRuntimeWithConfig(ctx, config),
		cache:   component.NewCompileCache(),
		code:    code,
	}

	// TWO FIRST STARTS RACE TO MAKE IT, and the one that loses closes what it made.
	r.mu.Lock()
	if c, ok := r.compiled[key]; ok {
		c.hold()
		r.mu.Unlock()
		made.close(ctx)
		return c, nil
	}
	if r.compiled == nil {
		r.compiled = map[runtimeKey]*compiled{}
	}
	r.compiled[key] = made
	made.hold()
	r.mu.Unlock()
	return made, nil
}

// take is the runtime compiled for key, held, or nil when there is none.
func (r *Runtime) take(key runtimeKey) *compiled {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.compiled[key]
	if !ok {
		return nil
	}
	c.hold()
	return c
}

// hold is one more instance on c. Called holding the Runtime's mu.
func (c *compiled) hold() {
	c.refs++
	if c.idle != nil {
		c.idle.Stop()
		c.idle = nil
	}
}

// release is one instance fewer on c, which closes after Keep once none holds it.
func (r *Runtime) release(c *compiled) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c.refs--
	if c.refs > 0 {
		return
	}
	keep := r.keep
	if keep <= 0 {
		keep = DefaultKeep
	}
	c.idle = time.AfterFunc(keep, func() { r.retire(c) })
}

// retire closes c unless an instance took it again since it was let go.
func (r *Runtime) retire(c *compiled) {
	r.mu.Lock()
	if c.refs > 0 || r.compiled[c.key] != c {
		r.mu.Unlock()
		return
	}
	delete(r.compiled, c.key)
	r.mu.Unlock()
	c.close(context.Background())
}

// closeCompiled closes every runtime this tier compiled, held or not: the tier is
// going, and so is everything that runs on it.
func (r *Runtime) closeCompiled(ctx context.Context) error {
	r.mu.Lock()
	all := r.compiled
	r.compiled = nil
	for _, c := range all {
		if c.idle != nil {
			c.idle.Stop()
		}
	}
	r.mu.Unlock()
	var errs []error
	for _, c := range all {
		errs = append(errs, c.close(ctx))
	}
	return errors.Join(errs...)
}
