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

func TestOnlyBuildFacesTheInternetAndOnlyBuildServes(t *testing.T) {
	require.Equal(t, IngressPublic, Build.Ingress)
	require.Equal(t, Protocol, Build.Protocol, "build serves the queue to runners and to other build nodes")

	require.Equal(t, IngressNone, Runner.Ingress)
	require.EqualValues(t, "", Runner.Protocol, "a runner serves nothing; it opens its own stream")

	require.NotEqual(t, Build.Name, Runner.Name)
	require.NotEmpty(t, Build.Name)
	require.NotEmpty(t, Runner.Name)
}

func TestSilenceIsPrivate(t *testing.T) {
	var d Descriptor
	require.Equal(t, IngressNone, d.Ingress)
}

func TestQueueDomainIsTheLiteralTheProtoFixes(t *testing.T) {
	require.Equal(t, "build/queue", QueueDomain)
}
