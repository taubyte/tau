package gateway_test

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path"
	"testing"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	gateway "github.com/taubyte/tau/services/gateway"
	"github.com/taubyte/tau/services/monkey/fixtures/compile"
	"github.com/taubyte/tau/utils/id"
	tcc "github.com/taubyte/tau/utils/tcc"
	"gotest.tools/v3/assert"
)

var (
	projectId  = id.Generate()
	resourceId = id.Generate()

	fqdn        = "hal.computers.com"
	requestPath = "ping"
)

func testSingleFunction(t *testing.T, call, method, fileName string, body []byte) (res *http.Response) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer":      {},
			"hoarder":   {},
			"tns":       {},
			"substrate": {}, //Others: map[string]int{"copies": 1}},
			"patrick":   {},
			"monkey":    {},
			"gateway":   {},
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

	fs, _, err := tcc.GenerateProject(
		projectId,
		&structureSpec.Function{
			Id:      resourceId,
			Name:    id.Generate(),
			Type:    "http",
			Call:    call,
			Memory:  100000,
			Timeout: 1000000000,
			Method:  method,
			Source:  ".",
			Domains: []string{"someDomain"},
			Paths:   []string{"/" + requestPath},
		},
		&structureSpec.Domain{
			Name: "someDomain",
			Fqdn: fqdn,
		},
	)
	assert.NilError(t, err)

	err = u.RunFixture("injectProject", fs)
	assert.NilError(t, err)

	wd, err := os.Getwd()
	assert.NilError(t, err)

	err = u.RunFixture("compileFor", compile.BasicCompileFor{
		ProjectId:  projectId,
		ResourceId: resourceId,
		Paths:      []string{path.Join(wd, "fixtures", fileName)},
	})
	assert.NilError(t, err)

	gatewayPort, err := u.GetPortHttp(u.Gateway().Node())
	assert.NilError(t, err)

	firstSubstrate := u.Substrate()
	substratePort, err := u.GetPortHttp(firstSubstrate.Node())
	assert.NilError(t, err)

	httpClient := commonTest.CreateHttpClient()

	substrateUrl := fmt.Sprintf("http://%s:%d/%s", fqdn, substratePort, requestPath)
	gatewayUrl := fmt.Sprintf("http://%s:%d/%s", fqdn, gatewayPort, requestPath)

	switch method {
	case "GET":
		_, err = httpClient.Get(substrateUrl)
		assert.NilError(t, err)

		res, err = httpClient.Get(gatewayUrl)
		assert.NilError(t, err)

	case "POST":
		buffer := bytes.NewBuffer(body)
		_, err = httpClient.Post(substrateUrl, "text/plain", buffer)
		assert.NilError(t, err)

		buffer = bytes.NewBuffer(body)
		res, err = httpClient.Post(gatewayUrl, "text/plain", buffer)
		assert.NilError(t, err)
	default:
		t.Errorf("method `%s` not supported", method)
		t.FailNow()
	}

	proxyPid := res.Header.Get(gateway.ProxyHeader)
	assert.Equal(t, proxyPid, firstSubstrate.Node().ID().String())

	return
}
