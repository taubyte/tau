package compile_test

import (
	"os"
	"path"
	"testing"

	_ "github.com/taubyte/tau/clients/p2p/tns"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/pkg/config-compiler/decompile"
	_ "github.com/taubyte/tau/pkg/config-compiler/fixtures"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/monkey/fixtures/compile"
	_ "github.com/taubyte/tau/services/substrate"
	_ "github.com/taubyte/tau/services/tns"
)

// TODO: FIXME
func TestLibrary(t *testing.T) {
	t.Skip("Libraries are handled differently now")
	u := dream.New(dream.UniverseConfig{
		Name: "MonkeyFixtureTestLibrary",
		Id:   "MonkeyFixtureTestLibrary",
	})
	defer u.Stop()

	err := u.StartWithConfig(&dream.Config{
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
