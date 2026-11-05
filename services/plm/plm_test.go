package plm

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2/plmv2connect"
)

func TestBothServiceBlocksDeriveOneProtocolID(t *testing.T) {
	require.Equal(t, Protocol, rpc.Protocol(plmv2connect.QueueServiceName))
	require.Equal(t, Protocol, rpc.Protocol(plmv2connect.JobServiceName))
	require.EqualValues(t, "/tau/plm/v2", Protocol)
}

func TestOnlyBuildFacesTheInternetAndEachServesItsOwn(t *testing.T) {
	require.Equal(t, IngressPublic, PLM.Ingress)
	require.Equal(t, Protocol, PLM.Protocol, "plm serves the queue and the job API to other plm nodes")

	require.Equal(t, IngressNone, Builder.Ingress)
	require.Equal(t, BuilderProtocol, Builder.Protocol, "a builder serves its own protocol, over p2p alone")
	require.NotEqual(t, PLM.Protocol, Builder.Protocol)

	require.NotEqual(t, PLM.Name, Builder.Name)
	require.NotEmpty(t, PLM.Name)
	require.NotEmpty(t, Builder.Name)
}

func TestSilenceIsPrivate(t *testing.T) {
	var d Descriptor
	require.Equal(t, IngressNone, d.Ingress)
}

func TestQueueDomainIsTheLiteralTheProtoFixes(t *testing.T) {
	require.Equal(t, "plm/queue", QueueDomain)
}
