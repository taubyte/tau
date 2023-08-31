package tests

import (
	"fmt"
	"os"
	"path"
	"strconv"
	"testing"

	commonIface "github.com/taubyte/go-interfaces/common"
	structureSpec "github.com/taubyte/go-specs/structure"
	commonDreamland "github.com/taubyte/tau/libdream/common"
	dreamland "github.com/taubyte/tau/libdream/services"
	"github.com/taubyte/tau/vm/cache"
	"github.com/taubyte/utils/id"
	"gotest.tools/v3/assert"

	"github.com/taubyte/config-compiler/decompile"
	_ "github.com/taubyte/config-compiler/fixtures"
	_ "github.com/taubyte/tau/libdream/common/fixtures"
	_ "github.com/taubyte/tau/protocols/hoarder"
	"github.com/taubyte/tau/protocols/monkey/fixtures/compile"
	_ "github.com/taubyte/tau/protocols/seer"
	_ "github.com/taubyte/tau/protocols/substrate"
	mockCounter "github.com/taubyte/tau/protocols/substrate/mocks/counters"
	_ "github.com/taubyte/tau/protocols/tns"
)

var (
	projectId  string
	functionId string

	fqdn         = "dreamland.test" // make sure to add this to /etc/hosts
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
	u := dreamland.Multiverse(dreamland.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns":       {},
			"substrate": {},
			"hoarder":   {},
			"seer":      {Others: map[string]int{"copies": 2}},
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

	project, err := decompile.MockBuild(projectId, "",
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

	err = u.RunFixture("injectProject", project)
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
	err = cache.ParallelGetWithBodyCheck(iterations, cache.GetTester{
		Url: url,
		PassingResponse: &cache.ResponseCheck{
			Body: []byte("PONG"),
			Code: 200,
		},
	})
	assert.NilError(t, err)

	counter, err := mockCounter.FromDreamland(u)
	assert.NilError(t, err)

	metrics := counter.Dump()
	report := metrics.Report(projectId, functionId)
	if report.Failure.Count > 0 {
		t.Error("expected 0 function calls to fail")
		return
	}

	fmt.Printf("Ran ping function %d times with an average execution time of %s", iterations, report.Success.Execution.Average())
}
