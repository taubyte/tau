package compile_test

import (
	"os"
	"path"
	"testing"

	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	_ "github.com/taubyte/tau/pkg/tcc/taubyte/v1/fixtures"
	"github.com/taubyte/tau/services/monkey/fixtures/compile"
	_ "github.com/taubyte/tau/services/substrate/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
	tcc "github.com/taubyte/tau/utils/tcc"
	"gotest.tools/v3/assert"
)

func TestWasmLibrary(t *testing.T) {
	t.Skip("Needs to be redone")
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
					TNS: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

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
			Call:    "ping3",
			Memory:  100000,
			Source:  "libraries/someLibrary",
			Timeout: 1000000000,
			Method:  "GET",
			Domains: []string{"someDomain"},
			Paths:   []string{"/ping3"},
		},
		&structureSpec.Domain{
			Name: "someDomain",
			Fqdn: "hal.computers.com",
		},
	)
	if err != nil {
		t.Error(err)
		return
	}

	err = u.RunFixture("injectProject", fs)
	if err != nil {
		t.Error(err)
		return
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Error(err)
		return
	}

	err = u.RunFixture("compileFor", compile.BasicCompileFor{
		ProjectId:  testProjectId,
		ResourceId: testLibraryId,
		Paths:      []string{path.Join(wd, "assets", "library.zwasm")},
	})
	if err != nil {
		t.Error(err)
		return
	}

	body, err := callHal(u, "/ping3")
	if err != nil {
		t.Error(err)
		return
	}

	if string(body) != "PONG3" {
		t.Error("Expected PONG3 got", string(body))
		return
	}

}
