//go:build dreaming && wasmtime_component

package compile_test

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/taubyte/tau/clients/p2p/hoarder/dream"
	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
	_ "github.com/taubyte/tau/pkg/tcc/taubyte/v1/fixtures"
	"github.com/taubyte/tau/services/monkey/fixtures/compile"
	_ "github.com/taubyte/tau/services/substrate/dream"
	"github.com/taubyte/tau/services/substrate/components/http/website/wasmtimehttp"
	_ "github.com/taubyte/tau/services/tns/dream"
	tcc "github.com/taubyte/tau/utils/tcc"
	"gotest.tools/v3/assert"
)

// componentHandlerPath mirrors the adapter's ComponentHandlerPath: a wasi:http
// component is stored raw (not zipped) in the website build asset.
const componentHandlerPath = "__taubyte__/handler.component.wasm"

type componentCase struct {
	name   string // also the artifact filename: assets/component/<name>.wasm
	method string
	path   string
	body   string
	ctype  string
	want   string // substring expected in the served response
}

// componentCases covers every framework the adapter targets. The substrate's
// serving path is framework-agnostic — each framework emits the same wasi:http
// component shape — so one fixture that deploys each build.zip into a real dream
// and serves it through the component runtime proves them all. Build the
// artifacts with the adapter (see assets/component/README.md); any whose .wasm is
// absent is skipped.
var componentCases = []componentCase{
	{name: "node-http", method: "GET", path: "/", want: "node:http on Taubyte"},
	{name: "express", method: "GET", path: "/", want: "Express 5 on Taubyte"},
	{name: "koa", method: "GET", path: "/", want: "Koa on Taubyte"},
	{name: "fastify", method: "GET", path: "/", want: "Fastify on Taubyte"},
	{name: "nestjs", method: "GET", path: "/", want: "NestJS on Taubyte"},
	{name: "apollo", method: "POST", path: "/graphql", body: `{"query":"{ hello }"}`, ctype: "application/json", want: "Hello from Apollo on Taubyte"},
	{name: "bun", method: "GET", path: "/", want: "Bun.serve on Taubyte"},
	{name: "deno", method: "GET", path: "/", want: "Deno.serve on Taubyte"},
	{name: "vue", method: "GET", path: "/products", want: "Vue SSR on Taubyte"},
	{name: "nuxt", method: "GET", path: "/products", want: "Nuxt on Taubyte"},
	{name: "nextjs", method: "GET", path: "/", want: "Next"},
}

// TestWebsiteComponent_Dreaming deploys each framework's StarlingMonkey wasi:http
// component as a Taubyte website into a real dream universe and serves it through
// the substrate's component backend (wasmtimehttp -> `wasmtime serve`), asserting
// the server-rendered response. This is the full deploy path: build.zip -> dream
// -> substrate -> component runtime. Needs `wasmtime` on PATH and the
// -tags "dreaming wasmtime_component" build; artifacts under assets/component/.
func TestWebsiteComponent_Dreaming(t *testing.T) {
	wd, err := os.Getwd()
	assert.NilError(t, err)
	assetsDir := filepath.Join(wd, "assets", "component")

	// The component runtime is process-global; stop its spawned `wasmtime serve`
	// children when the whole test finishes (each case shares the singleton).
	t.Cleanup(wasmtimehttp.ShutdownAll)

	ran := 0
	for _, tc := range componentCases {
		tc := tc
		wasmBytes, err := os.ReadFile(filepath.Join(assetsDir, tc.name+".wasm"))
		if err != nil {
			t.Logf("skip %s: artifact not built under %s", tc.name, assetsDir)
			continue
		}
		ran++
		t.Run(tc.name, func(t *testing.T) { serveComponentWebsite(t, tc, wasmBytes) })
	}
	if ran == 0 {
		t.Skip("no component artifacts under assets/component/ (see README.md)")
	}
}

func serveComponentWebsite(t *testing.T, tc componentCase, wasmBytes []byte) {
	// A component-ABI website build asset: the raw component + an SSR manifest
	// that routes everything to it.
	manifest := &websiteSpec.Manifest{
		Render:    websiteSpec.RenderSSR,
		ABI:       websiteSpec.ABIComponent,
		Framework: tc.name,
		Handler:   componentHandlerPath,
		Routes: []websiteSpec.Route{
			{Pattern: "/api/", Type: websiteSpec.RouteAPI},
			{Pattern: "/", Type: websiteSpec.RouteSSR},
		},
	}
	manifest.SetDefaults()
	manifestJSON, err := manifest.Marshal()
	assert.NilError(t, err)

	buildZip, err := makeZip(map[string][]byte{
		componentHandlerPath:     wasmBytes,
		websiteSpec.ManifestPath: manifestJSON,
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
			"tns": {}, "substrate": {}, "hoarder": {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {Clients: dream.SimpleConfigClients{
				TNS:     &commonIface.ClientConfig{},
				Hoarder: &commonIface.ClientConfig{},
			}.Compat()},
		},
	})
	assert.NilError(t, err)

	// Register the website under hal.computers.com (the fqdn the test client maps
	// to localhost); each case runs in its own universe so the shared name is fine.
	fs, _, err := tcc.GenerateProject(testProjectId,
		&structureSpec.Website{
			Id: testWebsiteId, Name: "componentSite", Domains: []string{"componentDomain"},
			Paths: []string{"/"}, Provider: "github", RepoID: "123456",
			RepoName: "test/" + tc.name, Render: websiteSpec.RenderSSR,
		},
		&structureSpec.Domain{Name: "componentDomain", Fqdn: "hal.computers.com"},
	)
	assert.NilError(t, err)

	assert.NilError(t, u.RunFixture("injectProject", fs))
	assert.NilError(t, u.RunFixture("compileFor", compile.BasicCompileFor{
		ProjectId:  testProjectId,
		ResourceId: testWebsiteId,
		Paths:      []string{buildZipPath},
	}))

	assert.NilError(t, waitForWebsiteInTNS(u, "hal.computers.com", 40, 500*time.Millisecond))

	// Warm with a GET: a read method matches even before the build asset loads,
	// and loading it makes the substrate see the SSR manifest. A POST-first site
	// (Apollo's /graphql) otherwise can't match until that happens.
	_, _, _ = callHalReqRetry(u, "GET", "/", "", "", 30, 500*time.Millisecond)

	body, status, err := callHalReqRetry(u, tc.method, tc.path, tc.body, tc.ctype, 30, 500*time.Millisecond)
	assert.NilError(t, err)
	if !strings.Contains(string(body), tc.want) {
		t.Fatalf("%s: status %d, response missing %q, got: %.300s", tc.name, status, tc.want, body)
	}
	t.Logf("%s: served from dream via the component runtime (status %d) — contains %q", tc.name, status, tc.want)
}

// callHalReq issues one method/path request to hal.computers.com (mapped to the
// substrate node by the test client), optionally with a body+content-type.
func callHalReq(u *dream.Universe, method, path, body, ctype string) ([]byte, int, error) {
	nodePort, err := u.GetPortHttp(u.Substrate().Node())
	if err != nil {
		return nil, 0, err
	}
	url := fmt.Sprintf("http://hal.computers.com:%d%s", nodePort, path)
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		return nil, 0, err
	}
	if ctype != "" {
		req.Header.Set("content-type", ctype)
	}
	resp, err := commonTest.CreateHttpClient().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return b, resp.StatusCode, err
}

func callHalReqRetry(u *dream.Universe, method, path, body, ctype string, maxRetries int, retryDelay time.Duration) ([]byte, int, error) {
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		b, status, err := callHalReq(u, method, path, body, ctype)
		if err == nil {
			return b, status, nil
		}
		lastErr = err
		if !isLookupError(err) {
			return nil, 0, err
		}
		if i < maxRetries-1 {
			time.Sleep(retryDelay)
		}
	}
	return nil, 0, fmt.Errorf("failed after %d retries, last error: %w", maxRetries, lastErr)
}
