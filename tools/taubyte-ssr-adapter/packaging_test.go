package main

import (
	"archive/zip"
	"bytes"
	"testing"

	wasmSpec "github.com/taubyte/tau/pkg/specs/builders/wasm"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
)

func TestBuildHandlerZip(t *testing.T) {
	wasm := []byte("\x00asm\x01\x00\x00\x00fake-module")

	zipBytes, err := buildHandlerZip(wasm)
	if err != nil {
		t.Fatal(err)
	}

	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatal(err)
	}

	f, err := zr.Open(wasmSpec.WasmFile) // must be main.wasm
	if err != nil {
		t.Fatalf("handler zip missing %s: %v", wasmSpec.WasmFile, err)
	}
	defer f.Close()

	var got bytes.Buffer
	if _, err := got.ReadFrom(f); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), wasm) {
		t.Error("wasm bytes not preserved in handler zip")
	}
}

func TestBuildManifest(t *testing.T) {
	data, err := buildManifest("hono").Marshal()
	if err != nil {
		t.Fatal(err)
	}

	// Must round-trip through the spec parser the runtime uses.
	m, err := websiteSpec.ParseManifest(data)
	if err != nil {
		t.Fatalf("adapter manifest rejected by spec: %v", err)
	}

	if !m.IsSSR() {
		t.Error("expected ssr manifest")
	}
	if m.ABIOrDefault() != websiteSpec.ABIWasiStdio {
		t.Errorf("abi = %q, want %q", m.ABIOrDefault(), websiteSpec.ABIWasiStdio)
	}
	if m.Handler != websiteSpec.DefaultHandlerPath {
		t.Errorf("handler = %q", m.Handler)
	}
	if m.Classify("/api/x") != websiteSpec.RouteAPI {
		t.Error("expected /api/x -> api")
	}
}

func TestBuildManifestUnknownFramework(t *testing.T) {
	// An unknown framework must still produce a valid manifest (no static
	// prefixes, but otherwise well-formed).
	if _, err := buildManifest("totally-unknown").Marshal(); err != nil {
		t.Fatal(err)
	}
}
