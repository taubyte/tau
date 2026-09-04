package build

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/gen/tau/build/v1/buildv1connect"
)

// The protocol id belongs to the proto PACKAGE, not to either service block
// in it, so both generated names have to derive the same string. If they ever
// did not, one of the two would be mounted behind a listener nothing dials.
func TestBothServiceBlocksDeriveOneProtocolID(t *testing.T) {
	require.Equal(t, Protocol, rpc.Protocol(buildv1connect.QueueServiceName))
	require.Equal(t, Protocol, rpc.Protocol(buildv1connect.JobServiceName))
	require.EqualValues(t, "/tau/build/v1", Protocol)
}

// v1 answers on "/patrick/v1" and "/monkey/v1" today, and both processes run
// in the same swarm and the same binary as this one until the swap. The "tau."
// segment in the proto package is what keeps them apart; pkg/proto's
// TestTheRulesBufCannotCheck holds it for every v2 package, reading v1's ids
// out of services/common/names.go. Restated here as the one-line form because
// this is the constant a reader of this package sees.
func TestProtocolIsNotOneOfV1s(t *testing.T) {
	require.NotEqualValues(t, "/patrick/v1", Protocol)
	require.NotEqualValues(t, "/monkey/v1", Protocol)
}

// The frontend's empty protocol id is a claim about the trust boundary, not an
// oversight: build.proto's header makes "this role hosts no RPC surface" the
// thing a reader can check. A registry that required a protocol id per
// descriptor would force one to be invented, and an invented id is a listener
// somebody eventually mounts.
func TestOnlyTheFrontendFacesTheInternetAndOnlyTheBackendHasAProtocol(t *testing.T) {
	require.Equal(t, IngressPublic, Frontend.Ingress)
	require.EqualValues(t, "", Frontend.Protocol, "the public role must have no protocol id")

	require.Equal(t, IngressNone, Backend.Ingress)
	require.Equal(t, Protocol, Backend.Protocol)

	require.NotEqual(t, Frontend.Name, Backend.Name)
	require.NotEmpty(t, Frontend.Name)
	require.NotEmpty(t, Backend.Name)
}

// IngressNone is the zero value so that a descriptor nobody finished still
// says "p2p only" rather than "public" — silence is safe.
func TestSilenceIsPrivate(t *testing.T) {
	var d Descriptor
	require.Equal(t, IngressNone, d.Ingress)
}

// QueueDomain is the literal build.proto and service-conventions.md §4 both
// fix. It is a key segment in pkg/queue's own layout, which escapes it, so
// the "/" in it is not a path boundary anywhere.
func TestQueueDomainIsTheLiteralTheProtoFixes(t *testing.T) {
	require.Equal(t, "build/queue", QueueDomain)
}
