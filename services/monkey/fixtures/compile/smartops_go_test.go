package compile_test

import (
	"os"
	"path"
	"strings"
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
	"gotest.tools/v3/assert"
)

// TODO: FIXME
func TestGoSmartOp(t *testing.T) {
	t.Skip("smart op is broken currently")
	u := dream.New(dream.UniverseConfig{
		Name: "MonkeyFixtureTestSmartOp",
		Id:   "MonkeyFixtureTestSmartOp",
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

	assert.Equal(t, string(body), "Hello, world!")

	body, err = callHal(u, "/pingFail")
	assert.NilError(t, err)

	assert.Assert(t, strings.Contains(string(body), "If you can see this text, it was not blocked by any filter!"))
}
