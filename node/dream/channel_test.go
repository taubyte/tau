//go:build dreaming

package dream_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/node"
	nodeDream "github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/project"
	pkgregister "github.com/taubyte/tau/pkg/register"
)

// A CHANNEL IS THE ONE PATH IN THIS VOCABULARY WHERE A WORKLOAD REACHES ANOTHER
// WORKLOAD. Every other test of the dial proves half of it — the ABI against a
// fake dialer, the grant against a fake project, the routing against a tier
// that starts nothing. What none of them can show is that a guest calling
// `dial` ends up inside a SECOND guest and gets its answer back.
//
// So this runs both ends for real: one component, deployed twice under two
// names, where the first is invoked over HTTP and the second is reached only
// because the first dialled it.

const (
	dialerFuncID  = "QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPZ"
	serverFuncID  = "QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYQa"
	channelResID  = "QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioED"
	channelHandle = "shop/prod/" + channelResID
)

func dialerTarget() project.Address {
	return project.Address{Project: "shop", Environment: "prod", Kind: "function", Name: "dialer"}
}

// withChannel is one component under two names and the channel between them.
//
// THE SERVER IS THE SAME BINARY, which is what makes the answer proof rather
// than coincidence: it replies to a channel request by echoing the body behind
// the protocol name, so a body that comes back changed came back THROUGH it.
func withChannel(componentCID string) []byte {
	code := map[string]any{"kind": "component", "component": componentCID}
	doc := map[string]any{
		"object": map[string]any{
			"id":   cacheProjectID,
			"name": "shop",
			"function": map[string]any{
				dialerFuncID: map[string]any{
					"name":      "dialer",
					"execution": map[string]any{"call": "handle", "memory": 33554432},
					"source":    code,
					"channel": map[string]any{
						"reserve": map[string]any{"ops": []any{"dial"}},
					},
				},
				serverFuncID: map[string]any{
					"name":      "server",
					"execution": map[string]any{"call": "handle", "memory": 33554432},
					"source":    code,
				},
			},
			"channel": map[string]any{
				channelResID: map[string]any{
					"name":     "reserve",
					"protocol": "reserve",
					// The compiled shape is flat with `kind` as the
					// discriminator; `serve: {function: {name: server}}` is what
					// an author writes.
					"serve": map[string]any{"kind": "function", "name": "server"},
				},
			},
		},
		"indexes": map[string]any{
			"projects/" + cacheProjectID + "/function/dialer/links": []any{
				"branches/main/projects/" + cacheProjectID + "/function/" + dialerFuncID},
			"projects/" + cacheProjectID + "/function/server/links": []any{
				"branches/main/projects/" + cacheProjectID + "/function/" + serverFuncID},
			"projects/" + cacheProjectID + "/channel/reserve/links": []any{
				"branches/main/projects/" + cacheProjectID + "/channel/" + channelResID},
		},
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return raw
}

// TestAGuestDialsAChannelAndASecondGuestAnswers — the whole kind, end to end.
func TestAGuestDialsAChannelAndASecondGuestAnswers(t *testing.T) {
	// NO INJECTED TIER, for the reason the cache test gives: an injected one
	// would be this test's runtime answering this test's dialer.
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	m, err := dream.New(t.Context())
	require.NoError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	require.NoError(t, err)
	require.NoError(t, u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			nodeDream.Protocol: {Others: nodeDream.Services(node.Register, node.Worker, node.Stats)},
		},
	}))

	pids, err := u.GetServicePids(nodeDream.Protocol)
	require.NoError(t, err)
	require.NotEmpty(t, pids)

	n := nodeOf(t, pids[0])
	store, ok := nodeDream.Assets(pids[0])
	require.True(t, ok, "the node has an asset store")

	cid, err := store.Put(t.Context(), bytes.NewReader(cacherWASM(t)))
	require.NoError(t, err)

	_, err = n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: withChannel(cid),
	})
	require.NoError(t, err)

	// THE GRANT CARRIES THE RESOLVED SERVER, not the name the author wrote —
	// the dialer holds a handle and cannot turn `server` into a workload.
	spec, err := n.Worker().Resolve(t.Context(), dialerTarget())
	require.NoError(t, err)
	require.Len(t, spec.Reaches, 1)
	require.Empty(t, spec.Reaches[0].Refused, "a function answers it and no port is named")
	require.Equal(t, channelHandle, spec.Reaches[0].Handle)
	require.Equal(t, "server", spec.Reaches[0].Settings["serve/name"])

	// AND THE DIAL LANDS INSIDE THE OTHER GUEST. What comes back is the body
	// this test sent, prefixed by the protocol name — which only the far end
	// could have written, and only if it was told which conduit it arrived on.
	res, err := n.Worker().Invoke(t.Context(), dialerTarget(), &invoke.Request{
		Method: "POST", Path: "/dial/reserve", Body: []byte("hello"),
	})
	require.NoError(t, err)
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Equal(t, "reserve:hello", string(res.Body))

	// A channel the config never granted is refused BY NAME, with the dialing
	// instance still alive afterwards.
	res, err = n.Worker().Invoke(t.Context(), dialerTarget(), &invoke.Request{
		Method: "POST", Path: "/dial/somebody-elses", Body: []byte("x"),
	})
	require.NoError(t, err)
	require.Equal(t, "no-such-channel:somebody-elses", string(res.Body))
}
