package instance

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/js"
	"github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/services/worker"
)

func TestAJSFunctionRunsOnEveryInstance(t *testing.T) {
	greet := invoke.Spec{Name: "shop/prod/function/greet", Runtime: js.Name, Artifact: "bafyarchive", Revision: "r1"}

	n, _, _ := makeNode(t, Config{Services: []string{Stats, Worker}})
	require.Contains(t, tierNames(n), js.Name)

	// The archive is asked of this instance's own blocks, which have nothing under
	// that id: refused by the tier, not for want of one.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	_, err := n.pool.Do(ctx, greet, &invoke.Request{Method: "GET", Path: "/"})
	require.Error(t, err)
	require.NotErrorIs(t, err, invoke.ErrNoRuntime)
}

func TestTheWorkersEnginesAreTheOperators(t *testing.T) {
	modes := func(n *Instance) (js.Mode, wasm.Mode) {
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
		Services: []string{Stats, Worker},
		Worker:   worker.Config{Engines: worker.Engines{JS: string(js.ModeBytecode), Wasm: string(wasm.ModeInterpreter)}},
	})
	script, component := modes(chosen)
	require.Equal(t, js.ModeBytecode, script)
	require.Equal(t, wasm.ModeInterpreter, component)

	unset, _, _ := makeNode(t, Config{Services: []string{Stats, Worker}})
	script, component = modes(unset)
	require.Equal(t, js.DefaultMode(), script)
	require.Equal(t, wasm.ModeCompiler, component)

	for engines, want := range map[worker.Engines]error{
		{JS: "turbo"}: js.ErrUnknownMode,
		{Wasm: "jit"}: wasm.ErrUnknownMode,
	} {
		_, err := New(mockPeer(t), rooted(t, Config{Services: []string{Stats, Worker}, Worker: worker.Config{Engines: engines}}), withKVDB(&memFactory{}), withRaft(func(string) (raft.Cluster, error) { return raft.NewMockCluster(), nil }))
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
