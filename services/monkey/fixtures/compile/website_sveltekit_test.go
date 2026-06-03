//go:build dreaming

package compile_test

import (
	"fmt"
	"io"
	"os"
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
	_ "github.com/taubyte/tau/services/tns/dream"
	tcc "github.com/taubyte/tau/utils/tcc"
	"gotest.tools/v3/assert"
)

// TestWebsiteSvelteKit_Dreaming hosts a complete SvelteKit build.zip (assembled
// by `taubyte-ssr-adapter --site`, containing prerendered pages + the Javy
// server bundle + manifest) on a real substrate and proves the full split end
// to end: the static layer serves prerendered pages while dynamic routes are
// server-rendered by the wasi-stdio bundle.
//
// The zip is supplied via TAUBYTE_SK_BUILD_ZIP (needs the esbuild+Javy>=5
// toolchain to produce); the test skips when unset so CI without the toolchain
// stays green. Build it with the SvelteKit demo:
//
//	go run ./tools/taubyte-ssr-adapter --mode fetch --node --framework sveltekit \
//	  --entry  .svelte-kit/cloudflare/_worker.js \
//	  --site   .svelte-kit/cloudflare --out /tmp/demo.zip
//	TAUBYTE_SK_BUILD_ZIP=/tmp/demo.zip go test -tags dreaming \
//	  -run TestWebsiteSvelteKit_Dreaming ./services/monkey/fixtures/compile/
func TestWebsiteSvelteKit_Dreaming(t *testing.T) {
	zipPath := os.Getenv("TAUBYTE_SK_BUILD_ZIP")
	if zipPath == "" {
		t.Skip("set TAUBYTE_SK_BUILD_ZIP to a SvelteKit build.zip (see test doc)")
	}
	if _, err := os.Stat(zipPath); err != nil {
		t.Skipf("TAUBYTE_SK_BUILD_ZIP=%s not readable: %v", zipPath, err)
	}

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
		&structureSpec.Domain{Name: "someDomain", Fqdn: "hal.computers.com"},
	)
	assert.NilError(t, err)

	assert.NilError(t, u.RunFixture("injectProject", fs))
	assert.NilError(t, u.RunFixture("compileFor", compile.BasicCompileFor{
		ProjectId:  testProjectId,
		ResourceId: testWebsiteId,
		Paths:      []string{zipPath},
	}))

	assert.NilError(t, waitForWebsiteInTNS(u, "hal.computers.com", 40, 500*time.Millisecond))
	// Warm the serviceable lookup before asserting.
	_, err = callHalWithRetry(u, "/", 30, 500*time.Millisecond)
	assert.NilError(t, err)

	for _, tc := range []struct {
		name, path string
		wantStatus int
		wantBody   string // substring that must appear
		layer      string
	}{
		{"prerendered root", "/", 200, "<title>Home</title>", "static"},
		{"clean-url prerender", "/about", 200, "About this app", "static"},
		{"clean-url nested prerender", "/sverdle/how-to-play", 200, "How to play", "static"},
		{"dynamic SSR", "/sverdle", 200, "keyboard", "ssr-bundle"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body, err := getHal(t, u, tc.path)
			assert.NilError(t, err)
			if status != tc.wantStatus {
				t.Fatalf("%s [%s]: status = %d, want %d\nbody: %.200s", tc.path, tc.layer, status, tc.wantStatus, body)
			}
			if !strings.Contains(strings.ToLower(string(body)), strings.ToLower(tc.wantBody)) {
				t.Fatalf("%s [%s]: body missing %q\ngot: %.300s", tc.path, tc.layer, tc.wantBody, body)
			}
		})
	}
}

// getHal issues a GET to the hosted website and returns status + body, retrying
// only while the serviceable lookup is still warming up.
func getHal(t *testing.T, u *dream.Universe, path string) (int, []byte, error) {
	t.Helper()
	nodePort, err := u.GetPortHttp(u.Substrate().Node())
	if err != nil {
		return 0, nil, err
	}
	host := fmt.Sprintf("hal.computers.com:%d", nodePort)
	url := fmt.Sprintf("http://%s%s", host, path)
	for i := 0; i < 30; i++ {
		ret, err := commonTest.CreateHttpClient().Get(url)
		if err != nil {
			if isLookupError(err) {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			return 0, nil, err
		}
		body, rerr := io.ReadAll(ret.Body)
		ret.Body.Close()
		return ret.StatusCode, body, rerr
	}
	return 0, nil, fmt.Errorf("lookup never resolved for %s", path)
}
