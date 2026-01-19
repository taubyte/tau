package tests

import (
	"fmt"
	"os"
	"path"
	"strconv"
	"testing"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/utils/id"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/hoarder/dream"
	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
	_ "github.com/taubyte/tau/dream/fixtures"
	_ "github.com/taubyte/tau/pkg/tcc/taubyte/v1/fixtures"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	"github.com/taubyte/tau/services/monkey/fixtures/compile"
	_ "github.com/taubyte/tau/services/seer/dream"
	_ "github.com/taubyte/tau/services/substrate/dream"
	mockCounter "github.com/taubyte/tau/services/substrate/mocks/counters"
	_ "github.com/taubyte/tau/services/tns/dream"
	tcc "github.com/taubyte/tau/utils/tcc"
)

var (
	projectId  string
	functionId string

	fqdn         = "hal.computers.com" // make sure to add this to /etc/hosts
	functionPath = "ping"

	iterations = 300
)

func init() {
	projectId = id.Generate()
	functionId = id.Generate()
	if len(os.Args) > 0 {
		iterationArg := os.Args[len(os.Args)-1]
		_iterations, err := strconv.Atoi(iterationArg)
		if err == nil {
			iterations = _iterations
		}
	}
}

func TestCounters(t *testing.T) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns":       {},
			"substrate": {},
			"hoarder":   {},
			"seer":      {Others: map[string]int{"copies": 2}},
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

	fs, _, err := tcc.GenerateProject(projectId,
		&structureSpec.Function{
			Id:      functionId,
			Name:    "testFunc",
			Type:    "http",
			Call:    "ping",
			Memory:  1000000000,
			Timeout: 10000000000,
			Source:  ".",
			Method:  "GET",
			Domains: []string{"testDomain"},
			Paths:   []string{"/" + functionPath},
		},
		&structureSpec.Domain{
			Name: "testDomain",
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
		ResourceId: functionId,
		Paths:      []string{path.Join(wd, "fixtures", "ping.go")},
	})
	assert.NilError(t, err)

	httpPort, err := u.GetPortHttp(u.Substrate().Node())
	assert.NilError(t, err)

	url := fmt.Sprintf("http://%s:%d/%s", fqdn, httpPort, functionPath)
	err = ParallelGetWithBodyCheck(iterations, GetTester{
		Url: url,
		PassingResponse: &ResponseCheck{
			Body: []byte("PONG"),
			Code: 200,
		},
	})
	assert.NilError(t, err)

	counter, err := mockCounter.FromDream(u)
	assert.NilError(t, err)

	metrics := counter.Dump()
	report := metrics.Report(projectId, functionId)
	if report.Failure.Count > 0 {
		t.Error("expected 0 function calls to fail")
		return
	}

	fmt.Printf("Ran ping function %d times with an average execution time of %s\n", iterations, report.Success.Execution.Average())
}
