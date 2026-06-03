//go:build dreaming

package compile_test

import (
	"archive/zip"
	"bytes"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/taubyte/tau/clients/p2p/hoarder/dream"
	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	wasmSpec "github.com/taubyte/tau/pkg/specs/builders/wasm"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
	_ "github.com/taubyte/tau/pkg/tcc/taubyte/v1/fixtures"
	"github.com/taubyte/tau/services/monkey/fixtures/compile"
	_ "github.com/taubyte/tau/services/substrate/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
	tcc "github.com/taubyte/tau/utils/tcc"
	"gotest.tools/v3/assert"
)

const ssrEntry = "ssrHandler"

// TestWebsiteSSR_Dreaming hosts a server-side rendered website end to end and
// asserts that static assets are served from the bundle while dynamic routes
// (pages and /api) are rendered on demand by the WebAssembly server bundle.
//
// It needs a prebuilt server bundle at assets/ssr/main.wasm (see
// assets/ssr/README.md); without it the test skips. Hosting itself needs no
// Docker: the SSR build zip is pushed directly through compileFor's zip path.
func TestWebsiteSSR_Dreaming(t *testing.T) {
	wd, err := os.Getwd()
	assert.NilError(t, err)

	wasmPath := filepath.Join(wd, "assets", "ssr", "main.wasm")
	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Skipf("server bundle not built: %s (see assets/ssr/README.md)", wasmPath)
	}

	// Assemble the website build asset: static index.html + the SSR manifest +
	// the server bundle (a function-format zip containing main.wasm).
	handlerZip, err := makeZip(map[string][]byte{wasmSpec.WasmFile: wasmBytes})
	assert.NilError(t, err)

	manifest := &websiteSpec.Manifest{
		Render:    websiteSpec.RenderSSR,
		Framework: "custom",
		Entry:     ssrEntry,
		Routes: []websiteSpec.Route{
			{Pattern: "/api/", Type: websiteSpec.RouteAPI},
			{Pattern: "/", Type: websiteSpec.RouteSSR},
		},
	}
	manifest.SetDefaults()
	manifestJSON, err := manifest.Marshal()
	assert.NilError(t, err)

	buildZip, err := makeZip(map[string][]byte{
		"index.html":                   []byte("<!doctype html><title>Welcome</title><h1>static home</h1>"),
		websiteSpec.ManifestPath:       manifestJSON,
		websiteSpec.DefaultHandlerPath: handlerZip,
	})
	assert.NilError(t, err)

	buildZipPath := filepath.Join(t.TempDir(), "build.zip")
	assert.NilError(t, os.WriteFile(buildZipPath, buildZip, 0o644))

	// Bring up a universe and deploy the website.
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns":       {},
			"substrate": {},
			"hoarder":   {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					TNS:     &commonIface.ClientConfig{},
					Hoarder: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	assert.NilError(t, err)

	fs, _, err := tcc.GenerateProject(testProjectId,
		&structureSpec.Website{
			Id:       testWebsiteId,
			Name:     "someWebsite",
			Domains:  []string{"someDomain"},
			Paths:    []string{"/"},
			Provider: "github",
			RepoID:   "123456",
			RepoName: "test/website",
			Render:   websiteSpec.RenderSSR,
			Entry:    ssrEntry,
		},
		&structureSpec.Domain{
			Name: "someDomain",
			Fqdn: "hal.computers.com",
		},
	)
	assert.NilError(t, err)

	assert.NilError(t, u.RunFixture("injectProject", fs))
	assert.NilError(t, u.RunFixture("compileFor", compile.BasicCompileFor{
		ProjectId:  testProjectId,
		ResourceId: testWebsiteId,
		Paths:      []string{buildZipPath},
	}))

	// Wait for the website to propagate to TNS (and warm the substrate's TNS
	// p2p connection) before hitting it, avoiding a first-request lookup race.
	assert.NilError(t, waitForWebsiteInTNS(u, "hal.computers.com", 40, 500*time.Millisecond))

	// 1. Static asset served straight from the bundle.
	body, err := callHalWithRetry(u, "/", 20, 500*time.Millisecond)
	assert.NilError(t, err)
	if !strings.Contains(string(body), "Welcome") {
		t.Fatalf("static `/` expected to contain \"Welcome\", got: %s", body)
	}

	// 2. Dynamic page rendered by the server bundle (path-dependent output).
	body, err = callHalWithRetry(u, "/blog/hello", 20, 500*time.Millisecond)
	assert.NilError(t, err)
	if !strings.Contains(string(body), "SSR rendered: /blog/hello") {
		t.Fatalf("dynamic page not server-rendered, got: %s", body)
	}

	// 3. /api route rendered by the server bundle.
	body, err = callHalWithRetry(u, "/api/ping", 20, 500*time.Millisecond)
	assert.NilError(t, err)
	if !strings.Contains(string(body), `"path":"/api/ping"`) {
		t.Fatalf("api route not server-rendered, got: %s", body)
	}
}

// makeZip builds an in-memory zip from name->content entries.
func makeZip(entries map[string][]byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		// zip uses forward slashes; normalise just in case.
		w, err := zw.Create(path.Clean(name))
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(content); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
