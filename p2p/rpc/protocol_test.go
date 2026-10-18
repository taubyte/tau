package rpc_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
	"github.com/taubyte/tau/pkg/proto/gen/tau/build/v2/buildv2connect"
)

// The three rows of service-conventions.md §2d's table that exist today,
// asserted against the generated names rather than against a string typed
// here twice: whatever the schema declares is what these constants say, so a
// package rename shows up as a failure at exactly one place.
func TestProtocolIsTheProtoPackage(t *testing.T) {
	require.EqualValues(t, "/tau/auth/v2", rpc.Protocol(authv2connect.ProjectServiceName))
	require.EqualValues(t, "/tau/build/v2", rpc.Protocol(buildv2connect.QueueServiceName))

	// Every service block in one package derives the same id — the id names
	// the listener, and five blocks share one.
	require.Equal(t,
		rpc.Protocol(authv2connect.ProjectServiceName),
		rpc.Protocol(authv2connect.CertificateServiceName))
	require.Equal(t,
		rpc.Protocol(buildv2connect.QueueServiceName),
		rpc.Protocol(buildv2connect.JobServiceName))
}
