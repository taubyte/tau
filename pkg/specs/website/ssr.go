package websiteSpec

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// Render modes a website can be served with.
const (
	// RenderStatic serves the website as a bundle of static files (default,
	// the historical Taubyte behaviour). Single Page Application routing is
	// applied as a fallback.
	RenderStatic = "static"

	// RenderSSR serves dynamic routes (and `/api` endpoints) through a
	// WebAssembly server bundle while still serving immutable assets directly
	// from the build output.
	RenderSSR = "ssr"
)

// RouteType classifies how a request path is fulfilled by an SSR website.
type RouteType string

const (
	// RouteStatic is served directly from the build asset (immutable assets,
	// pre-rendered html, the public/ directory, ...).
	RouteStatic RouteType = "static"

	// RouteSSR is rendered on demand by the server bundle.
	RouteSSR RouteType = "ssr"

	// RouteAPI is handled by the server bundle's API handler (`/api/*`). It is
	// functionally identical to RouteSSR at the runtime level but is kept
	// distinct so metrics/observability can tell pages from endpoints.
	RouteAPI RouteType = "api"
)

// Handler ABIs: how the runtime invokes the server bundle.
const (
	// ABIFunction (the default) calls an exported function with a Taubyte HTTP
	// event id; the bundle uses the go-sdk host ABI to read the request and
	// write the response. This is what a hand-written Taubyte function or a
	// TinyGo handler uses.
	ABIFunction = "function"

	// ABIWasiStdio runs the bundle as a WASI program, piping the serialized
	// request to stdin and reading the response from stdout. This suits a
	// JavaScript engine compiled to WebAssembly (e.g. Javy/QuickJS), whose I/O
	// is WASI stdio based, and is what the taubyte-ssr-adapter targets.
	ABIWasiStdio = "wasi-stdio"

	// ABIComponent is a WebAssembly Component (Component Model + WASI) that
	// handles requests via a wasi:http-style export. It is the slot for a richer
	// JS engine (StarlingMonkey/SpiderMonkey) with a full Web-API surface. The
	// manifest may declare it, but a given substrate build only serves it once a
	// component-model runtime backend is wired in (see docs/js-runtime-roadmap.md);
	// otherwise serving fails fast with a clear "unsupported" error.
	ABIComponent = "component"
)

const (
	// ManifestPath is the well known location, inside a website build asset
	// (the build zip), of the SSR manifest. Its presence is what switches a
	// website from static to SSR serving, keeping the feature fully backwards
	// compatible: assets produced before this feature simply have no manifest.
	ManifestPath = "__taubyte__/ssr.json"

	// DefaultHandlerPath is the default location, inside the build asset, of
	// the server bundle compiled to WebAssembly. It is a function style zip
	// (an archive containing `artifact.wasm`) so it can be loaded by the exact
	// same machinery that loads regular Taubyte functions.
	DefaultHandlerPath = "__taubyte__/handler.wasm.zip"

	// DefaultEntry is the WebAssembly export the runtime calls for every SSR /
	// API request when the manifest does not specify one.
	DefaultEntry = "handle"

	// ManifestVersion is the schema version emitted by this implementation.
	ManifestVersion = "1"
)

// Sensible runtime defaults for a server bundle when the manifest leaves them
// unset. They mirror the order of magnitude used by regular functions.
const (
	DefaultSSRMemory  uint64 = 256 << 20 // 256 MiB
	DefaultSSRTimeout uint64 = uint64(30 * time.Second)
)

// Route is a single routing rule matched against the request path by longest
// prefix. A pattern of "/" (or "") matches everything and acts as a catch all.
type Route struct {
	Pattern string    `json:"pattern"`
	Type    RouteType `json:"type"`
}

// Manifest is the self describing contract embedded in an SSR website build
// asset. The build (framework adapter) produces it, the substrate runtime
// consumes it. Keeping the routing decision in the asset—rather than in
// platform config—means a deployment is reproducible and self contained.
type Manifest struct {
	Version   string `json:"version"`
	Framework string `json:"framework,omitempty"`

	// Render is RenderSSR for server rendered sites. RenderStatic is accepted
	// (and means "behave like a classic static site") so a framework adapter
	// can always emit a manifest.
	Render string `json:"render"`

	// Entry is the WebAssembly export invoked for SSR/API requests. It applies
	// to the ABIFunction calling convention; it is ignored for ABIWasiStdio.
	Entry string `json:"entry,omitempty"`

	// ABI is how the runtime invokes the server bundle: ABIFunction (default)
	// or ABIWasiStdio (stdin/stdout, for JS engines compiled to wasm).
	ABI string `json:"abi,omitempty"`

	// Handler is the path, inside the build asset, of the server bundle wasm
	// archive. HandlerCID, when set, points at an already stored DAG asset and
	// takes precedence over Handler (avoiding a re-add at provision time).
	Handler    string `json:"handler,omitempty"`
	HandlerCID string `json:"handlerCid,omitempty"`

	// Memory (bytes) and Timeout (nanoseconds) bound the server bundle VM.
	Memory  uint64 `json:"memory,omitempty"`
	Timeout uint64 `json:"timeout,omitempty"`

	// Static lists path prefixes that must always be served from the build
	// asset, never the server bundle (e.g. "/_next/static/", "/assets/").
	Static []string `json:"static,omitempty"`

	// Routes are explicit classification rules. When empty every non-static
	// request falls through to Fallback.
	Routes []Route `json:"routes,omitempty"`

	// Fallback classifies any request not matched by Static or Routes. It
	// defaults to RouteSSR.
	Fallback RouteType `json:"fallback,omitempty"`
}

// ManifestDir returns the directory, inside a build asset, that holds the SSR
// manifest and (by default) the server bundle.
func ManifestDir() string {
	return path.Dir(ManifestPath)
}

// ParseManifest decodes, defaults and validates a manifest.
func ParseManifest(data []byte) (*Manifest, error) {
	m := &Manifest{}
	if err := json.Unmarshal(data, m); err != nil {
		return nil, fmt.Errorf("decoding ssr manifest failed with: %w", err)
	}

	m.SetDefaults()
	if err := m.Validate(); err != nil {
		return nil, err
	}

	return m, nil
}

// Marshal serialises the manifest as it should be embedded in a build asset.
func (m *Manifest) Marshal() ([]byte, error) {
	return json.Marshal(m)
}

// SetDefaults fills unset fields with their defaults. It is idempotent.
func (m *Manifest) SetDefaults() {
	if m.Version == "" {
		m.Version = ManifestVersion
	}
	if m.Render == "" {
		m.Render = RenderSSR
	}
	if m.Entry == "" {
		m.Entry = DefaultEntry
	}
	if m.IsSSR() {
		if m.ABI == "" {
			m.ABI = ABIFunction
		}
		if m.Handler == "" && m.HandlerCID == "" {
			m.Handler = DefaultHandlerPath
		}
		if m.Memory == 0 {
			m.Memory = DefaultSSRMemory
		}
		if m.Timeout == 0 {
			m.Timeout = DefaultSSRTimeout
		}
		if m.Fallback == "" {
			m.Fallback = RouteSSR
		}
	} else if m.Fallback == "" {
		m.Fallback = RouteStatic
	}
}

// ABIOrDefault returns the configured handler ABI, defaulting to ABIFunction.
func (m *Manifest) ABIOrDefault() string {
	if m.ABI == "" {
		return ABIFunction
	}
	return m.ABI
}

// Validate reports whether the manifest is internally consistent.
func (m *Manifest) Validate() error {
	switch m.Render {
	case RenderStatic, RenderSSR:
	default:
		return fmt.Errorf("invalid render mode `%s`, expected `%s` or `%s`", m.Render, RenderStatic, RenderSSR)
	}

	if m.IsSSR() {
		switch m.ABIOrDefault() {
		case ABIFunction:
			if m.Entry == "" {
				return fmt.Errorf("ssr manifest is missing an entry point")
			}
		case ABIWasiStdio, ABIComponent:
		default:
			return fmt.Errorf("invalid handler abi `%s`, expected `%s`, `%s` or `%s`", m.ABI, ABIFunction, ABIWasiStdio, ABIComponent)
		}
		if m.Handler == "" && m.HandlerCID == "" {
			return fmt.Errorf("ssr manifest is missing a handler (path or cid)")
		}
	}

	for _, r := range m.Routes {
		switch r.Type {
		case RouteStatic, RouteSSR, RouteAPI:
		default:
			return fmt.Errorf("invalid route type `%s` for pattern `%s`", r.Type, r.Pattern)
		}
	}

	return nil
}

// IsSSR reports whether the website renders dynamically.
func (m *Manifest) IsSSR() bool {
	return m != nil && m.Render == RenderSSR
}

// fallback returns the configured fallback route type, defaulting to SSR.
func (m *Manifest) fallback() RouteType {
	if m.Fallback != "" {
		return m.Fallback
	}
	if m.IsSSR() {
		return RouteSSR
	}
	return RouteStatic
}

// Classify resolves how a request path should be served. Static prefixes and
// explicit routes are matched by longest prefix; the most specific rule wins,
// with explicit routes taking precedence over static prefixes on a tie. When
// nothing matches, the manifest fallback is used.
//
// Classify expresses the manifest's intent only. The runtime additionally
// downgrades any SSR/API decision to static when the path resolves to a real
// file in the build asset, so genuine assets are always served directly even
// if the manifest is coarse.
func (m *Manifest) Classify(path string) RouteType {
	if path == "" {
		path = "/"
	}

	bestType := m.fallback()
	bestSpec := -1

	consider := func(pattern string, rt RouteType) {
		if spec, ok := patternSpecificity(pattern, path); ok && spec >= bestSpec {
			bestSpec = spec
			bestType = rt
		}
	}

	for _, s := range m.Static {
		consider(s, RouteStatic)
	}
	// Explicit routes are considered after static prefixes so that, on an equal
	// specificity tie, an author defined route wins.
	for _, r := range m.Routes {
		consider(r.Pattern, r.Type)
	}

	return bestType
}

// StaticPrefixes returns the static prefixes sorted longest first, which is the
// order a router wants to test them in.
func (m *Manifest) StaticPrefixes() []string {
	out := make([]string, len(m.Static))
	copy(out, m.Static)
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// patternSpecificity returns the match length (used to pick the most specific
// rule) and whether pattern matches path. A pattern matches when it equals the
// path or is one of its leading path segments. "/" (or "") matches everything
// with the lowest possible specificity so it behaves as a catch all.
func patternSpecificity(pattern, path string) (int, bool) {
	p := strings.TrimSuffix(pattern, "/")
	if p == "" {
		return 0, true
	}
	if path == p || strings.HasPrefix(path, p+"/") {
		return len(p), true
	}
	return 0, false
}
