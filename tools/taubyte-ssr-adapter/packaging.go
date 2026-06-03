package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/taubyte/tau/pkg/specs/builders/frameworks"
	wasmSpec "github.com/taubyte/tau/pkg/specs/builders/wasm"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
)

// buildHandlerZip wraps a wasm module as a Taubyte function-format zip (the
// module stored as main.wasm). This is the exact format the website asset's
// handler entry and the dfs backend expect, so the same serving path that runs
// a hand-written handler also runs this JS-engine bundle.
func buildHandlerZip(wasm []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(wasmSpec.WasmFile)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(wasm); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ComponentHandlerPath is where a StarlingMonkey wasi:http component is stored
// in the website asset (raw .wasm, not a zip — the component backend reads the
// bytes directly).
const ComponentHandlerPath = "__taubyte__/handler.component.wasm"

// buildManifest produces the SSR manifest for a WASI-stdio server bundle (the
// shape Javy emits).
func buildManifest(framework string) *websiteSpec.Manifest {
	return buildManifestFor(framework, websiteSpec.ABIWasiStdio, websiteSpec.DefaultHandlerPath)
}

// buildComponentManifest produces the SSR manifest for a Component Model server
// bundle (StarlingMonkey / wasi:http), served by a registered ComponentRuntime.
func buildComponentManifest(framework string) *websiteSpec.Manifest {
	return buildManifestFor(framework, websiteSpec.ABIComponent, ComponentHandlerPath)
}

// buildManifestFor builds the SSR manifest for a given handler ABI + handler
// path. Static prefixes are pulled from the framework registry so immutable
// assets are served directly rather than re-rendered.
func buildManifestFor(framework, abi, handler string) *websiteSpec.Manifest {
	m := &websiteSpec.Manifest{
		Render:    websiteSpec.RenderSSR,
		ABI:       abi,
		Framework: framework,
		Handler:   handler,
		Routes: []websiteSpec.Route{
			{Pattern: "/api/", Type: websiteSpec.RouteAPI},
			{Pattern: "/", Type: websiteSpec.RouteSSR},
		},
	}
	if fw, ok := frameworks.Get(framework); ok {
		m.Static = append([]string(nil), fw.StaticPrefixes...)
	}
	m.SetDefaults()
	return m
}

// siteAssetKeys returns the site-root paths a build-output file should be served
// at: its own path, plus — for a non-index `foo.html` — the clean-URL form
// `foo/index.html`. Taubyte resolves a clean URL `/foo` to `foo/index.html`, not
// the flat `foo.html` that edge adapters (SvelteKit's adapter-cloudflare) emit,
// so both forms are written for prerendered pages to serve under clean URLs.
func siteAssetKeys(rel string) []string {
	keys := []string{rel}
	if strings.HasSuffix(rel, ".html") && path.Base(rel) != "index.html" {
		keys = append(keys, strings.TrimSuffix(rel, ".html")+"/index.html")
	}
	return keys
}

// siteControlFiles are host/adapter artifacts that live in an edge build output
// (e.g. SvelteKit's .svelte-kit/cloudflare) but must not be served as static
// assets: the worker is compiled into the handler, and the rest is edge-platform
// routing metadata Taubyte does not consume.
var siteControlFiles = map[string]bool{
	"_worker.js":     true,
	"_worker.js.map": true,
	"_routes.json":   true,
	"_headers":       true,
	".assetsignore":  true,
}

// siteControlDirs are top-level directories in an edge build output that are not
// servable static assets: build temp dirs, and (next-on-pages) the worker
// emitted as a directory rather than a single file.
var siteControlDirs = map[string]bool{
	"cloudflare-tmp": true, // SvelteKit adapter-cloudflare build temp
	"_worker.js":     true, // next-on-pages emits the worker as _worker.js/index.js
}

// isSiteControlPath reports whether a site-relative (slash separated) path is a
// host/adapter artifact that must be excluded from the served static surface.
func isSiteControlPath(rel string) bool {
	if siteControlFiles[rel] {
		return true
	}
	return siteControlDirs[strings.SplitN(rel, "/", 2)[0]]
}

// buildSiteZip assembles a complete Taubyte website build asset: every static /
// prerendered file under siteDir (minus the host control files above) at the
// site root, plus the SSR server bundle and manifest under __taubyte__/. The
// substrate serves the static files directly and routes everything else to the
// handler — so a prerendered page (e.g. a prerendered "/") is served statically
// while dynamic routes hit the bundle. This is the deployable artifact.
func buildSiteZip(siteDir string, handlerZip []byte, manifest *websiteSpec.Manifest) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	seen := map[string]bool{}
	add := func(name string, data []byte) error {
		name = strings.TrimPrefix(path.Clean("/"+filepath.ToSlash(name)), "/")
		if name == "" || seen[name] {
			return nil
		}
		seen[name] = true
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}

	if siteDir != "" {
		err := filepath.WalkDir(siteDir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, err := filepath.Rel(siteDir, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if isSiteControlPath(rel) {
				return nil // host artifact, not a servable asset
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			for _, k := range siteAssetKeys(rel) {
				if err := add(k, data); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	if err := add(manifest.Handler, handlerZip); err != nil {
		return nil, err
	}
	mdata, err := manifest.Marshal()
	if err != nil {
		return nil, err
	}
	if err := add(websiteSpec.ManifestPath, mdata); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
