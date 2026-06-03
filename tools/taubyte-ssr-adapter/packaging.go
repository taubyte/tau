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

// buildManifest produces the SSR manifest for a WASI-stdio server bundle (the
// shape Javy emits). Static prefixes are pulled from the framework registry so
// immutable assets are served directly rather than re-rendered.
func buildManifest(framework string) *websiteSpec.Manifest {
	m := &websiteSpec.Manifest{
		Render:    websiteSpec.RenderSSR,
		ABI:       websiteSpec.ABIWasiStdio,
		Framework: framework,
		Handler:   websiteSpec.DefaultHandlerPath,
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
			top := strings.SplitN(rel, "/", 2)[0]
			if siteControlFiles[rel] || top == "cloudflare-tmp" {
				return nil // host artifact, not a servable asset
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			return add(rel, data)
		})
		if err != nil {
			return nil, err
		}
	}

	if err := add(websiteSpec.DefaultHandlerPath, handlerZip); err != nil {
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
