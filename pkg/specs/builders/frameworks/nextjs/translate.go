// Package nextjs translates a Next.js production build (`.next/`) into the
// Taubyte website serving model: the SSR manifest plus a report describing which
// routes are pre-rendered (served statically), dynamic, API, or guarded by
// middleware.
//
// This is the routing brain of an OpenNext-style adapter. It is deliberately
// defensive: Next's manifest schemas shift between versions, so only the small,
// stable subset a router needs is parsed; unknown fields are ignored.
//
// It does not, by itself, execute Next's server code — that is the runtime layer
// (a Web-API/Node-capable JS bundle) the adapter builds on top. See
// docs/nextjs-adapter.md.
package nextjs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
)

// Well-known locations inside a Next.js build.
const (
	BuildDir           = ".next"
	routesManifest     = "routes-manifest.json"
	prerenderManifest  = "prerender-manifest.json"
	middlewareManifest = "server/middleware-manifest.json"

	// StaticPrefix is where Next serves immutable hashed assets.
	staticPrefix = "/_next/static/"
)

// Report summarises how a Next build maps onto Taubyte serving. The adapter uses
// it to assemble the asset (copy pre-rendered HTML + static dirs) and for logs.
type Report struct {
	BasePath           string
	PrerenderedRoutes  []string // served statically from the asset
	DynamicRoutes      []string // rendered on demand (e.g. /blog/[slug])
	APIRoutes          []string // route handlers / api routes
	HasMiddleware      bool
	MiddlewareMatchers []string

	handlerEmbedded bool
}

// ---- minimal manifest subsets (defensive) -------------------------------

type routesManifestDoc struct {
	BasePath      string          `json:"basePath"`
	StaticRoutes  []routePageOnly `json:"staticRoutes"`
	DynamicRoutes []routePageOnly `json:"dynamicRoutes"`
}

type routePageOnly struct {
	Page string `json:"page"`
}

type prerenderManifestDoc struct {
	Routes        map[string]json.RawMessage `json:"routes"`
	DynamicRoutes map[string]json.RawMessage `json:"dynamicRoutes"`
}

type middlewareManifestDoc struct {
	Middleware map[string]middlewareEntry `json:"middleware"`
	Functions  map[string]json.RawMessage `json:"functions"`
}

type middlewareEntry struct {
	Matchers []struct {
		RegExp string `json:"regexp"`
	} `json:"matchers"`
}

// Translate reads a Next.js build directory (the project root containing
// `.next/`) and returns the Taubyte SSR manifest plus a Report. The manifest
// uses the wasi-stdio ABI (the Next handler runs as a JS-in-wasm bundle).
func Translate(projectDir string) (*websiteSpec.Manifest, *Report, error) {
	nextDir := filepath.Join(projectDir, BuildDir)
	if info, err := os.Stat(nextDir); err != nil || !info.IsDir() {
		return nil, nil, fmt.Errorf("no Next.js build found at `%s` (run `next build`)", nextDir)
	}

	rep := &Report{}

	var routes routesManifestDoc
	if err := readJSON(filepath.Join(nextDir, routesManifest), &routes); err != nil {
		return nil, nil, fmt.Errorf("reading %s failed with: %w", routesManifest, err)
	}
	rep.BasePath = strings.TrimSuffix(routes.BasePath, "/")
	for _, r := range append(routes.StaticRoutes, routes.DynamicRoutes...) {
		if isAPIRoute(r.Page) {
			rep.APIRoutes = append(rep.APIRoutes, r.Page)
		}
	}
	for _, r := range routes.DynamicRoutes {
		if !isAPIRoute(r.Page) {
			rep.DynamicRoutes = append(rep.DynamicRoutes, r.Page)
		}
	}

	// Pre-rendered (SSG/ISR) pages are optional and only present when the app
	// has them.
	var prerender prerenderManifestDoc
	if err := readJSON(filepath.Join(nextDir, prerenderManifest), &prerender); err == nil {
		for route := range prerender.Routes {
			rep.PrerenderedRoutes = append(rep.PrerenderedRoutes, route)
		}
	}

	// Middleware is optional.
	var mw middlewareManifestDoc
	if err := readJSON(filepath.Join(nextDir, middlewareManifest), &mw); err == nil {
		for _, entry := range mw.Middleware {
			rep.HasMiddleware = true
			for _, m := range entry.Matchers {
				if m.RegExp != "" {
					rep.MiddlewareMatchers = append(rep.MiddlewareMatchers, m.RegExp)
				}
			}
		}
	}

	sort.Strings(rep.PrerenderedRoutes)
	sort.Strings(rep.DynamicRoutes)
	sort.Strings(rep.APIRoutes)
	sort.Strings(rep.MiddlewareMatchers)

	return rep.Manifest(), rep, nil
}

// Manifest builds the Taubyte SSR manifest implied by the report.
func (rep *Report) Manifest() *websiteSpec.Manifest {
	base := rep.BasePath

	m := &websiteSpec.Manifest{
		Framework: "nextjs",
		Render:    websiteSpec.RenderSSR,
		ABI:       websiteSpec.ABIWasiStdio,
		Static:    []string{base + staticPrefix},
		Routes: []websiteSpec.Route{
			{Pattern: base + "/api/", Type: websiteSpec.RouteAPI},
			{Pattern: base + "/", Type: websiteSpec.RouteSSR},
		},
	}

	// Pre-rendered pages are emitted as files into the asset, so the runtime's
	// static-file check serves them directly; recording them as static routes
	// keeps classification/metrics honest.
	for _, route := range rep.PrerenderedRoutes {
		if route == "/" || isDynamicRoute(route) {
			continue // "/" stays SSR catch-all; dynamic prerenders are ISR
		}
		m.Routes = append(m.Routes, websiteSpec.Route{Pattern: base + route, Type: websiteSpec.RouteStatic})
	}

	m.SetDefaults()
	return m
}

func readJSON(path string, v interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func isAPIRoute(page string) bool {
	return page == "/api" || strings.HasPrefix(page, "/api/")
}

// isDynamicRoute reports whether a Next route page contains a dynamic segment.
func isDynamicRoute(page string) bool {
	return strings.ContainsAny(page, "[:*")
}
