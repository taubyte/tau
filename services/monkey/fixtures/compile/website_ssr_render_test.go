//go:build dreaming

package compile_test

import (
	"fmt"
	"testing"
	"time"

	_ "github.com/taubyte/tau/clients/p2p/hoarder/dream"
	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	specCommon "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/pkg/specs/methods"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
	_ "github.com/taubyte/tau/pkg/tcc/taubyte/v1/fixtures"
	_ "github.com/taubyte/tau/services/substrate/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
	tcc "github.com/taubyte/tau/utils/tcc"
	"gotest.tools/v3/assert"
)

// TestWebsiteSSRConfig_Dreaming guards that the server side rendering selectors
// (`render`, `framework`, `entry`) survive the production compile+publish path
// into TNS. The substrate binds this config and matches on `render == "ssr"`
// BEFORE the build asset (and its manifest) load, so without it a freshly
// deployed SSR site rejects non-read methods — e.g. a POST to /api or a GraphQL
// POST — until something warms it, and even then the serviceable cache only
// returns HighMatch entries. The compiler used to drop these attributes (they
// were absent from its website schema), so this is a direct regression test.
//
// It needs no wasm artifact or `wasmtime`: it only deploys and reads back.
func TestWebsiteSSRConfig_Dreaming(t *testing.T) {
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

	fs, _, err := tcc.GenerateProject(testProjectId,
		&structureSpec.Website{
			Id: testWebsiteId, Name: "ssrConfigSite", Domains: []string{"ssrConfigDomain"},
			Paths: []string{"/"}, Provider: "github", RepoID: "123456", RepoName: "test/ssr-config",
			Render: websiteSpec.RenderSSR, Framework: "apollo", Entry: "ssrHandler",
		},
		&structureSpec.Domain{Name: "ssrConfigDomain", Fqdn: "hal.computers.com"},
	)
	assert.NilError(t, err)

	assert.NilError(t, u.RunFixture("injectProject", fs))
	// No build asset needed: we only assert the published structure config.
	assert.NilError(t, waitForWebsiteInTNS(u, "hal.computers.com", 40, 500*time.Millisecond))

	cfg, err := fetchWebsiteConfigFromTNS(u, "hal.computers.com")
	assert.NilError(t, err)

	// The whole point: these came through the compiler into TNS.
	assert.Equal(t, cfg.Render, websiteSpec.RenderSSR, "render must survive compilation so SSR sites match every method")
	assert.Equal(t, cfg.IsSSR(), true)
	assert.Equal(t, cfg.Framework, "apollo")
	assert.Equal(t, cfg.Entry, "ssrHandler")
}

// fetchWebsiteConfigFromTNS reads the website serviceable config back from TNS
// the same way the substrate's lookup does: resolve the host's website link
// index, then bind the current published object to a structureSpec.Website.
func fetchWebsiteConfigFromTNS(u *dream.Universe, fqdn string) (*structureSpec.Website, error) {
	tns := u.Substrate().Tns()

	servKey, err := methods.HttpPath(fqdn, websiteSpec.PathVariable)
	if err != nil {
		return nil, err
	}

	indexObject, err := tns.Fetch(servKey.Versioning().Links())
	if err != nil {
		return nil, err
	}
	pathList, err := indexObject.Current(specCommon.DefaultBranches)
	if err != nil {
		return nil, err
	}

	var last error
	for _, p := range pathList {
		obj, err := tns.Fetch(p)
		if err != nil {
			last = err
			continue
		}
		cfg := &structureSpec.Website{}
		if err := obj.Bind(cfg); err != nil {
			last = err
			continue
		}
		return cfg, nil
	}
	if last == nil {
		last = fmt.Errorf("no website object published for host %q", fqdn)
	}
	return nil, last
}
