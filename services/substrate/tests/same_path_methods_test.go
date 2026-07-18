//go:build dreaming

package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"testing"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/monkey/fixtures/compile"
	tcc "github.com/taubyte/tau/utils/tcc"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/dream/fixtures"
	_ "github.com/taubyte/tau/pkg/tcc/taubyte/v1/fixtures"
	_ "github.com/taubyte/tau/services/accounts/dream"
	_ "github.com/taubyte/tau/services/auth/dream"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/patrick/dream"
	_ "github.com/taubyte/tau/services/substrate/dream"
	_ "github.com/taubyte/tau/services/tns/dream"

	_ "github.com/taubyte/tau/clients/p2p/hoarder/dream"
	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
)

const (
	repro340ProjectId  = "QmQazSMmMztAFkECFpvNjGMJpaYH4CvHTt85GDj1yYgt4a"
	repro340LibraryId  = "QmP6qBNyoLeMLiwk8uYZ8xoT4CnDspYntcY4oCkpVG1byt"
	repro340GetFuncId  = "QmaCRFcRsv3oNaBRD9XR8mFzmHrkTBGGbkugZfezg9La9K"
	repro340PostFuncId = "Qmc3WjpDvCaVY3jWmxranUY7roFhRj66SNqstiRbKxDbU4"
	repro340WebsiteId  = "QmcrzjxwbqERscawQcXW4e5jyNBNoxLsUYatn63E8XPQq2"
)

// TestSamePathDifferentMethods_Dreaming reproduces issue #340: two http functions
// share a path (/api/store) but differ by method (GET vs POST). Both must route
// to their own handler. Before the fix, one method fails to route.
func TestSamePathDifferentMethods_Dreaming(t *testing.T) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"hoarder":   {},
			"tns":       {},
			"substrate": {},
			"auth":      {},
			"patrick":   {},
			"monkey":    {},
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

	fs, _, err := tcc.GenerateProject(repro340ProjectId,
		&structureSpec.Library{
			Id:       repro340LibraryId,
			Name:     "someLibrary",
			Path:     "/",
			Provider: "github",
			RepoID:   "123456",
			RepoName: "test/library",
		},
		&structureSpec.Function{
			Id:      repro340GetFuncId,
			Name:    "getFunc",
			Type:    "http",
			Call:    "getHandler",
			Memory:  100000,
			Timeout: 1000000000,
			Method:  "GET",
			Source:  "libraries/someLibrary",
			Domains: []string{"someDomain"},
			Paths:   []string{"/api/store"},
		},
		&structureSpec.Function{
			Id:      repro340PostFuncId,
			Name:    "postFunc",
			Type:    "http",
			Call:    "postHandler",
			Memory:  100000,
			Timeout: 1000000000,
			Method:  "POST",
			Source:  "libraries/someLibrary",
			Domains: []string{"someDomain"},
			Paths:   []string{"/api/store"},
		},
		&structureSpec.Domain{
			Name: "someDomain",
			Fqdn: "hal.computers.com",
		},
	)
	assert.NilError(t, err)

	assert.NilError(t, u.RunFixture("injectProject", fs))

	wd, err := os.Getwd()
	assert.NilError(t, err)

	err = u.RunFixture("compileFor", compile.BasicCompileFor{
		ProjectId:  repro340ProjectId,
		ResourceId: repro340LibraryId,
		Paths:      []string{path.Join(wd, "_assets", "library")},
	})
	assert.NilError(t, err)

	// Interleave so neither ordering nor a first-populated cache entry can mask
	// a same-path/method mixup (issue #340).
	for _, step := range []struct{ method, want string }{
		{http.MethodGet, "GET-HANDLER"},
		{http.MethodPost, "POST-HANDLER"},
		{http.MethodGet, "GET-HANDLER"},
		{http.MethodPost, "POST-HANDLER"},
	} {
		body, err := callHalMethod(u, step.method, "/api/store")
		assert.NilError(t, err)
		assert.Equal(t, string(body), step.want)
	}
}

// TestSamePathMethodsWithWebsite_Dreaming adds the reporter's "furthermore" case
// to issue #340: a website at "/" coexisting with two same-path functions. The
// functions must still win their exact path/method; only unclaimed paths fall
// through to the website catch-all.
func TestSamePathMethodsWithWebsite_Dreaming(t *testing.T) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"hoarder":   {},
			"tns":       {},
			"substrate": {},
			"auth":      {},
			"patrick":   {},
			"monkey":    {},
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

	fs, _, err := tcc.GenerateProject(repro340ProjectId,
		&structureSpec.Library{
			Id:       repro340LibraryId,
			Name:     "someLibrary",
			Path:     "/",
			Provider: "github",
			RepoID:   "123456",
			RepoName: "test/library",
		},
		&structureSpec.Function{
			Id:      repro340GetFuncId,
			Name:    "getFunc",
			Type:    "http",
			Call:    "getHandler",
			Memory:  100000,
			Timeout: 1000000000,
			Method:  "GET",
			Source:  "libraries/someLibrary",
			Domains: []string{"someDomain"},
			Paths:   []string{"/api/store"},
		},
		&structureSpec.Function{
			Id:      repro340PostFuncId,
			Name:    "postFunc",
			Type:    "http",
			Call:    "postHandler",
			Memory:  100000,
			Timeout: 1000000000,
			Method:  "POST",
			Source:  "libraries/someLibrary",
			Domains: []string{"someDomain"},
			Paths:   []string{"/api/store"},
		},
		&structureSpec.Website{
			Id:       repro340WebsiteId,
			Name:     "someWebsite",
			Domains:  []string{"someDomain"},
			Paths:    []string{"/"},
			Provider: "github",
			RepoID:   "123456",
			RepoName: "test/website",
		},
		&structureSpec.Domain{
			Name: "someDomain",
			Fqdn: "hal.computers.com",
		},
	)
	assert.NilError(t, err)

	assert.NilError(t, u.RunFixture("injectProject", fs))

	wd, err := os.Getwd()
	assert.NilError(t, err)

	assert.NilError(t, u.RunFixture("compileFor", compile.BasicCompileFor{
		ProjectId:  repro340ProjectId,
		ResourceId: repro340LibraryId,
		Paths:      []string{path.Join(wd, "_assets", "library")},
	}))
	assert.NilError(t, u.RunFixture("compileFor", compile.BasicCompileFor{
		ProjectId:  repro340ProjectId,
		ResourceId: repro340WebsiteId,
		Paths:      []string{path.Join(wd, "_assets", "website")},
	}))

	// Functions still win their exact path+method.
	getBody, err := callHalMethod(u, http.MethodGet, "/api/store")
	assert.NilError(t, err)
	assert.Equal(t, string(getBody), "GET-HANDLER")

	postBody, err := callHalMethod(u, http.MethodPost, "/api/store")
	assert.NilError(t, err)
	assert.Equal(t, string(postBody), "POST-HANDLER")

	// An unclaimed path falls through to the website at "/".
	siteBody, err := callHalMethod(u, http.MethodGet, "/somewhere-else")
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(string(siteBody), "Welcome"), "expected website body, got: %s", string(siteBody))
}

func callHalMethod(u *dream.Universe, method, urlPath string) ([]byte, error) {
	nodePort, err := u.GetPortHttp(u.Substrate().Node())
	if err != nil {
		return nil, err
	}

	host := fmt.Sprintf("hal.computers.com:%d", nodePort)
	req, err := http.NewRequest(method, fmt.Sprintf("http://%s%s", host, urlPath), nil)
	if err != nil {
		return nil, err
	}

	ret, err := commonTest.CreateHttpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer ret.Body.Close()

	body, _ := io.ReadAll(ret.Body)
	if ret.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s %s -> status %d: %s", method, urlPath, ret.StatusCode, string(body))
	}

	return body, nil
}
