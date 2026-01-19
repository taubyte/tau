package compile_test

import (
	"os"
	"path"
	"testing"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/monkey/fixtures/compile"
	tcc "github.com/taubyte/tau/utils/tcc"
	"gotest.tools/v3/assert"
)

func TestRSFunction(t *testing.T) {
	t.Skip("takes forever...")
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
		&structureSpec.Function{
			Id:      testFunctionId,
			Name:    "someFunc",
			Type:    "http",
			Call:    "do_stuff",
			Memory:  100000,
			Timeout: 1000000000,
			Source:  ".",
			Method:  "GET",
			Domains: []string{"someDomain"},
			Paths:   []string{"/ping"},
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
		ResourceId: testFunctionId,
		Paths:      []string{path.Join(wd, "assets", "lib.rs"), path.Join(wd, "assets", "Cargo.toml")},
	})
	if err != nil {
		t.Error(err)
		return
	}

	body, err := callHal(u, "/ping")
	if err != nil {
		t.Error(err)
		return
	}

	if string(body) != "Hello world" {
		t.Errorf("Expected Hello world , got `%s`", string(body))
		return
	}
}
