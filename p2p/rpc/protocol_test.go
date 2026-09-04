package rpc_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v1/authv1connect"
	"github.com/taubyte/tau/pkg/proto/gen/tau/build/v1/buildv1connect"
)

// The three rows of service-conventions.md §2d's table that exist today,
// asserted against the generated names rather than against a string typed
// here twice: whatever the schema declares is what these constants say, so a
// package rename shows up as a failure at exactly one place.
func TestProtocolIsTheProtoPackage(t *testing.T) {
	require.EqualValues(t, "/tau/auth/v1", rpc.Protocol(authv1connect.ProjectServiceName))
	require.EqualValues(t, "/tau/build/v1", rpc.Protocol(buildv1connect.QueueServiceName))

	// Every service block in one package derives the same id — the id names
	// the listener, and five blocks share one.
	require.Equal(t,
		rpc.Protocol(authv1connect.ProjectServiceName),
		rpc.Protocol(authv1connect.CertificateServiceName))
	require.Equal(t,
		rpc.Protocol(buildv1connect.QueueServiceName),
		rpc.Protocol(buildv1connect.JobServiceName))
}
