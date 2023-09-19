package gateway

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/taubyte/config-compiler/decompile"
	commonIface "github.com/taubyte/go-interfaces/common"
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

func TestGatewayBasic(t *testing.T) {
	u := startDreamland(t)
	defer u.Stop()

	projectId := id.Generate()
	pingId := id.Generate()
	upperId := id.Generate()

	fqdn := "hal.computers.com"
	pingPath := "ping"
	upperPath := "upper"

	project, err := decompile.MockBuild(projectId, "",
		&structureSpec.Function{
			Id:      pingId,
			Name:    id.Generate(),
			Type:    "http",
			Call:    "ping",
			Memory:  100000,
			Timeout: 1000000000,
			Method:  "GET",
			Source:  ".",
			Domains: []string{"someDomain"},
			Paths:   []string{"/" + pingPath},
		},
		&structureSpec.Function{
			Id:      upperId,
			Name:    id.Generate(),
			Type:    "http",
			Call:    "toUpper",
			Memory:  100000000,
			Timeout: 1000000000000,
			Method:  "POST",
			Source:  ".",
			Domains: []string{"someDomain"},
			Paths:   []string{"/" + upperPath},
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
		ResourceId: pingId,
		Paths:      []string{path.Join(wd, "fixtures", "ping.zwasm")},
	})
	assert.NilError(t, err)

	err = u.RunFixture("compileFor", compile.BasicCompileFor{
		ProjectId:  projectId,
		ResourceId: upperId,
		Paths:      []string{path.Join(wd, "fixtures", "toupper.zwasm")},
	})
	assert.NilError(t, err)

	gateway := u.Gateway()
	gateWayHttpPort, err := u.GetPortHttp(gateway.Node())
	assert.NilError(t, err)

	firstSubstrate := u.Substrate()
	substrateHttpPort, err := u.GetPortHttp(firstSubstrate.Node())
	assert.NilError(t, err)

	url := fmt.Sprintf("http://%s:%d/%s", fqdn, substrateHttpPort, pingPath)
	res, err := http.DefaultClient.Get(url)
	assert.NilError(t, err)

	data, err := io.ReadAll(res.Body)
	assert.NilError(t, err)
	assert.Equal(t, "PONG", string(data))

	body := "hello_world"
	url = fmt.Sprintf("http://%s:%d/%s", fqdn, substrateHttpPort, upperPath)
	buf := bytes.NewBuffer([]byte(body))

	res, err = http.DefaultClient.Post(url, "text/plain", buf)
	assert.NilError(t, err)

	data, err = io.ReadAll(res.Body)
	assert.NilError(t, err)
	assert.Equal(t, strings.ToUpper(body), string(data))

	url = fmt.Sprintf("http://%s:%d/%s", fqdn, gateWayHttpPort, pingPath)
	res, err = http.DefaultClient.Get(url)
	assert.NilError(t, err)

	data, err = io.ReadAll(res.Body)
	assert.NilError(t, err)

	assert.Equal(t, res.StatusCode, 200, "Gateway Response:", string(data))
	assert.Equal(t, "PONG", string(data))
}

func startDreamland(t *testing.T) *dreamland.Universe {
	u := dreamland.New(dreamland.UniverseConfig{Name: t.Name()})
	err := u.StartWithConfig(&dreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer":      {},
			"hoarder":   {},
			"tns":       {},
			"substrate": {Others: map[string]int{"copies": 4}},
			"patrick":   {},
			"monkey":    {},
			"gateway":   {},
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

	return u
}
