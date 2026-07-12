//go:build dreaming

package benchmarks

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"sync"
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	nodeIface "github.com/taubyte/tau/core/services/substrate"
	"github.com/taubyte/tau/dream"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/monkey/fixtures/compile"
	"github.com/taubyte/tau/services/substrate/components/p2p"
	tcc "github.com/taubyte/tau/utils/tcc"

	_ "github.com/taubyte/tau/services/seer/dream"
)

// BenchmarkP2PFunction needs its own universe rather than sharedUniverse's:
// the p2p client (p2p/streams/client) resolves the target peer by
// discovering *other* peers advertising the protocol (it explicitly skips
// its own node — see refreshFromPeerStore in p2p/streams/client/client.go),
// so a lone substrate node can never dispatch a p2p command to itself; every
// send blocks for SendToPeerTimeout (10s) and fails with "i/o timeout"
// (confirmed empirically). services/substrate/components/p2p/test's own
// TestFail_Dreaming uses the same two-node shape for the same reason.
//
// A second substrate copy could instead be added to the shared universe's
// Services config, but dream.Universe.Substrate()/GetPortHttp() etc. pick
// the "first" node out of a map, and Go's map iteration order is randomized
// per range (confirmed empirically) — with two copies, every other
// benchmark that calls sharedUniverse(b) would risk racing between the
// warmed and cold node. A dedicated universe avoids that entirely.
var (
	p2pUOnce sync.Once
	p2pNodeA nodeIface.Service
	p2pNodeB nodeIface.Service
	p2pUErr  error
)

const (
	p2pProtocol    = "/testproto/v1"
	p2pCommand     = "someCommand"
	p2pServiceName = "benchP2PService"
	p2pFuncName    = "benchP2PFunc"
	p2pFunctionId  = "QmZY4u91d1YALDN2LTbpVtgwW8iT5cK9PE1bHZqX9J5789"

	// p2pResponse is the exact payload the prebuilt artifact.zwasm binary
	// returns (confirmed empirically): the checked-in .zwasm predates the
	// current p2p_method.go source, which now writes a lowercase "hello
	// from the other side" but the compiled artifact still returns this
	// capitalized string (matching TestFail_Dreaming's assertion).
	p2pResponse = "Hello from the other side"
)

func p2pUniverse(b *testing.B) (nodeIface.Service, nodeIface.Service) {
	b.Helper()
	p2pUOnce.Do(func() { p2pNodeA, p2pNodeB, p2pUErr = bootP2P() })
	if p2pUErr != nil {
		b.Fatal(p2pUErr)
	}
	return p2pNodeA, p2pNodeB
}

func bootP2P() (nodeIface.Service, nodeIface.Service, error) {
	m, err := dream.New(context.Background())
	if err != nil {
		return nil, nil, err
	}

	u, err := m.New(dream.UniverseConfig{Name: "bench-p2p"})
	if err != nil {
		return nil, nil, err
	}

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns":       {},
			"substrate": {Others: map[string]int{"copies": 2}},
			"hoarder":   {},
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
	if err != nil {
		return nil, nil, err
	}

	// The two substrate copies need to know about each other before the p2p
	// client's peer discovery can find one from the other.
	u.Mesh()

	fs, _, err := tcc.GenerateProject(testProjectId,
		&structureSpec.Service{
			Name:     p2pServiceName,
			Protocol: p2pProtocol,
		},
		&structureSpec.Function{
			Id:       p2pFunctionId,
			Name:     p2pFuncName,
			Type:     "p2p",
			Call:     "methodP2P",
			Command:  p2pCommand,
			Protocol: p2pProtocol,
			Memory:   20 * 1024 * 1024,
			Source:   ".",
			Timeout:  1000000000,
		},
	)
	if err != nil {
		return nil, nil, err
	}

	if err = u.RunFixture("injectProject", fs); err != nil {
		return nil, nil, err
	}

	wd, err := os.Getwd()
	if err != nil {
		return nil, nil, err
	}

	zwasm := path.Join(wd, "..", "..", "services", "substrate", "components", "p2p", "test", "assets", "artifact.zwasm")
	if err = u.RunFixture("compileFor", compile.BasicCompileFor{
		ProjectId:  testProjectId,
		ResourceId: p2pFunctionId,
		Paths:      []string{zwasm},
	}); err != nil {
		return nil, nil, err
	}

	pids, err := u.GetServicePids("substrate")
	if err != nil {
		return nil, nil, err
	}
	if len(pids) != 2 {
		return nil, nil, fmt.Errorf("expected 2 substrate copies, got %d", len(pids))
	}

	nodeA, ok := u.SubstrateByPid(pids[0])
	if !ok {
		return nil, nil, errors.New("substrate node A not found")
	}
	nodeB, ok := u.SubstrateByPid(pids[1])
	if !ok {
		return nil, nil, errors.New("substrate node B not found")
	}

	return nodeA, nodeB, nil
}

// BenchmarkP2PFunction measures the warm p2p-triggered serving path: stream
// command -> peer discovery -> service/function lookup -> cached wasm
// instance call -> response. The stream and command are created once outside
// the timed loop since Command.Send opens a fresh libp2p connection per call
// (see services/substrate/components/p2p/stream/command.go), so repeated
// Send on one Command is safe and representative of the steady-state path.
func BenchmarkP2PFunction(b *testing.B) {
	nodeA, _ := p2pUniverse(b)

	srv, err := p2p.New(nodeA)
	if err != nil {
		b.Fatal(err)
	}

	stream, err := srv.Stream(nodeA.Context(), testProjectId, "", p2pProtocol)
	if err != nil {
		b.Fatal(err)
	}

	cmd, err := stream.Command(p2pCommand)
	if err != nil {
		b.Fatal(err)
	}

	ctx := nodeA.Context()

	// The function config needs to propagate through TNS, node B needs to be
	// discoverable, and the wasm instance needs to warm up before the p2p
	// dispatch resolves - retry until the first Send succeeds.
	var lastErr error
	for i := 0; ; i++ {
		resp, sendErr := cmd.Send(ctx, map[string]interface{}{"data": []byte("Hello, world")})
		if sendErr == nil {
			val, getErr := resp.Get("data")
			if getErr == nil {
				if string(val.([]byte)) == p2pResponse {
					lastErr = nil
					break
				}
				lastErr = fmt.Errorf("unexpected response %q", val)
			} else {
				lastErr = getErr
			}
		} else {
			lastErr = sendErr
		}
		if i >= 60 {
			b.Fatalf("p2p function never came up: %v", lastErr)
		}
		time.Sleep(500 * time.Millisecond)
	}

	b.ReportAllocs()
	for b.Loop() {
		resp, err := cmd.Send(ctx, map[string]interface{}{"data": []byte("Hello, world")})
		if err != nil {
			b.Fatal(err)
		}
		val, err := resp.Get("data")
		if err != nil {
			b.Fatal(err)
		}
		if string(val.([]byte)) != p2pResponse {
			b.Fatalf("expected %q got %q", p2pResponse, val)
		}
	}
}
