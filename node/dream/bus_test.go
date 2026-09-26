//go:build dreaming

package dream_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	nodeDream "github.com/taubyte/tau/node/dream"
)

// bus_test is the claim a user can see: a browser opens a websocket at a name
// and gets what a workload publishes, with nothing deployed beside it.
//
// EVERY OTHER TEST OF THIS PATH FAKES ITS NEIGHBOUR. The gateway's fakes an
// attachment, the node's fakes a browser, and the guest's fakes the topic. What
// none of them can show is that a REAL browser, on a node that runs no part of
// the workload, is listening to the topic a guest's grant resolved to — and
// that is the whole of the feature.
//
// AND THE PROJECT IS AUTHORED RATHER THAN TRANSCRIBED, for project_test's
// reason: the object this test used to publish was written by hand, so it was a
// statement of what the compiler was believed to produce and nothing ever
// checked it. It had drifted the same two ways the queue's had — it omitted
// `execution/timeout`, which is Required(), so tcc would have refused the very
// project this test claimed to be running, and it omitted `implements`, which
// the vocabulary fills in as `handler`. The yaml under projects/with/bus is
// compiled here, which is the only way a bus's declaration and a bus's
// behaviour are tested as one thing.

// busFor is the name the bus answers on: the label `live` in the zone the
// project declares. IT IS NOT COMPUTED HERE AND IT IS NOT LOOKED UP — a zone
// the project owns makes the fqdn complete without the cloud's own, which is
// why this project declares one rather than taking a generated name.
const busFor = "live.example.com"

// TestABrowserOnOneNodeGetsWhatAGuestPublishesOnAnother — the whole claim, over
// a real websocket and real gossipsub.
func TestABrowserAttachesAndAGuestReachesIt(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	cloud := deployFleet(t, 2, "with/bus", busFor, cacherWASM(t))
	publishing, edge := cloud[0], cloud[1]

	// THE BROWSER ATTACHES TO THE OTHER NODE, which runs no part of this
	// workload's invocation. A gateway that had to be the node the workload
	// ran on would be a placement decision made by a browser.
	srv := httptest.NewServer(edge.node.Handler())
	defer srv.Close()

	// The publish has to reach the edge node's register before its gateway can
	// resolve the name at all. deployFleet already waits for exactly that, so
	// this is expected to succeed on its first tick; it is a retry because a
	// resolvable project and a routable one are two facts and only the first is
	// what the barrier watched.
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
	//
	// NEITHER NAMED BUDGET PRICES THIS ONE. formingBudget and electionBudget
	// are both the cost of a raft group, and there is no raft anywhere on this
	// path: a bus keeps nothing, orders nothing and elects nobody. What is
	// being waited out is the swarm's own — a gossipsub heartbeat is 1s and a
	// mesh needs a few of them once both ends have subscribed, so a few seconds
	// is the shape of it. The 60s here is the cost of REPORTING a mesh that
	// never formed rather than a bound on one that does, and it is the number
	// this assertion has always carried.
	// THE PUBLISH GOES THROUGH THE GATEWAY TOO, and that is the difference
	// between this project and what it used to prove. The workload used to be
	// invoked straight on the worker, so the gateway was only ever tested on the
	// attachment half; now ONE NAME carries both -- `/broadcast` reaches the
	// function and everything else attaches to the bus -- and the whole chain a
	// user actually has is real: HTTP in, a grant resolved, a topic published,
	// a websocket out, on two different nodes.
	require.Eventually(t, func() bool {
		res := publishing.askCarrying(t, http.MethodPost, "/broadcast/live/", "deploy finished")
		require.Equal(t, 204, res.Code, res.Body.String())

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

	r := deploy(t, "with/bus", busFor, cacherWASM(t))

	got := r.ask(t, http.MethodGet, "/")

	require.Equal(t, http.StatusUpgradeRequired, got.Code)
	require.Contains(t, got.Body.String(), "websocket")
}
