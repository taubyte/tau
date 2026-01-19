package p2p_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	nodeIface "github.com/taubyte/tau/core/services/substrate"
	"github.com/taubyte/tau/dream"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	_ "github.com/taubyte/tau/pkg/tcc/taubyte/v1/fixtures"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	"github.com/taubyte/tau/services/monkey/fixtures/compile"
	_ "github.com/taubyte/tau/services/seer/dream"
	"github.com/taubyte/tau/services/substrate/components/p2p"
	_ "github.com/taubyte/tau/services/substrate/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
	"github.com/taubyte/tau/utils/id"
	tcc "github.com/taubyte/tau/utils/tcc"
	"gotest.tools/v3/assert"
)

type testContext struct {
	ctx         context.Context
	project     string
	application string
}

func (t *testContext) Project() string {
	return t.project
}

func (t *testContext) Application() string {
	return t.application
}

func (t *testContext) Context() context.Context {
	return t.ctx
}

func TestFail(t *testing.T) {
	maxAttempts := 5
	commandsTested := 2

	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	var attempts int
	var successes atomic.Int32
	var mostRecentError error
	for attempts < maxAttempts {
		attempts++
		u, err := m.New(dream.UniverseConfig{Name: t.Name()})
		if err != nil {
			mostRecentError = err
			continue
		}
		err = u.StartWithConfig(&dream.Config{
			Services: map[string]commonIface.ServiceConfig{
				"tns":       {},
				"substrate": {Others: map[string]int{"verbose": 1, "copies": 2}},
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
		if err != nil {
			mostRecentError = err
			return
		}

		pids, err := u.GetServicePids("substrate")
		if err != nil {
			mostRecentError = err
			return
		}

		node1, ok := u.SubstrateByPid(pids[0])
		if !ok {
			mostRecentError = errors.New("Node1 not found")
			return
		}
		node2, ok := u.SubstrateByPid(pids[1])
		if !ok {
			mostRecentError = errors.New("Node2 not found")
			return
		}

		fs, project, err := tcc.GenerateProject(id.Generate(),
			&structureSpec.Service{
				Name:     "someService",
				Protocol: "/testproto/v1",
			},

			&structureSpec.Function{
				Name:     "p2pCalledFunc",
				Source:   ".",
				Type:     "p2p",
				Memory:   100000000,
				Timeout:  1000000000,
				Call:     "methodP2P",
				Command:  "someCommand",
				Protocol: "/testproto/v1",
			},
		)
		if err != nil {
			mostRecentError = err
			return
		}

		err = u.RunFixture("injectProject", fs)
		if err != nil {
			mostRecentError = err
			return
		}

		_, globalFunctions := project.Get().Functions("")
		_func, err := project.Function(globalFunctions[0], "")
		if err != nil {
			mostRecentError = err
			return
		}

		err = u.RunFixture("compileFor", compile.BasicCompileFor{
			ProjectId:  project.Get().Id(),
			ResourceId: _func.Get().Id(),

			// Uncomment to generate in temp directory
			// Path: path.Join(os.Getenv("_TAUREPOS"), "/go-node-p2p/test/assets/p2p_method.go"),

			Paths: []string{path.Join(os.Getenv("_TAUREPOS"), "/go-node-p2p/test/assets/artifact.zwasm")},
		})
		if err != nil {
			mostRecentError = err
			return
		}

		ctx := &testContext{
			ctx:         u.Context(),
			project:     project.Get().Id(),
			application: "",
		}

		var wg sync.WaitGroup

		wg.Add(1)
		go func() {
			fmt.Println("SENDING FROM", node1.Node().ID())
			defer wg.Done()

			err := sendTestCommand(ctx, node1)
			if err != nil {
				mostRecentError = err
				return
			}

			successes.Add(1)
		}()

		wg.Add(1)
		go func() {
			fmt.Println("SENDING FROM", node2.Node().ID())
			defer wg.Done()

			err := sendTestCommand(ctx, node2)
			if err != nil {
				mostRecentError = err
				return
			}

			successes.Add(1)
		}()

		wg.Wait()
		u.Stop()

		// Wait for universe to clean up
		<-time.After(5 * time.Second)
	}

	if float64(successes.Load())/float64(maxAttempts*commandsTested) <= .5 {
		t.Error(mostRecentError)
		return
	}
}

func sendTestCommand(ctx *testContext, node nodeIface.Service) error {
	protocol := "/testproto/v1"
	command := "someCommand"

	srv, err := p2p.New(node)
	if err != nil {
		return fmt.Errorf("creating new P2P node failed with: %w", err)
	}

	stream, err := srv.Stream(ctx.Context(), ctx.Project(), ctx.Application(), protocol)
	if err != nil {
		return fmt.Errorf("Creating stream failed with: %s", err)
	}

	cmd, err := stream.Command(command)
	if err != nil {
		return fmt.Errorf("Command failed with: %s", err)
	}

	data, err := cmd.Send(ctx.Context(), map[string]interface{}{"data": []byte("Hello, world")})
	if err != nil {
		return fmt.Errorf("Sending message failed with: %s", err)
	}

	val, err := data.Get("data")
	if err != nil {
		return fmt.Errorf("Getting data failed with: %s", err)
	}

	if string(val.([]byte)) != "Hello from the other side" {
		return fmt.Errorf("Expected Hello from the other side got %#v", data)
	}

	return nil
}
