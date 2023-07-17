package compile_test

import (
	"os"
	"path"
	"testing"

	"bitbucket.org/taubyte/config-compiler/decompile"
	_ "bitbucket.org/taubyte/config-compiler/fixtures"
	commonDreamland "bitbucket.org/taubyte/dreamland/common"
	dreamland "bitbucket.org/taubyte/dreamland/services"
	_ "bitbucket.org/taubyte/node/service"
	_ "bitbucket.org/taubyte/tns-p2p-client"
	_ "bitbucket.org/taubyte/tns/service"
	commonIface "github.com/taubyte/go-interfaces/common"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/odo/protocols/monkey/fixtures/compile"
)

func TestWasmFunction(t *testing.T) {
	u := dreamland.MultiverseWithConfig(dreamland.UniverseConfig{
		Name: "MonkeyFixtureTestWasmFunction",
		Id:   "MonkeyFixtureTestWasmFunction",
	})
	defer u.Stop()

	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns":     {},
			"node":    {Others: map[string]int{"verbose": 1}},
			"hoarder": {},
		},
		Simples: map[string]commonDreamland.SimpleConfig{
			"client": {
				Clients: commonDreamland.SimpleConfigClients{
					TNS: &commonIface.ClientConfig{},
				},
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	project, err := decompile.MockBuild(testProjectId, "",
		&structureSpec.Function{
			Id:      testFunctionId,
			Name:    "someFunc",
			Type:    "http",
			Call:    "doStuff",
			Memory:  100000,
			Source:  ".",
			Timeout: 1000000000,
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

	err = u.RunFixture("injectProject", project)
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
		Paths:      []string{path.Join(wd, "assets", "release.wasm")},
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

	if string(body) != "Hello, world!" {
		t.Error("Expected Hello, world! got", string(body))
		return
	}
}
