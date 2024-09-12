package tests

import (
	"os"
	"path"
	"strings"
	"testing"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	_ "github.com/taubyte/tau/dream/fixtures"
	"github.com/taubyte/tau/pkg/config-compiler/decompile"
	specs "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/pkg/specs/methods"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	_ "github.com/taubyte/tau/services/hoarder"
	_ "github.com/taubyte/tau/services/monkey"
	"github.com/taubyte/tau/services/monkey/fixtures/compile"
	_ "github.com/taubyte/tau/services/patrick"
	_ "github.com/taubyte/tau/services/seer"
	_ "github.com/taubyte/tau/services/substrate"
	_ "github.com/taubyte/tau/services/tns"
	"github.com/taubyte/utils/id"
	"gotest.tools/v3/assert"
)

func TestHoarder(t *testing.T) {
	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer":    {},
			"hoarder": {},
			"tns":     {},
			"patrick": {},
			"monkey":  {},
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
		Branch:     specs.DefaultBranches[0],
	})
	assert.NilError(t, err)

	u.Mesh(u.Monkey().Node(), u.Hoarder().Node())

	simple, err := u.Simple("client")
	assert.NilError(t, err)

	tns, err := simple.TNS()
	assert.NilError(t, err)

	assetKey, err := methods.GetTNSAssetPath(projectId, functionId, specs.DefaultBranches[0])
	assert.NilError(t, err)

	obj, err := tns.Fetch(assetKey)
	assert.NilError(t, err)

	cid, ok := obj.Interface().(string)
	assert.Equal(t, ok, true)

	hoarderC, err := simple.Hoarder()
	assert.NilError(t, err)

	// time.Sleep(5 * time.Minute)

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
