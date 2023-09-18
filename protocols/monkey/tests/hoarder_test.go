package tests

import (
	"os"
	"path"
	"strings"
	"testing"

	"github.com/taubyte/config-compiler/decompile"
	commonIface "github.com/taubyte/go-interfaces/common"
	specs "github.com/taubyte/go-specs/common"
	"github.com/taubyte/go-specs/methods"
	structureSpec "github.com/taubyte/go-specs/structure"
	dreamland "github.com/taubyte/tau/libdream"
	_ "github.com/taubyte/tau/libdream/fixtures"
	_ "github.com/taubyte/tau/protocols/hoarder"
	_ "github.com/taubyte/tau/protocols/monkey"
	"github.com/taubyte/tau/protocols/monkey/fixtures/compile"
	_ "github.com/taubyte/tau/protocols/patrick"
	_ "github.com/taubyte/tau/protocols/seer"
	_ "github.com/taubyte/tau/protocols/substrate"
	_ "github.com/taubyte/tau/protocols/tns"
	"github.com/taubyte/utils/id"
	"gotest.tools/v3/assert"
)

func TestHoarder(t *testing.T) {
	u := dreamland.New(dreamland.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&dreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer":      {},
			"hoarder":   {},
			"tns":       {},
			"substrate": {},
			"patrick":   {},
			"monkey":    {},
		},
		Simples: map[string]dreamland.SimpleConfig{
			"client": {
				Clients: dreamland.SimpleConfigClients{
					TNS:     &commonIface.ClientConfig{},
					Hoarder: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	assert.NilError(t, err)

	projectId := id.Generate()
	functionId := id.Generate()

	fqdn := "hal.computers.com"
	_path := "ping"

	project, err := decompile.MockBuild(projectId, "",
		&structureSpec.Function{
			Id:      functionId,
			Name:    "someFunc",
			Type:    "http",
			Call:    "ping",
			Memory:  100000,
			Timeout: 1000000000,
			Method:  "GET",
			Source:  ".",
			Domains: []string{"someDomain"},
			Paths:   []string{"/" + _path},
		},
		&structureSpec.Domain{
			Name: "someDomain",
			Fqdn: fqdn,
		},
	)
	assert.NilError(t, err)

	err = u.RunFixture("injectProject", project)
	assert.NilError(t, err)

	wd, err := os.Getwd()
	assert.NilError(t, err)

	err = u.RunFixture("compileFor", compile.BasicCompileFor{
		ProjectId:  projectId,
		ResourceId: functionId,
		Paths:      []string{path.Join(wd, "fixtures", "ping.zwasm")},
	})
	assert.NilError(t, err)

	simple, err := u.Simple("client")
	assert.NilError(t, err)

	tns, err := simple.TNS()
	assert.NilError(t, err)

	assetKey, err := methods.GetTNSAssetPath(projectId, functionId, specs.DefaultBranch)
	assert.NilError(t, err)

	obj, err := tns.Fetch(assetKey)
	assert.NilError(t, err)

	cid, ok := obj.Interface().(string)
	assert.Equal(t, ok, true)

	hoarderC, err := simple.Hoarder()
	assert.NilError(t, err)

	list, err := hoarderC.List()
	assert.NilError(t, err)

	var stashed bool
	for _, val := range list {
		if strings.Compare(val, cid) == 0 {
			stashed = true
			break
		}
	}

	assert.Equal(t, stashed, true)
}
