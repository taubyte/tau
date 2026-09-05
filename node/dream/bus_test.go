//go:build dreaming

package dream_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/node"
	nodeDream "github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/pkg/invoke"
	pkgregister "github.com/taubyte/tau/pkg/register"
)

// bus_test is the claim a user can see: a browser opens a websocket at a name
// and gets what a workload publishes, with nothing deployed beside it.
//
// EVERY OTHER TEST OF THIS PATH FAKES ITS NEIGHBOUR. The gateway's fakes an
// attachment, the node's fakes a browser, and the guest's fakes the topic. What
// none of them can show is that a REAL browser, on a node that runs no part of
// the workload, is listening to the topic a guest's grant resolved to — and
// that is the whole of the feature.

const (
	busID  = "QmTx4Jo9Ty3ST6h6XPmSYf7FEH9iUsWbF8zGEJsWkQAnVW"
	busFor = "live.example.com"
)

// withBus is a project whose one function publishes to one bus, and whose bus
// answers on a name a browser can reach.
//
// THE HOST IS IN A ZONE THE PROJECT DECLARES, so the fqdn is complete without
// a cloud's own. A generated name would work the same way and would make this
// test compute the project's label, which is a property proven elsewhere.
func withBus(componentCID string) []byte {
	doc := map[string]any{
		"object": map[string]any{
			"id":   cacheProjectID,
			"name": "shop",
			"function": map[string]any{
				cacheFuncID: map[string]any{
					"name":      "cacher",
					"execution": map[string]any{"call": "handle", "memory": 33554432},
					"source":    map[string]any{"kind": "component", "component": componentCID},
					"bus": map[string]any{
						"live": map[string]any{"ops": []any{"publish"}},
					},
				},
			},
			"bus": map[string]any{
				// NO `instances:` BLOCK. One bus, one topic, nothing for a
				// caller to name — which is the shape a gateway can serve
				// today; one that admits instances through a validator is
				// refused by name.
				busID: map[string]any{
					"name": "live",
					"host": map[string]any{
						"kind": "zone", "name": "live",
						"zone": map[string]any{"in": map[string]any{"name": zoneID}},
					},
				},
			},
			"zone": map[string]any{
				zoneID: map[string]any{"name": "example_com", "fqdn": "example.com"},
			},
		},
		"indexes": map[string]any{
			"projects/" + cacheProjectID + "/function/cacher/links": []any{
				"branches/main/projects/" + cacheProjectID + "/function/" + cacheFuncID},
			"projects/" + cacheProjectID + "/bus/live/links": []any{
				"branches/main/projects/" + cacheProjectID + "/bus/" + busID},
			"projects/" + cacheProjectID + "/zone/example_com/links": []any{
				"branches/main/projects/" + cacheProjectID + "/zone/" + zoneID},
		},
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return raw
}

// TestABrowserOnOneNodeGetsWhatAGuestPublishesOnAnother — the whole claim, over
// a real websocket and real gossipsub.
func TestABrowserAttachesAndAGuestReachesIt(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

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

	publishing, edge := nodeOf(t, pids[0]), nodeOf(t, pids[1])

	for _, pid := range pids {
		store, ok := nodeDream.Assets(pid)
		require.True(t, ok)
		_, err := store.Put(t.Context(), bytes.NewReader(cacherWASM(t)))
		require.NoError(t, err)
	}
	store, _ := nodeDream.Assets(pids[0])
	cid, err := store.Put(t.Context(), bytes.NewReader(cacherWASM(t)))
	require.NoError(t, err)

	_, err = publishing.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1,
		Compiled: withBus(cid), Hosts: []string{busFor},
	})
	require.NoError(t, err)

	// THE BROWSER ATTACHES TO THE OTHER NODE, which runs no part of this
	// workload's invocation. A gateway that had to be the node the workload
	// ran on would be a placement decision made by a browser.
	srv := httptest.NewServer(edge.Handler())
	defer srv.Close()

	// The publish has to reach the edge node's register before its gateway can
	// resolve the name at all.
	var conn *websocket.Conn
	require.Eventually(t, func() bool {
		c, res, err := websocket.DefaultDialer.Dial(
			"ws://"+strings.TrimPrefix(srv.URL, "http://")+"/",
			http.Header{"Host": []string{busFor}})
		if err != nil {
			return false
		}
		require.Equal(t, http.StatusSwitchingProtocols, res.StatusCode)
		conn = c
		return true
	}, 60*time.Second, 500*time.Millisecond, "the project never reached the edge node")
	defer conn.Close()

	received := make(chan []byte, 4)
	go func() {
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				return
			}
			received <- message
		}
	}()

	// A gossipsub mesh needs both sides joined before a publish reaches
	// anybody, so the invocation is retried until it does — the same shape the
	// stream test uses, for the same reason.
	require.Eventually(t, func() bool {
		res, err := publishing.Worker().Invoke(t.Context(), target(), &invoke.Request{
			Method: "POST", Path: "/broadcast/live/", Body: []byte("deploy finished"),
		})
		require.NoError(t, err)
		require.Equal(t, 204, res.Status, string(res.Body))

		select {
		case got := <-received:
			require.Equal(t, "deploy finished", string(got))
			return true
		case <-time.After(250 * time.Millisecond):
			return false
		}
	}, 60*time.Second, 500*time.Millisecond, "a guest's message never reached the browser")
}

// TestABusIsNotAnHttpRoute. The name resolves and the request is the wrong one,
// which is a different answer from "nothing serves this" — and the difference
// is whether somebody goes looking for a typo.
func TestAPlainRequestToABusNameIsToldToUpgrade(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	m, err := dream.New(t.Context())
	require.NoError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	require.NoError(t, err)
	require.NoError(t, u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			nodeDream.Protocol: {Others: nodeDream.Services(node.Register, node.Worker, node.Stats, node.Gateway)},
		},
	}))

	pids, err := u.GetServicePids(nodeDream.Protocol)
	require.NoError(t, err)
	require.NotEmpty(t, pids)

	n := nodeOf(t, pids[0])
	store, ok := nodeDream.Assets(pids[0])
	require.True(t, ok)
	cid, err := store.Put(t.Context(), bytes.NewReader(cacherWASM(t)))
	require.NoError(t, err)

	_, err = n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1,
		Compiled: withBus(cid), Hosts: []string{busFor},
	})
	require.NoError(t, err)

	r := httptest.NewRequest(http.MethodGet, "http://"+busFor+"/", nil)
	r.RemoteAddr = "203.0.113.7:51000"
	w := httptest.NewRecorder()
	n.Handler().ServeHTTP(w, r)

	require.Equal(t, http.StatusUpgradeRequired, w.Code)
	require.Contains(t, w.Body.String(), "websocket")
}
