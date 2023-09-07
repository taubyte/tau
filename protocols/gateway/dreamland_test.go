package gateway

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"testing"

	"github.com/taubyte/config-compiler/decompile"
	commonIface "github.com/taubyte/go-interfaces/common"
	structureSpec "github.com/taubyte/go-specs/structure"
	dreamlandCommon "github.com/taubyte/tau/libdream/common"
	_ "github.com/taubyte/tau/libdream/common/fixtures"
	dreamland "github.com/taubyte/tau/libdream/services"
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

func TestGatewayBasic(t *testing.T) {
	u := dreamland.Multiverse(dreamland.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&dreamlandCommon.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer":      {},
			"hoarder":   {},
			"tns":       {},
			"substrate": {Others: map[string]int{"copies": 4}},
			"patrick":   {},
			"monkey":    {},
			"gateway":   {},
		},
		Simples: map[string]dreamlandCommon.SimpleConfig{
			"client": {
				Clients: dreamlandCommon.SimpleConfigClients{
					TNS: &commonIface.ClientConfig{},
				},
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

	gateway := u.Gateway()
	gateWayHttpPort, err := u.GetPortHttp(gateway.Node())
	assert.NilError(t, err)

	firstSubstrate := u.Substrate()
	substrateHttpPort, err := u.GetPortHttp(firstSubstrate.Node())
	assert.NilError(t, err)

	url := fmt.Sprintf("http://%s:%d/%s", fqdn, substrateHttpPort, _path)
	res, err := http.DefaultClient.Get(url)
	assert.NilError(t, err)

	data, err := io.ReadAll(res.Body)
	assert.NilError(t, err)
	assert.Equal(t, "PONG", string(data))

	url = fmt.Sprintf("http://%s:%d/%s", fqdn, gateWayHttpPort, _path)
	res, err = http.DefaultClient.Get(url)
	assert.NilError(t, err)

	data, err = io.ReadAll(res.Body)
	assert.NilError(t, err)

	assert.Equal(t, res.StatusCode, 200, "Gateway Response:", string(data))
	assert.Equal(t, firstSubstrate.Node().ID().String(), string(data))
}
