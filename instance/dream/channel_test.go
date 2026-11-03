//go:build dreaming

package dream_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	nodeDream "github.com/taubyte/tau/instance/dream"
	"github.com/taubyte/tau/pkg/specs"
)

// A channel is the one path where a workload reaches another workload: a guest's
// `dial` lands inside a second guest. Both ends are one component deployed under
// two names, so an echoed body prefixed by the protocol came back through the far
// end. projects/with/channel is compiled.

func dialerTarget() specs.Address {
	return specs.Address{Project: "shop", Environment: "prod", Kind: "function", Name: "dialer"}
}

func TestAGuestDialsAChannelAndASecondGuestAnswers(t *testing.T) {
	// NO INJECTED TIER, for the reason the cache test gives: an injected one
	// would be this test's runtime answering this test's dialer.
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	r := deploy(t, "with/channel/function", "reserve.example.com", cacherWASM(t))

	// The grant carries the resolved server, asked of the compiled project (idOf).
	spec, err := r.node.Worker().Resolve(t.Context(), dialerTarget())
	require.NoError(t, err)
	require.Len(t, spec.Reaches, 1)
	require.Empty(t, spec.Reaches[0].Refused, "a function answers it and no port is named")
	require.Equal(t, "shop/prod/"+r.idOf(t, "channel", "reserve"), spec.Reaches[0].Handle)

	// `serve/name` stays the author's label (ByName), so the far end is located in the
	// running deployment.
	require.Equal(t, "server", spec.Reaches[0].Settings["serve/name"])

	// The dial lands inside the other guest: only it could prefix the protocol name.
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
