package build

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/gen/tau/build/v2/buildv2connect"
)

func TestBothServiceBlocksDeriveOneProtocolID(t *testing.T) {
	require.Equal(t, Protocol, rpc.Protocol(buildv2connect.QueueServiceName))
	require.Equal(t, Protocol, rpc.Protocol(buildv2connect.JobServiceName))
	require.EqualValues(t, "/tau/build/v2", Protocol)
}

func TestOnlyTheFrontendFacesTheInternetAndOnlyTheBackendHasAProtocol(t *testing.T) {
	require.Equal(t, IngressPublic, Frontend.Ingress)
	require.EqualValues(t, "", Frontend.Protocol, "the public role must have no protocol id")

	require.Equal(t, IngressNone, Backend.Ingress)
	require.Equal(t, Protocol, Backend.Protocol)

	require.NotEqual(t, Frontend.Name, Backend.Name)
	require.NotEmpty(t, Frontend.Name)
	require.NotEmpty(t, Backend.Name)
}

func TestSilenceIsPrivate(t *testing.T) {
	var d Descriptor
	require.Equal(t, IngressNone, d.Ingress)
}

func TestQueueDomainIsTheLiteralTheProtoFixes(t *testing.T) {
	require.Equal(t, "build/queue", QueueDomain)
}
