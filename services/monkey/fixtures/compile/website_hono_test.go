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

// TestWebsiteHono_Dreaming proves a real Hono app (compiled to wasm via the
// adapter's fetch mode + Web-API polyfill + Javy) renders through the substrate
// — static and dynamic routes side by side.
//
// Build the bundle first (see assets/hono/README.md):
//
//	cd tools/taubyte-ssr-adapter/example && npm i hono && cd -
//	go run ./tools/taubyte-ssr-adapter --mode fetch --framework hono \
//	  --entry ./tools/taubyte-ssr-adapter/example/hono-app.js --out /tmp/h.zip
//	unzip -o /tmp/h.zip main.wasm -d services/monkey/fixtures/compile/assets/hono/
func TestWebsiteHono_Dreaming(t *testing.T) {
	wd, err := os.Getwd()
	assert.NilError(t, err)

	wasmPath := filepath.Join(wd, "assets", "hono", "main.wasm")
	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Skipf("hono server bundle not built: %s (see assets/hono/README.md)", wasmPath)
	}

	handlerZip, err := makeZip(map[string][]byte{wasmSpec.WasmFile: wasmBytes})
	assert.NilError(t, err)

	manifest := &websiteSpec.Manifest{
		Render:    websiteSpec.RenderSSR,
		ABI:       websiteSpec.ABIWasiStdio,
		Framework: "hono",
		Routes: []websiteSpec.Route{
			{Pattern: "/api/", Type: websiteSpec.RouteAPI},
			{Pattern: "/", Type: websiteSpec.RouteSSR},
		},
	}
	manifest.SetDefaults()
	manifestJSON, err := manifest.Marshal()
	assert.NilError(t, err)

	buildZip, err := makeZip(map[string][]byte{
		"robots.txt":                   []byte("User-agent: *\nDisallow:"), // static, served alongside dynamic
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

	assert.NilError(t, waitForWebsiteInTNS(u, "hal.computers.com", 40, 500*time.Millisecond))

	// Dynamic page rendered by Hono.
	body, err := callHalWithRetry(u, "/", 30, 500*time.Millisecond)
	assert.NilError(t, err)
	if !strings.Contains(string(body), "Hello from Hono on Taubyte") {
		t.Fatalf("hono `/` not rendered, got: %s", body)
	}

	// Dynamic /api route rendered by Hono.
	body, err = callHalWithRetry(u, "/api/ping", 30, 500*time.Millisecond)
	assert.NilError(t, err)
	if !strings.Contains(string(body), `"runtime":"javy"`) {
		t.Fatalf("hono `/api/ping` not rendered, got: %s", body)
	}

	// Static file served straight from the bundle, alongside the dynamic routes.
	body, err = callHalWithRetry(u, "/robots.txt", 30, 500*time.Millisecond)
	assert.NilError(t, err)
	if !strings.Contains(string(body), "User-agent") {
		t.Fatalf("static /robots.txt not served, got: %s", body)
	}
}
