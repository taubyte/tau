// Package frameworks describes the popular JavaScript web frameworks Taubyte
// can host, how to recognise them from a repository, how to build them, and the
// render mode (static vs server side rendering) they default to.
//
// It is the source of truth that lets the build pipeline turn an arbitrary
// Next.js / Nuxt / SvelteKit / Vite / Express / ... repository into the
// normalised website asset (static files + optional SSR handler + manifest)
// the substrate runtime knows how to serve.
package frameworks

import (
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
)

// RenderMode is the default way a framework is served.
type RenderMode = string

const (
	// ModeStatic builds to a directory of static assets (SPA / SSG).
	ModeStatic RenderMode = websiteSpec.RenderStatic
	// ModeSSR builds to static assets plus a server bundle rendered on demand.
	ModeSSR RenderMode = websiteSpec.RenderSSR
)

// Framework describes one supported web framework.
type Framework struct {
	// Name is the canonical identifier, also written into the SSR manifest.
	Name  string
	Title string

	// Mode is the default render mode for the framework.
	Mode RenderMode

	// Dependencies are package.json dependency names; the presence of any of
	// them in a repository signals this framework.
	Dependencies []string

	// ConfigFiles are file names whose presence (additionally) signals the
	// framework, e.g. "next.config.js".
	ConfigFiles []string

	// Priority disambiguates when several frameworks match. Meta and server
	// frameworks sit above the base libraries they build on (Next.js above
	// React, Nuxt above Vue, ...).
	Priority int

	// Image is the default container image used to build the framework.
	Image string

	// Install / Build are the default package-manager commands. They are only
	// used when the repository does not ship its own build configuration.
	Install string
	Build   string

	// StaticDir is the directory, relative to the repository root, that holds
	// the static assets produced by Build. Empty for pure server frameworks.
	StaticDir string

	// StaticPrefixes are URL path prefixes that must always be served straight
	// from the asset bundle (immutable assets) and never the server bundle.
	StaticPrefixes []string

	// ServerEntry is the conventional server entry the SSR adapter wraps and
	// compiles to WebAssembly. Empty for static-only frameworks.
	ServerEntry string
}

// IsSSR reports whether the framework defaults to server side rendering.
func (f *Framework) IsSSR() bool {
	return f.Mode == ModeSSR
}

// OutputDir returns the static asset directory produced by the build.
func (f *Framework) OutputDir() string {
	return f.StaticDir
}

// Manifest returns the seed SSR manifest for the framework, with render mode,
// name and static prefixes pre-filled. The adapter completes it (handler,
// routes) at build time.
func (f *Framework) Manifest() *websiteSpec.Manifest {
	m := &websiteSpec.Manifest{
		Framework: f.Name,
		Render:    f.Mode,
		Static:    append([]string(nil), f.StaticPrefixes...),
	}
	if f.IsSSR() {
		// JS server bundles are compiled to wasm by the adapter via Javy, whose
		// I/O is WASI stdio based.
		m.ABI = websiteSpec.ABIWasiStdio
		m.Routes = []websiteSpec.Route{
			{Pattern: "/api/", Type: websiteSpec.RouteAPI},
			{Pattern: "/", Type: websiteSpec.RouteSSR},
		}
	}
	m.SetDefaults()
	return m
}

// Registry is the ordered list of supported frameworks. Order is used only to
// break Priority ties deterministically.
var Registry = []*Framework{
	// ---- SSR meta-frameworks -------------------------------------------------
	{
		Name: "nextjs", Title: "Next.js", Mode: ModeSSR, Priority: 100,
		Dependencies: []string{"next"},
		ConfigFiles:  []string{"next.config.js", "next.config.mjs", "next.config.ts"},
		Image:        "node:20-alpine", Install: "npm ci", Build: "npm run build",
		StaticDir: ".next", StaticPrefixes: []string{"/_next/static/", "/_next/image"},
		ServerEntry: ".next/standalone/server.js",
	},
	{
		Name: "nuxt", Title: "Nuxt", Mode: ModeSSR, Priority: 100,
		Dependencies: []string{"nuxt", "nuxt3", "nuxt-edge"},
		ConfigFiles:  []string{"nuxt.config.js", "nuxt.config.ts"},
		Image:        "node:20-alpine", Install: "npm ci", Build: "npm run build",
		StaticDir: ".output/public", StaticPrefixes: []string{"/_nuxt/"},
		ServerEntry: ".output/server/index.mjs",
	},
	{
		Name: "sveltekit", Title: "SvelteKit", Mode: ModeSSR, Priority: 100,
		Dependencies: []string{"@sveltejs/kit"},
		Image:        "node:20-alpine", Install: "npm ci", Build: "npm run build",
		StaticDir: "build/client", StaticPrefixes: []string{"/_app/"},
		ServerEntry: "build/server/index.js",
	},
	{
		Name: "remix", Title: "Remix", Mode: ModeSSR, Priority: 100,
		Dependencies: []string{"@remix-run/react", "@remix-run/node", "@remix-run/serve"},
		Image:        "node:20-alpine", Install: "npm ci", Build: "npm run build",
		StaticDir: "build/client", StaticPrefixes: []string{"/assets/"},
		ServerEntry: "build/server/index.js",
	},
	{
		Name: "solidstart", Title: "SolidStart", Mode: ModeSSR, Priority: 100,
		Dependencies: []string{"@solidjs/start", "solid-start"},
		Image:        "node:20-alpine", Install: "npm ci", Build: "npm run build",
		StaticDir: ".output/public", StaticPrefixes: []string{"/_build/"},
		ServerEntry: ".output/server/index.mjs",
	},

	// ---- Server / API frameworks --------------------------------------------
	{
		Name: "nestjs", Title: "NestJS", Mode: ModeSSR, Priority: 60,
		Dependencies: []string{"@nestjs/core"},
		Image:        "node:20-alpine", Install: "npm ci", Build: "npm run build",
		ServerEntry: "dist/main.js",
	},
	{
		Name: "express", Title: "Express", Mode: ModeSSR, Priority: 50,
		Dependencies: []string{"express"},
		Image:        "node:20-alpine", Install: "npm ci", Build: "npm run build --if-present",
		ServerEntry: "index.js",
	},
	{
		Name: "fastify", Title: "Fastify", Mode: ModeSSR, Priority: 50,
		Dependencies: []string{"fastify"},
		Image:        "node:20-alpine", Install: "npm ci", Build: "npm run build --if-present",
		ServerEntry: "index.js",
	},
	{
		Name: "koa", Title: "Koa", Mode: ModeSSR, Priority: 50,
		Dependencies: []string{"koa"},
		Image:        "node:20-alpine", Install: "npm ci", Build: "npm run build --if-present",
		ServerEntry: "index.js",
	},
	{
		Name: "hono", Title: "Hono", Mode: ModeSSR, Priority: 50,
		Dependencies: []string{"hono"},
		Image:        "node:20-alpine", Install: "npm ci", Build: "npm run build --if-present",
		ServerEntry: "src/index.js",
	},

	// ---- Static / SSG frameworks --------------------------------------------
	{
		Name: "astro", Title: "Astro", Mode: ModeStatic, Priority: 70,
		Dependencies: []string{"astro"},
		Image:        "node:20-alpine", Install: "npm ci", Build: "npm run build",
		StaticDir: "dist", StaticPrefixes: []string{"/_astro/"},
	},
	{
		Name: "gatsby", Title: "Gatsby", Mode: ModeStatic, Priority: 70,
		Dependencies: []string{"gatsby"},
		Image:        "node:20-alpine", Install: "npm ci", Build: "npm run build",
		StaticDir: "public",
	},
	{
		Name: "vite", Title: "Vite", Mode: ModeStatic, Priority: 50,
		Dependencies: []string{"vite"},
		ConfigFiles:  []string{"vite.config.js", "vite.config.ts"},
		Image:        "node:20-alpine", Install: "npm ci", Build: "npm run build",
		StaticDir: "dist",
	},
	{
		Name: "angular", Title: "Angular", Mode: ModeStatic, Priority: 40,
		Dependencies: []string{"@angular/core"},
		Image:        "node:20-alpine", Install: "npm ci", Build: "npm run build",
		StaticDir: "dist",
	},
	{
		// Keyed primarily on react-scripts (Create React App). "react" /
		// "react-dom" are included as a low priority catch-all so a bare React
		// SPA is still buildable; bundler based setups (Vite, Next, Gatsby, ...)
		// outrank it via Priority.
		Name: "react", Title: "Create React App", Mode: ModeStatic, Priority: 40,
		Dependencies: []string{"react-scripts", "react", "react-dom"},
		Image:        "node:20-alpine", Install: "npm ci", Build: "npm run build",
		StaticDir: "build",
	},
	{
		Name: "vue", Title: "Vue", Mode: ModeStatic, Priority: 40,
		Dependencies: []string{"@vue/cli-service"},
		Image:        "node:20-alpine", Install: "npm ci", Build: "npm run build",
		StaticDir: "dist",
	},
	{
		Name: "preact", Title: "Preact", Mode: ModeStatic, Priority: 20,
		Dependencies: []string{"preact-cli"},
		Image:        "node:20-alpine", Install: "npm ci", Build: "npm run build",
		StaticDir: "build",
	},
	{
		Name: "svelte", Title: "Svelte", Mode: ModeStatic, Priority: 15,
		Dependencies: []string{"svelte"},
		Image:        "node:20-alpine", Install: "npm ci", Build: "npm run build",
		StaticDir: "dist",
	},
	{
		Name: "solid", Title: "Solid", Mode: ModeStatic, Priority: 15,
		Dependencies: []string{"solid-js"},
		Image:        "node:20-alpine", Install: "npm ci", Build: "npm run build",
		StaticDir: "dist",
	},
}

// Get returns the framework with the given canonical name.
func Get(name string) (*Framework, bool) {
	for _, f := range Registry {
		if f.Name == name {
			return f, true
		}
	}
	return nil, false
}

// Names returns the canonical names of every supported framework.
func Names() []string {
	out := make([]string, len(Registry))
	for i, f := range Registry {
		out[i] = f.Name
	}
	return out
}
