package compile_test

import (
	"os"
	"path"
	"strings"
	"testing"

	"github.com/taubyte/config-compiler/decompile"
	_ "github.com/taubyte/config-compiler/fixtures"
	commonDreamland "github.com/taubyte/dreamland/core/common"
	dreamland "github.com/taubyte/dreamland/core/services"
	commonIface "github.com/taubyte/go-interfaces/common"
	structureSpec "github.com/taubyte/go-specs/structure"
	_ "github.com/taubyte/odo/clients/p2p/tns"
	"github.com/taubyte/odo/protocols/monkey/fixtures/compile"
	_ "github.com/taubyte/odo/protocols/node/service"
	_ "github.com/taubyte/odo/protocols/tns/service"
	"gotest.tools/assert"
)

// TODO: FIXME
func TestGoSmartOp(t *testing.T) {
	t.Skip("smart op is broken currently")
	u := dreamland.MultiverseWithConfig(dreamland.UniverseConfig{
		Name: "MonkeyFixtureTestSmartOp",
		Id:   "MonkeyFixtureTestSmartOp",
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
	assert.NilError(t, err)

	project, err := decompile.MockBuild(testProjectId, "",
		&structureSpec.SmartOp{
			Id:      testSmartOpId,
			Name:    "someSmart",
			Call:    "confirmHttp",
			Memory:  100000,
			Source:  ".",
			Timeout: 1000000000,
		},
		&structureSpec.Function{
			Id:      testFunctionId,
			Tags:    []string{"smartops:someSmart"},
			Name:    "someFunc",
			Type:    "http",
			Call:    "doStuff",
			Memory:  100000,
			Source:  ".",
			Timeout: 1000000000,
			Method:  "GET",
			Domains: []string{"someDomain"},
			Paths:   []string{"/pingSuccess"},
		},
		&structureSpec.Function{
			Id:      testFunction2Id,
			Tags:    []string{"smartops:someSmart"},
			Name:    "someFunc2",
			Type:    "http",
			Call:    "doStuff",
			Memory:  100000,
			Source:  ".",
			Timeout: 1000000000,
			Method:  "GET",
			Domains: []string{"someDomain"},
			Paths:   []string{"/pingFail"},
		},
		&structureSpec.Domain{
			Name: "someDomain",
			Fqdn: "hal.computers.com",
		},
	)
	assert.NilError(t, err)

	err = u.RunFixture("injectProject", project)
	assert.NilError(t, err)

	wd, err := os.Getwd()
	assert.NilError(t, err)

	err = u.RunFixture("compileFor", compile.BasicCompileFor{
		ProjectId:  testProjectId,
		ResourceId: testSmartOpId,
		Paths:      []string{path.Join(wd, "assets", "confirmHttp.go")},
	})
	assert.NilError(t, err)

	err = u.RunFixture("compileFor", compile.BasicCompileFor{
		ProjectId:  testProjectId,
		ResourceId: testFunctionId,
		Paths:      []string{path.Join(wd, "assets", "release.wasm")},
	})
	assert.NilError(t, err)

	err = u.RunFixture("compileFor", compile.BasicCompileFor{
		ProjectId:  testProjectId,
		ResourceId: testFunction2Id,
		Paths:      []string{path.Join(wd, "assets", "release.wasm")},
	})
	assert.NilError(t, err)

	body, err := callHal(u, "/pingSuccess")
	assert.NilError(t, err)

	if string(body) != "Hello, world!" {
		t.Error("Expected PONG2 got", string(body))
		return
	}

	body, err = callHal(u, "/pingFail")
	assert.NilError(t, err)

	if strings.Contains(string(body), "If you can see this text, it was not blocked by any filter!") == false {
		t.Error("Expected PONG2 got", string(body))
		return
	}
}
