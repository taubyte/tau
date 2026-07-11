//go:build dreaming

package gateway_test

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path"
	"testing"
	"time"

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

	if method != "GET" && method != "POST" {
		t.Errorf("method `%s` not supported", method)
		t.FailNow()
	}

	// Warm the substrate directly.
	switch method {
	case "GET":
		_, err = httpClient.Get(substrateUrl)
	case "POST":
		_, err = httpClient.Post(substrateUrl, "text/plain", bytes.NewBuffer(body))
	}
	assert.NilError(t, err)

	// The gateway proxies to a substrate it discovers over p2p; right after boot
	// (and under back-to-back sweep load) that discovery can lag the first
	// request, so the proxy header comes back empty. Poll until the gateway has
	// resolved the substrate rather than racing a single shot — a real failure to
	// discover still fails the assertion when the deadline elapses.
	wantPid := firstSubstrate.Node().ID().String()
	deadline := time.Now().Add(30 * time.Second)
	for {
		switch method {
		case "GET":
			res, err = httpClient.Get(gatewayUrl)
		case "POST":
			res, err = httpClient.Post(gatewayUrl, "text/plain", bytes.NewBuffer(body))
		}
		assert.NilError(t, err)
		if res.Header.Get(gateway.ProxyHeader) == wantPid || time.Now().After(deadline) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	assert.Equal(t, res.Header.Get(gateway.ProxyHeader), wantPid)

	return
}
