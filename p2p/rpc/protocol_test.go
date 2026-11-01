package rpc_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
	"github.com/taubyte/tau/pkg/proto/gen/tau/build/v2/buildv2connect"
)

// The protocol ids are asserted against the generated names, so a package
// rename fails in exactly one place.
func TestProtocolIsTheProtoPackage(t *testing.T) {
	require.EqualValues(t, "/tau/auth/v2", rpc.Protocol(authv2connect.IdentityServiceName))
	require.EqualValues(t, "/tau/build/v2", rpc.Protocol(buildv2connect.QueueServiceName))

	// Every service block in one package derives the same id — the id names
	// the listener, and the blocks share one.
	require.Equal(t,
		rpc.Protocol(buildv2connect.QueueServiceName),
		rpc.Protocol(buildv2connect.JobServiceName))
}
