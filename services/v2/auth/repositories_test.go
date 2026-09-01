package auth

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libp2p/go-libp2p/core/peer"

	authv1 "github.com/taubyte/tau/pkg/v2/proto/gen/auth/v1"
)

// This file covers RepositoryService's handler directly — no wire, no
// server, no listener (service-conventions.md §6a) — for everything that
// does not need a genuine caller identity or a server-streaming connection.
// List (which needs a real connect.ServerStream — handler_stream.go: "no
// exported constructor") and Register/Unregister's success and denial paths
// (which need a genuine rpc.RemotePeer, obtainable only over the real
// transport) live in rpc_test.go and authz_test.go instead.

func TestRepositoryGet_NotFound(t *testing.T) {
	s := newTestService(t)
	h := &repositoryHandler{s}

	_, err := h.Get(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: "github.com/acme/pay"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestRepositoryGet_BadKey(t *testing.T) {
	s := newTestService(t)
	h := &repositoryHandler{s}

	_, err := h.Get(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: ""}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestRepositoryGet_Found(t *testing.T) {
	s := newTestService(t)
	h := &repositoryHandler{s}
	key, err := parseRepoKey("github.com/acme/pay")
	require.NoError(t, err)
	require.NoError(t, registerRepository(t.Context(), s.kv, key, peer.ID("QmOwner")))

	res, err := h.Get(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: "github.com/acme/pay"}))
	require.NoError(t, err)
	assert.Equal(t, "github.com/acme/pay", res.Msg.GetRepositoryKey())
	assert.NotEmpty(t, res.Msg.GetDeployKey())
	assert.Equal(t, "", res.Msg.GetProjectId())
}

func TestRepositoryGet_CarriesProjectLink(t *testing.T) {
	s := newTestService(t)
	h := &repositoryHandler{s}
	key, err := parseRepoKey("github.com/acme/pay")
	require.NoError(t, err)
	require.NoError(t, registerRepository(t.Context(), s.kv, key, peer.ID("QmOwner")))
	require.NoError(t, linkRepositoryToProject(t.Context(), s.kv, key, "proj-1"))

	res, err := h.Get(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: "github.com/acme/pay"}))
	require.NoError(t, err)
	assert.Equal(t, "proj-1", res.Msg.GetProjectId())
}

func TestRepositoryRegister_BadKey(t *testing.T) {
	s := newTestService(t)
	h := &repositoryHandler{s}

	_, err := h.Register(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: ""}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

// TestRepositoryRegister_NoCallerIdentity proves Register fails closed — the
// same property p2p/rpc's own TestRemotePeerFailsClosed proves at the
// transport layer — for a context that carries no libp2p-authenticated
// peer, rather than falling back to trusting anything in the request.
func TestRepositoryRegister_NoCallerIdentity(t *testing.T) {
	s := newTestService(t)
	h := &repositoryHandler{s}

	_, err := h.Register(context.Background(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: "github.com/acme/pay"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestRepositoryUnregister_BadKey(t *testing.T) {
	s := newTestService(t)
	h := &repositoryHandler{s}

	_, err := h.Unregister(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: ""}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestRepositoryUnregister_NoCallerIdentity(t *testing.T) {
	s := newTestService(t)
	h := &repositoryHandler{s}

	_, err := h.Unregister(context.Background(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: "github.com/acme/pay"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}
