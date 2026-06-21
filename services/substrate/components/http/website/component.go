package website

import (
	"context"
	"fmt"
	"io"
	goHttp "net/http"
	"time"

	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
)

// ComponentRuntime runs a WebAssembly Component (Component Model + WASI) server
// bundle — the slot for a richer JavaScript engine such as StarlingMonkey
// (SpiderMonkey on wasm), which provides a near-browser Web-API surface that the
// Javy/wasi-stdio tier polyfills by hand.
//
// wazero (Taubyte's default VM) runs core modules + WASI Preview 1 and cannot
// run components; the wasmtime-go bindings can't either (no Component Model API).
// So the reference backend (./wasmtimehttp, build tag `wasmtime_component`)
// shells out to the `wasmtime` CLI — whose `wasmtime serve` implements the full
// wasi:http host — and reverse-proxies to it. It lives in its own package/build
// and registers here via RegisterComponentRuntime, keeping that dependency out
// of the substrate core. Until a backend registers, ABIComponent assets fail
// fast (see engine.go / serveSSR). See docs/js-runtime-roadmap.md.
type ComponentRuntime interface {
	// ServeHTTP renders r through the component server bundle and writes the
	// response. key identifies the bundle (the DAG cid) so the backend can cache
	// the compiled component; component is its bytes (used on first compile).
	ServeHTTP(ctx context.Context, key string, component []byte, w goHttp.ResponseWriter, r *goHttp.Request, limits ComponentLimits) error

	// Name identifies the backend, e.g. "wasmtime/starlingmonkey".
	Name() string
}

// ComponentLimits bound a component invocation.
type ComponentLimits struct {
	MemoryBytes uint64
	Timeout     time.Duration
}

// componentRuntime is the registered backend, if any. Set once at init time.
var componentRuntime ComponentRuntime

// RegisterComponentRuntime wires a component-model backend and enables the
// ABIComponent engine. A backend package calls this from its init():
//
//	func init() { website.RegisterComponentRuntime(myWasmtimeBackend{}) }
func RegisterComponentRuntime(rt ComponentRuntime) {
	componentRuntime = rt
	ssrEngines[websiteSpec.ABIComponent] = (*Website).serveSSRComponent
}

// componentBindingsInjector, when set, populates a request's internal binding
// headers before it is proxied to the component: `x-taubyte-env` (a JSON object
// of secrets/config spread onto the handler's `env`) and `x-taubyte-bindings`
// (the loopback base URL of a substrate endpoint the component fetches for
// env.KV / env.STORAGE). The component shim reads and strips these headers (see
// tools/taubyte-ssr-adapter/shim/component.js).
var componentBindingsInjector func(w *Website, r *goHttp.Request)

// RegisterComponentBindings wires per-request env/KV/storage bindings into the
// component path. Optional — without it, components run with an empty `env`
// (env.ASSETS still 404s and KV/STORAGE are absent). The injector resolves the
// website's secrets and a per-website KV/storage endpoint from Taubyte services.
func RegisterComponentBindings(f func(w *Website, r *goHttp.Request)) {
	componentBindingsInjector = f
}

// serveSSRComponent renders via a registered component-model engine.
func (w *Website) serveSSRComponent(_w goHttp.ResponseWriter, r *goHttp.Request) (time.Time, error) {
	if componentRuntime == nil {
		return time.Time{}, fmt.Errorf(
			"website `%s`: handler abi `%s` needs a component-model runtime backend, which is not in this substrate build",
			w.config.Name, websiteSpec.ABIComponent,
		)
	}

	cid, err := w.ssrHandlerCID()
	if err != nil {
		return time.Time{}, fmt.Errorf("resolving component handler failed with: %w", err)
	}
	component, err := w.handlerBytes()
	if err != nil {
		return time.Time{}, fmt.Errorf("reading component handler failed with: %w", err)
	}

	ctx, cancel := context.WithTimeout(w.instanceCtx, time.Duration(w.ssr.Timeout))
	defer cancel()

	// Inject per-request env/KV/storage bindings (secrets + binding endpoint) for
	// the component shim to consume, if a provider is registered.
	if componentBindingsInjector != nil {
		componentBindingsInjector(w, r)
	}

	err = componentRuntime.ServeHTTP(ctx, cid, component, _w, r, ComponentLimits{
		MemoryBytes: w.ssr.Memory,
		Timeout:     time.Duration(w.ssr.Timeout),
	})
	return time.Now(), err
}

// handlerBytes returns the server bundle bytes, from the asset (the common case)
// or by fetching the manifest's HandlerCID from the node.
func (w *Website) handlerBytes() ([]byte, error) {
	if len(w.ssrHandlerData) > 0 {
		return w.ssrHandlerData, nil
	}
	cid, err := w.ssrHandlerCID()
	if err != nil {
		return nil, err
	}
	rc, err := w.srv.Node().GetFile(w.srv.Context(), cid)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}
