package node

import (
	"testing"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/js"
	"github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/services/worker"
)

func TestAJSFunctionRunsWhereTheAssetStoreIs(t *testing.T) {
	greet := invoke.Spec{Name: "shop/prod/function/greet", Runtime: js.Name, Artifact: "bafyarchive", Revision: "r1"}
	req := &invoke.Request{Method: "GET", Path: "/"}

	assets := &countingAssets{}
	with, _, _ := makeNode(t, Config{Services: []string{Discovery, Worker}}, WithAssets(assets))
	require.Contains(t, tierNames(with), js.Name)
	_, err := with.pool.Do(t.Context(), greet, req)
	require.NotErrorIs(t, err, invoke.ErrNoRuntime)
	require.ErrorContains(t, err, "nothing stored", "the js tier asked this node's asset store for the archive")
	require.Equal(t, 1, assets.gets)

	without, _, _ := makeNode(t, Config{Services: []string{Discovery, Worker}})
	require.NotContains(t, tierNames(without), js.Name)
	_, err = without.pool.Do(t.Context(), greet, req)
	require.ErrorIs(t, err, invoke.ErrNoRuntime)
	require.ErrorContains(t, err, `"warp"`)
}

func TestTheWorkersEnginesAreTheOperators(t *testing.T) {
	modes := func(n *Node) (js.Mode, wasm.Mode) {
		var script js.Mode
		var component wasm.Mode
		for _, rt := range n.tiers() {
			switch tier := rt.(type) {
			case *js.Runtime:
				script = tier.Mode()
			case *wasm.Runtime:
				component = tier.Mode()
			}
		}
		return script, component
	}

	chosen, _, _ := makeNode(t, Config{
		Services: []string{Discovery, Worker},
		Worker:   worker.Config{Engines: worker.Engines{JS: string(js.ModeBytecode), Wasm: string(wasm.ModeInterpreter)}},
	}, WithAssets(&countingAssets{}))
	script, component := modes(chosen)
	require.Equal(t, js.ModeBytecode, script)
	require.Equal(t, wasm.ModeInterpreter, component)

	unset, _, _ := makeNode(t, Config{Services: []string{Discovery, Worker}}, WithAssets(&countingAssets{}))
	script, component = modes(unset)
	require.Equal(t, js.DefaultMode(), script)
	require.Equal(t, wasm.ModeCompiler, component)

	for engines, want := range map[worker.Engines]error{
		{JS: "turbo"}: js.ErrUnknownMode,
		{Wasm: "jit"}: wasm.ErrUnknownMode,
	} {
		mn := mocknet.New()
		t.Cleanup(func() { mn.Close() })
		h := newHost(t, mn)
		_, err := New(Config{Services: []string{Discovery, Worker}, Worker: worker.Config{Engines: engines}},
			WithHost(h), WithPubSub(newPubSub(t, h)), WithDatabases(&memFactory{}),
			WithRaft(func(string) (raft.Cluster, error) { return raft.NewMockCluster(), nil }),
			WithAssets(&countingAssets{}))
		require.ErrorIs(t, err, want)
	}
}

func TestTheEnginesAreWrittenUnderTheWorker(t *testing.T) {
	var c Config
	require.NoError(t, yaml.Unmarshal([]byte("worker:\n  maxprojects: 8\n  engines:\n    js: native\n    wasm: interpreter\n"), &c))
	require.Equal(t, 8, c.Worker.MaxProjects)
	require.Equal(t, worker.Engines{JS: "native", Wasm: "interpreter"}, c.Worker.Engines)

	out, err := yaml.Marshal(c)
	require.NoError(t, err)
	var back Config
	require.NoError(t, yaml.Unmarshal(out, &back))
	require.Equal(t, c.Worker.Engines, back.Worker.Engines)
}
