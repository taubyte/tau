package website

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	goHttp "net/http"
	"path"
	"strings"
	"time"

	commonIface "github.com/taubyte/tau/core/services/substrate/components"
	"github.com/taubyte/tau/core/vm"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
	vmContext "github.com/taubyte/tau/pkg/vm/context"
	"github.com/taubyte/tau/services/substrate/runtime"
)

// loadManifest looks for an SSR manifest inside the build asset zip and, when
// present, parses it and reconciles it with the website configuration. A static
// website (no manifest) simply leaves w.ssr nil.
func (w *Website) loadManifest(zipReader *zip.Reader) error {
	f, err := openZipFile(zipReader, websiteSpec.ManifestPath)
	if err != nil {
		// No manifest: classic static website.
		return nil
	}

	data, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		return fmt.Errorf("reading ssr manifest failed with: %w", err)
	}

	manifest, err := websiteSpec.ParseManifest(data)
	if err != nil {
		return err
	}

	// Website configuration overrides manifest defaults when explicitly set.
	if w.config.Entry != "" {
		manifest.Entry = w.config.Entry
	}
	if w.config.SSRMemory != 0 {
		manifest.Memory = w.config.SSRMemory
	}
	if w.config.SSRTimeout != 0 {
		manifest.Timeout = w.config.SSRTimeout
	}

	w.ssr = manifest
	if manifest.IsSSR() {
		// Reflect the asset's nature back onto the config so post-provision
		// matching and metrics see an SSR website even when it was not declared
		// explicitly in project configuration.
		w.config.Render = websiteSpec.RenderSSR
		if w.config.Framework == "" {
			w.config.Framework = manifest.Framework
		}

		// Read the server bundle eagerly, while the asset zip is still open, so
		// the runtime can be built lazily later without re-fetching the asset.
		if manifest.HandlerCID == "" {
			hf, err := openZipFile(zipReader, manifest.Handler)
			if err != nil {
				return fmt.Errorf("ssr handler `%s` missing from build asset: %w", manifest.Handler, err)
			}
			w.ssrHandlerData, err = io.ReadAll(hf)
			hf.Close()
			if err != nil {
				return fmt.Errorf("reading ssr handler `%s` failed with: %w", manifest.Handler, err)
			}
		}
	}

	return nil
}

// isSSR reports whether this website serves dynamic content.
func (w *Website) isSSR() bool {
	return w.ssr != nil && w.ssr.IsSSR()
}

// resolveStaticAsset maps a site-root relative request path to the real asset
// file that serves it, applying the directory-index fallback (a clean URL like
// "/about" resolves to "/about/index.html"). The returned path is the actual
// file, so the asset handler can stat it directly instead of failing on the
// bare directory path. Files under the internal SSR directory are never treated
// as static assets.
func (w *Website) resolveStaticAsset(p string) (string, bool) {
	if _, ok := w.assetFiles[p]; ok {
		return p, true
	}
	idx := path.Join(p, "index.html")
	if _, ok := w.assetFiles[idx]; ok {
		return idx, true
	}
	return "", false
}

// isStaticAsset reports whether a site-root relative path is backed by a static
// file in the build asset.
func (w *Website) isStaticAsset(p string) bool {
	_, ok := w.resolveStaticAsset(p)
	return ok
}

// serveSSRFunction renders via the function ABI: instantiate a pooled runtime,
// create the HTTP event, and call the exported entry. Mirrors a regular Taubyte
// function.
func (w *Website) serveSSRFunction(_w goHttp.ResponseWriter, r *goHttp.Request) (time.Time, error) {
	rt, err := w.ssrRuntimeReady()
	if err != nil {
		return time.Time{}, fmt.Errorf("ssr runtime unavailable for website `%s`: %w", w.config.Name, err)
	}

	instance, err := rt.Instantiate(w.instanceCtx)
	if err != nil {
		return time.Time{}, fmt.Errorf("ssr instantiate failed with: %w", err)
	}
	defer instance.Free()

	ev := instance.SDK().CreateHttpEvent(_w, r)

	return time.Now(), rt.Call(instance, ev.Id)
}

// serveSSRStdio renders via the WASI-stdio ABI: per request, instantiate the
// server bundle with the serialized request on stdin and read the serialized
// response from stdout. This hosts JavaScript engines compiled to WebAssembly
// (e.g. Javy/QuickJS), whose I/O is WASI stdio based.
func (w *Website) serveSSRStdio(_w goHttp.ResponseWriter, r *goHttp.Request) (time.Time, error) {
	cid, err := w.ssrHandlerCID()
	if err != nil {
		return time.Time{}, fmt.Errorf("resolving ssr handler failed with: %w", err)
	}

	reqBytes, err := encodeStdioRequest(r)
	if err != nil {
		return time.Time{}, err
	}

	ctx, cancel := context.WithTimeout(w.instanceCtx, time.Duration(w.ssr.Timeout))
	defer cancel()

	vmCtx, err := vmContext.New(ctx,
		vmContext.Project(w.project),
		vmContext.Application(w.application),
		vmContext.Resource(w.config.Id),
		vmContext.Commit(w.commit),
		vmContext.Branch(w.branch),
	)
	if err != nil {
		return time.Time{}, fmt.Errorf("creating vm context failed with: %w", err)
	}

	inst, err := w.srv.Vm().New(vmCtx, vm.Config{
		MemoryLimitPages: ssrMemoryPages(w.ssr.Memory),
		Output:           vm.Buffer,
		Stdin:            bytes.NewReader(reqBytes),
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("creating vm instance failed with: %w", err)
	}
	defer inst.Close()

	rt, err := inst.Runtime(nil)
	if err != nil {
		return time.Time{}, fmt.Errorf("creating runtime failed with: %w", err)
	}
	defer rt.Close()

	// Instantiating the module runs its WASI `_start`, which reads the request
	// from stdin and writes the response to stdout.
	if _, err := rt.Module("/dfs/" + cid); err != nil {
		return time.Time{}, fmt.Errorf("running ssr bundle failed with: %w", err)
	}

	out, err := io.ReadAll(inst.Stdout())
	if err != nil {
		return time.Time{}, fmt.Errorf("reading ssr output failed with: %w", err)
	}

	t := time.Now()
	return t, writeStdioResponse(_w, out)
}

// ssrHandlerCID resolves (once) the DAG cid of the server bundle, adding the
// embedded bytes to the node on first use.
func (w *Website) ssrHandlerCID() (string, error) {
	w.ssrCIDOnce.Do(func() {
		if w.ssr.HandlerCID != "" {
			w.ssrCID = w.ssr.HandlerCID
			return
		}
		if len(w.ssrHandlerData) == 0 {
			w.ssrCIDErr = fmt.Errorf("ssr handler bytes unavailable")
			return
		}
		w.ssrCID, w.ssrCIDErr = w.srv.Node().AddFile(bytes.NewReader(w.ssrHandlerData))
	})
	return w.ssrCID, w.ssrCIDErr
}

// ssrRuntimeReady lazily builds (once) and returns the server bundle runtime.
func (w *Website) ssrRuntimeReady() (*runtime.Function, error) {
	w.ssrOnce.Do(func() {
		w.ssrRuntime, w.ssrErr = w.buildSSRRuntime()
	})
	return w.ssrRuntime, w.ssrErr
}

// buildSSRRuntime resolves the server bundle to a DAG asset and wires it into
// the regular WebAssembly function runtime via a synthetic function serviceable
// sourced from `/dfs/<cid>`.
func (w *Website) buildSSRRuntime() (*runtime.Function, error) {
	manifest := w.ssr
	if manifest == nil || !manifest.IsSSR() {
		return nil, fmt.Errorf("website is not server side rendered")
	}

	cid, err := w.ssrHandlerCID()
	if err != nil {
		return nil, fmt.Errorf("storing ssr handler in dag failed with: %w", err)
	}
	w.ssrHandlerCid = cid

	fn := &structureSpec.Function{
		Id:      w.config.Id,
		Name:    w.config.Name,
		Source:  "/dfs/" + cid,
		Call:    manifest.Entry,
		Memory:  manifest.Memory,
		Timeout: manifest.Timeout,
	}

	return runtime.New(w.instanceCtx, &ssrServiceable{Website: w, fn: fn, assetId: cid})
}

// ssrMaxRequestBody bounds the request body forwarded to a WASI-stdio bundle.
const ssrMaxRequestBody = 8 << 20 // 8 MiB

// stdioRequest / stdioResponse are the JSON envelopes exchanged with a
// WASI-stdio server bundle over stdin/stdout.
type stdioRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body,omitempty"`
}

type stdioResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// encodeStdioRequest serializes the request for a WASI-stdio bundle.
func encodeStdioRequest(r *goHttp.Request) ([]byte, error) {
	headers := make(map[string]string, len(r.Header)+2)
	for k := range r.Header {
		headers[k] = r.Header.Get(k)
	}

	// Go promotes the Host header onto r.Host and drops it from r.Header, so
	// forward it explicitly: the bundle reconstructs the request origin from it
	// (Host + scheme), and frameworks such as SvelteKit compare that origin
	// against the Origin header for CSRF protection — without it every form POST
	// is rejected as cross-site. Likewise propagate the scheme so the
	// reconstructed origin's protocol matches the browser's Origin header.
	if r.Host != "" {
		headers["Host"] = r.Host
	}
	if _, ok := headers["X-Forwarded-Proto"]; !ok {
		scheme := "http"
		if r.TLS != nil || strings.EqualFold(r.URL.Scheme, "https") {
			scheme = "https"
		}
		headers["X-Forwarded-Proto"] = scheme
	}

	var body string
	if r.Body != nil {
		b, err := io.ReadAll(io.LimitReader(r.Body, ssrMaxRequestBody))
		if err != nil {
			return nil, fmt.Errorf("reading request body failed with: %w", err)
		}
		body = string(b)
	}

	return json.Marshal(stdioRequest{
		Method:  r.Method,
		URL:     r.URL.RequestURI(),
		Headers: headers,
		Body:    body,
	})
}

// writeStdioResponse decodes the bundle's stdout envelope and writes it to the
// HTTP response.
func writeStdioResponse(w goHttp.ResponseWriter, out []byte) error {
	var resp stdioResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return fmt.Errorf("ssr bundle produced an invalid response: %w", err)
	}

	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	if resp.Status == 0 {
		resp.Status = 200
	}
	w.WriteHeader(resp.Status)
	_, err := w.Write([]byte(resp.Body))
	return err
}

// ssrMemoryPages converts a byte memory limit to WASM pages, clamped to the
// runtime maximum (0 means use the maximum).
func ssrMemoryPages(memory uint64) uint32 {
	if memory == 0 {
		return vm.MemoryLimitPages
	}
	pages := memory / uint64(vm.MemoryPageSize)
	if memory%uint64(vm.MemoryPageSize) != 0 {
		pages++
	}
	if pages > uint64(vm.MemoryLimitPages) {
		pages = uint64(vm.MemoryLimitPages)
	}
	return uint32(pages)
}

// openZipFile opens a named entry from a zip reader, tolerating an optional
// leading slash.
func openZipFile(zipReader *zip.Reader, name string) (io.ReadCloser, error) {
	if f, err := zipReader.Open(name); err == nil {
		return f, nil
	}
	return zipReader.Open(strings.TrimPrefix(name, "/"))
}

// ssrServiceable adapts a Website into a function serviceable so the existing
// WebAssembly runtime can host its server bundle. Lifecycle, project/app/commit
// metadata and the substrate service are inherited from the website; only the
// function configuration and asset identity differ.
type ssrServiceable struct {
	*Website
	fn      *structureSpec.Function
	assetId string
}

var _ commonIface.FunctionServiceable = (*ssrServiceable)(nil)

func (s *ssrServiceable) Config() *structureSpec.Function {
	return s.fn
}

func (s *ssrServiceable) AssetId() string {
	return s.assetId
}

// Close is a no-op: the parent Website owns the runtime lifecycle and shuts the
// server bundle down when it is closed.
func (s *ssrServiceable) Close() {}
