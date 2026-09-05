//go:build dreaming

package dream_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/node"
	nodeDream "github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/project"
	pkgregister "github.com/taubyte/tau/pkg/register"
)

// cron_test is a schedule firing with nobody watching the clock but the cloud
// itself, across two nodes and one raft cluster.
//
// WHAT ONLY A UNIVERSE CAN SHOW is that TWO nodes holding one deployment fire
// it ONCE. Every node runs the same loop and reads the same expression; what
// makes it once is a replicated write, and nothing short of real raft between
// real peers proves that the write is the thing deciding.

const scheduledID = "QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zn"

// withSchedule is a project whose one cron fires its one function every minute.
//
// EVERY MINUTE, because the first sight of a schedule records where the clock
// is and fires nothing — so a test has to wait for a real slot to come round,
// and a minute is the finest grain a cron expression has.
func withSchedule() []byte {
	doc := map[string]any{
		"object": map[string]any{
			"id":   cacheProjectID,
			"name": "shop",
			"function": map[string]any{
				cacheFuncID: map[string]any{
					"name":      "cacher",
					"execution": map[string]any{"call": "handle", "memory": 33554432},
					"source":    map[string]any{"kind": "component", "component": "QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVv"},
				},
			},
			"cron": map[string]any{
				scheduledID: map[string]any{
					"name":     "everyminute",
					"schedule": "* * * * *",
					"timezone": "UTC",
					"function": map[string]any{"cacher": map[string]any{"ops": []any{"invoke"}}},
				},
			},
		},
		"indexes": map[string]any{
			"projects/" + cacheProjectID + "/function/cacher/links": []any{
				"branches/main/projects/" + cacheProjectID + "/function/" + cacheFuncID},
			"projects/" + cacheProjectID + "/cron/everyminute/links": []any{
				"branches/main/projects/" + cacheProjectID + "/cron/" + scheduledID},
		},
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return raw
}

// TestTwoNodesReadOneScheduleAndFireItOnce.
func TestAScheduleFiresOnceAcrossAFleet(t *testing.T) {
	fired := &firing{name: "wasm"}
	defer nodeDream.Runtimes(nodeDream.Runtimes(fired)...)

	m, err := dream.New(t.Context())
	require.NoError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	require.NoError(t, err)
	require.NoError(t, u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			nodeDream.Protocol: {Others: map[string]int{"copies": 2}},
		},
	}))

	pids, err := u.GetServicePids(nodeDream.Protocol)
	require.NoError(t, err)
	require.Len(t, pids, 2)

	first, second := nodeOf(t, pids[0]), nodeOf(t, pids[1])

	_, err = first.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: withSchedule(),
	})
	require.NoError(t, err)

	// BOTH NODES HAVE TO HOLD IT, which is what makes this a test of the claim
	// rather than of which node happened to have the project. A node drains and
	// fires only what it is already serving.
	target := func(n *node.Node) bool {
		_, err := n.Worker().Resolve(t.Context(), scheduleTarget())
		return err == nil
	}
	require.Eventually(t, func() bool { return target(first) && target(second) },
		60*time.Second, time.Second, "the project never reached both nodes")

	// ONE FIRE, and it may take up to a minute for the clock to come round plus
	// the tick that records where the schedule starts.
	require.Eventually(t, func() bool { return fired.count() > 0 },
		180*time.Second, time.Second, "the schedule never fired")

	req := fired.seen()[0]
	require.Equal(t, "POST", req.Method)
	require.Equal(t, []string{"everyminute"}, req.Headers["X-Taubyte-Cron"])
	require.NotEmpty(t, req.Headers["X-Taubyte-Fired-At"])

	// AND ONLY ONE NODE FIRED IT. A second fire within the same minute would be
	// two nodes agreeing to do the same job, which is exactly what the
	// replicated claim exists to prevent — so this waits out the rest of the
	// slot rather than asserting on the instant.
	slot := req.Headers["X-Taubyte-Fired-At"][0]
	require.Never(t, func() bool { return fired.countOf(slot) > 1 },
		20*time.Second, time.Second, "one slot was fired more than once")
}

func scheduleTarget() project.Address {
	return project.Address{Project: "shop", Environment: "prod", Kind: "function", Name: "cacher"}
}

// firing is a tier that records every request its instances are handed.
type firing struct {
	name string

	mu   sync.Mutex
	reqs []*invoke.Request
}

func (f *firing) Name() string { return f.name }

func (f *firing) Start(context.Context, invoke.Spec) (invoke.Instance, error) {
	return &firingInstance{tier: f}, nil
}

func (f *firing) seen() []*invoke.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*invoke.Request(nil), f.reqs...)
}

func (f *firing) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

// countOf is how many times one slot was fired.
func (f *firing) countOf(slot string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	var n int
	for _, req := range f.reqs {
		if at := req.Headers["X-Taubyte-Fired-At"]; len(at) > 0 && at[0] == slot {
			n++
		}
	}
	return n
}

type firingInstance struct{ tier *firing }

func (i *firingInstance) Serve(_ context.Context, req *invoke.Request) (*invoke.Response, error) {
	i.tier.mu.Lock()
	i.tier.reqs = append(i.tier.reqs, req)
	i.tier.mu.Unlock()
	return &invoke.Response{Status: http.StatusOK}, nil
}

func (i *firingInstance) Close() error { return nil }
