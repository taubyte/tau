//go:build dreaming

package compile_test

import (
	"os"
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

// TestWebsiteSSRStdio_Dreaming proves the wasi-stdio handler ABI: the runtime
// feeds the serialized request to the bundle's stdin and reads the response
// from its stdout. The bundle is a plain WASI command (see assets/ssr-stdio),
// so this needs no Javy. makeZip is shared with website_ssr_test.go.
func TestWebsiteSSRStdio_Dreaming(t *testing.T) {
	wd, err := os.Getwd()
	assert.NilError(t, err)

	wasmPath := filepath.Join(wd, "assets", "ssr-stdio", "main.wasm")
	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Skipf("stdio server bundle not built: %s (see assets/ssr-stdio/README.md)", wasmPath)
	}

	handlerZip, err := makeZip(map[string][]byte{wasmSpec.WasmFile: wasmBytes})
	assert.NilError(t, err)

	manifest := &websiteSpec.Manifest{
		Render:    websiteSpec.RenderSSR,
		ABI:       websiteSpec.ABIWasiStdio,
		Framework: "custom-stdio",
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

	// 1. Static asset served from the bundle.
	body, err := callHalWithRetry(u, "/", 30, 500*time.Millisecond)
	assert.NilError(t, err)
	if !strings.Contains(string(body), "Welcome") {
		t.Fatalf("static `/` expected to contain \"Welcome\", got: %s", body)
	}

	// 2. Dynamic page rendered by the stdio bundle (path-dependent output).
	body, err = callHalWithRetry(u, "/blog/hello", 30, 500*time.Millisecond)
	assert.NilError(t, err)
	if !strings.Contains(string(body), "STDIO rendered: /blog/hello") {
		t.Fatalf("dynamic page not server-rendered, got: %s", body)
	}

	// 3. /api route rendered by the stdio bundle.
	body, err = callHalWithRetry(u, "/api/ping", 30, 500*time.Millisecond)
	assert.NilError(t, err)
	if !strings.Contains(string(body), `"path":"/api/ping"`) {
		t.Fatalf("api route not server-rendered, got: %s", body)
	}
}
