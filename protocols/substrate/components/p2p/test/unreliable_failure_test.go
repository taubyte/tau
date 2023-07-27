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

	"github.com/taubyte/config-compiler/decompile"
	_ "github.com/taubyte/config-compiler/fixtures"
	commonDreamland "github.com/taubyte/dreamland/core/common"
	dreamland "github.com/taubyte/dreamland/core/services"
	commonIface "github.com/taubyte/go-interfaces/common"
	nodeIface "github.com/taubyte/go-interfaces/services/substrate"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/p2p/streams/client"
	_ "github.com/taubyte/tau/protocols/hoarder"
	"github.com/taubyte/tau/protocols/monkey/fixtures/compile"
	_ "github.com/taubyte/tau/protocols/seer"
	_ "github.com/taubyte/tau/protocols/substrate"
	"github.com/taubyte/tau/protocols/substrate/components/p2p"
	_ "github.com/taubyte/tau/protocols/tns"
	"github.com/taubyte/utils/id"
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

// This test is unreliable, if you cannot get it to pass, close everything and run this in the terminal
func TestFail(t *testing.T) {
	t.Skip("this is a really bad test")
	client.SendTimeout = time.Second * 10
	client.SendToPeerTimeout = time.Second * 20
	client.RecvTimeout = time.Second * 10
	client.EstablishStreamTimeout = time.Second * 10
	maxAttempts := 5
	commandsTested := 2

	var attempts int
	var successes atomic.Int32
	var mostRecentError error
	for attempts < maxAttempts {
		attempts++
		u := dreamland.Multiverse("TestFail")
		err := u.StartWithConfig(&commonDreamland.Config{
			Services: map[string]commonIface.ServiceConfig{
				"tns":       {},
				"substrate": {Others: map[string]int{"verbose": 1, "copies": 2}},
				"hoarder":   {},
			},
			Simples: map[string]commonDreamland.SimpleConfig{
				"client": {
					Clients: commonDreamland.SimpleConfigClients{
						TNS: &commonIface.ClientConfig{},
					},
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
		if ok == false {
			mostRecentError = errors.New("Node1 not found")
			return
		}
		node2, ok := u.SubstrateByPid(pids[1])
		if ok == false {
			mostRecentError = errors.New("Node2 not found")
			return
		}

		project, err := decompile.MockBuild(id.Generate(), "/tmp/TestFail_config",
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

		err = u.RunFixture("injectProject", project)
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
