package main

import (
	"archive/zip"
	"bytes"

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
