package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
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
	_ "github.com/taubyte/tau/services/auth/dream"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/patrick/dream"
	_ "github.com/taubyte/tau/services/substrate/dream"
	_ "github.com/taubyte/tau/services/tns/dream"

	_ "github.com/taubyte/tau/clients/p2p/hoarder/dream"
	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
)

var (
	testProjectId  = "QmegMKBQmDTU9FUGKdhPFn1ZEtwcNaCA2wmyLW8vJn7wZN"
	testFunctionId = "QmZY4u91d1YALDN2LTbpVtgwW8iT5cK9PE1bHZqX9J51Tv"
	testLibraryId  = "QmP6qBNyoLeMLiwk8uYZ8xoT4CnDspYntcY4oCkpVG1byt"
	testWebsiteId  = "QmcrzjxwbqERscawQcXW4e5jyNBNoxLsUYatn63E8XPQq2"
)

func TestBasicWithLibrary(t *testing.T) {
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

	fs, _, err := tcc.GenerateProject(testProjectId,
		&structureSpec.Library{
			Id:       testLibraryId,
			Name:     "someLibrary",
			Path:     "/",
			Provider: "github",
			RepoID:   "123456",
			RepoName: "test/library",
		},
		&structureSpec.Function{
			Id:      testFunctionId,
			Name:    "someFunc",
			Type:    "http",
			Call:    "ping",
			Memory:  100000,
			Timeout: 1000000000,
			Method:  "GET",
			Source:  "libraries/someLibrary",
			Domains: []string{"someDomain"},
			Paths:   []string{"/ping"},
		},
		&structureSpec.Domain{
			Name: "someDomain",
			Fqdn: "hal.computers.com",
		},
		&structureSpec.Website{
			Id:       testWebsiteId,
			Name:     "someWebsite",
			Domains:  []string{"someDomain"},
			Paths:    []string{"/"},
			Provider: "github",
			RepoID:   "123456",
			RepoName: "test/website",
		},
	)
	assert.NilError(t, err)

	err = u.RunFixture("injectProject", fs)
	if err != nil {
		t.Error(err)
		return
	}

	wd, err := os.Getwd()
	assert.NilError(t, err)

	err = u.RunFixture("compileFor", compile.BasicCompileFor{
		ProjectId:  testProjectId,
		ResourceId: testLibraryId,
		Paths:      []string{path.Join(wd, "_assets", "library")},

		// Uncomment and change directory to use cached build
		// Path: "/tmp/QmP6qBNyoLeMLiwk8uYZ8xoT4CnDspYntcY4oCkpVG1byt-556050950/artifact.zwasm",
	})
	assert.NilError(t, err)

	body, err := callHal(u, "/ping")
	assert.NilError(t, err)

	if string(body) != "PONG" {
		t.Error("Expected PONG got", string(body))
		return
	}

	// TODO: This revisit Website Compile Fixture, Keep commented code
	// err = u.RunFixture("compileFor", compile.BasicCompileFor{
	// 	ProjectId:  testProjectId,
	// 	ResourceId: testWebsiteId,
	// 	Paths:      []string{path.Join(wd, "_assets", "website")},

	// 	// Uncomment and change directory to use cached build
	// 	// Path: "/tmp/QmcrzjxwbqERscawQcXW4e5jyNBNoxLsUYatn63E8XPQq2-3889885873/build.zip",
	// })
	// if err != nil {
	// 	t.Error("here", err)
	// 	return
	// }

	// body, err = callHal(u, "/")
	// if err != nil {
	// 	t.Error(err)
	// 	return
	// }

	// expectedToContain := "<title>Welcome</title>"
	// if !strings.Contains(string(body), expectedToContain)  {
	// 	t.Errorf("Expected %s to be in %s", expectedToContain, string(body))
	// 	return
	// }
}

func callHal(u *dream.Universe, path string) ([]byte, error) {
	if u == nil {
		return nil, errors.New("universe nil")
	}
	nodePort, err := u.GetPortHttp(u.Substrate().Node())
	if err != nil {
		return nil, err
	}

	host := fmt.Sprintf("hal.computers.com:%d", nodePort)
	ret, err := commonTest.CreateHttpClient().Get(fmt.Sprintf("http://%s%s", host, path))
	if err != nil {
		return nil, err
	}
	defer ret.Body.Close()

	return io.ReadAll(ret.Body)
}
