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

// TODO: FIXME
func TestLibrary(t *testing.T) {
	t.Skip("Test file doesn't exist")
	u := dreamland.MultiverseWithConfig(dreamland.UniverseConfig{
		Name: "MonkeyFixtureTestLibrary",
		Id:   "MonkeyFixtureTestLibrary",
	})
	defer u.Stop()

	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns":     {},
			"node":    {},
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
		&structureSpec.Library{
			Id:   testLibraryId,
			Name: "someLibrary",
			Path: "/",
		},
		&structureSpec.Function{
			Id:      testFunctionId,
			Name:    "someFunc",
			Type:    "http",
			Call:    "ping1",
			Source:  "libraries/someLibrary",
			Memory:  100000,
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
		ResourceId: testLibraryId,
		Paths:      []string{path.Join(wd, "assets", "library")},
	})
	if err != nil {
		t.Error(err)
		return
	}

	if err = u.RunFixture("compileFor", compile.BasicCompileFor{
		ProjectId:  testProjectId,
		ResourceId: testFunctionId,
		Paths:      []string{path.Join(wd, "assets", "ping_w_library.go")},
	}); err != nil {
		t.Error(err)
		return
	}

	body, err := callHal(u, "/ping")
	if err != nil {
		t.Error(err)
		return
	}

	if string(body) != "PONG1" {
		t.Error("Expected PONG1 got", string(body))
		return
	}

}
