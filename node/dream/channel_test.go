//go:build dreaming

package dream_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	nodeDream "github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/pkg/specs"
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
//
// AND THE PROJECT IS AUTHORED RATHER THAN TRANSCRIBED, for project_test's
// reason. The object this test used to publish was written by hand, so it was a
// statement of what the compiler was believed to produce and nothing ever
// checked it — and it had already drifted in the two ways the queue conversion
// found: both functions omitted `execution/timeout`, which is Required(), so
// tcc would have REFUSED the very project this test claimed to be running, and
// neither carried `implements: handler`, which the vocabulary defaults. The
// yaml under projects/with/channel is compiled here, so the declaration and the
// dial are tested as one thing.
//
// THE SERVER IS THE SAME BINARY, which is what makes the answer proof rather
// than coincidence: it replies to a channel request by echoing the body behind
// the protocol name, so a body that comes back changed came back THROUGH it.
// That property survives the conversion for free — project() points every
// resource carrying a `source` at the one component it is handed, and both
// functions carry one.

func dialerTarget() specs.Address {
	return specs.Address{Project: "shop", Environment: "prod", Kind: "function", Name: "dialer"}
}

// TestAGuestDialsAChannelAndASecondGuestAnswers — the whole kind, end to end.
//
// ONE NODE, because what is under test is that a dial lands inside another
// guest and not where placement puts it: a second node would add the choice of
// where the server runs to the things this test could fail on without adding
// anything to what it proves.
//
// THE HOST IS INERT. deploy names one because a register keeps its pointers by
// fqdn, but both requests below are put to the worker directly — the project
// declares no zone and no gateway, exactly as the object it replaces did.
//
// NOTHING HERE RETRIES, AND NOTHING HERE NEEDS TO. Every budget in this package
// prices a raft group forming; a channel opens no group, and the dial is one
// warm invoke reaching a second one through the same worker. The resolve below
// is answered out of the project this node opens on first being asked for it,
// which is the same moment the old hand-published object was read at.
func TestAGuestDialsAChannelAndASecondGuestAnswers(t *testing.T) {
	// NO INJECTED TIER, for the reason the cache test gives: an injected one
	// would be this test's runtime answering this test's dialer.
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	r := deploy(t, "with/channel", "reserve.example.com", cacherWASM(t))

	// THE GRANT CARRIES THE RESOLVED SERVER, not the name the author wrote —
	// the dialer holds a handle and cannot turn `server` into a workload.
	//
	// THE HANDLE IS ASKED OF THE COMPILED PROJECT rather than restated, for
	// idOf's reason: a typed id is a second statement of what the compiler
	// produced, and it is the one thing this project exists to stop.
	spec, err := r.node.Worker().Resolve(t.Context(), dialerTarget())
	require.NoError(t, err)
	require.Len(t, spec.Reaches, 1)
	require.Empty(t, spec.Reaches[0].Refused, "a function answers it and no port is named")
	require.Equal(t, "shop/prod/"+r.idOf(t, "channel", "reserve"), spec.Reaches[0].Handle)

	// AND `serve/name` STAYS THE AUTHOR'S WORD. The ref on it is ByName(), so
	// what the compiler writes into the grant is the label and not an id —
	// which is what lets the far end be located in the deployment that is
	// running rather than in the one that was compiled.
	require.Equal(t, "server", spec.Reaches[0].Settings["serve/name"])

	// AND THE DIAL LANDS INSIDE THE OTHER GUEST. What comes back is the body
	// this test sent, prefixed by the protocol name — which only the far end
	// could have written, and only if it was told which conduit it arrived on.
	res, err := r.onCarrying(t.Context(), dialerTarget(), http.MethodPost, "/dial/reserve", "hello")
	require.NoError(t, err)
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Equal(t, "reserve:hello", string(res.Body))

	// A channel the config never granted is refused BY NAME, with the dialing
	// instance still alive afterwards.
	res, err = r.onCarrying(t.Context(), dialerTarget(), http.MethodPost, "/dial/somebody-elses", "x")
	require.NoError(t, err)
	require.Equal(t, "no-such-channel:somebody-elses", string(res.Body))
}
