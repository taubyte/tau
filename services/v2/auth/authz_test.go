package auth

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authv1 "github.com/taubyte/tau/pkg/v2/proto/gen/auth/v1"
)

// This file is the one denial-per-guarded-RPC set (service-conventions.md
// §6c(3)): RepositoryService.Register and .Unregister are the only guarded
// RPCs in this schema (authz.go explains why the other four services carry
// no check). Every allow and every deny here goes through newWireHarness —
// two real mocknet-backed libp2p hosts, connect-go, gostream — so the
// identity a denial turns on is the one thing this package cannot fake: the
// caller's actual authenticated peer id (p2p/rpc.RemotePeer), never a field
// this test could have set in a request instead.

func TestRegister_AllowsTheOriginalRegistrant(t *testing.T) {
	h := newWireHarness(t)
	key := "github.com/acme/pay"

	_, err := h.Repositories.Register(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: key}))
	require.NoError(t, err)

	// A repeat Register by the same peer is a no-op, not an error and not a
	// key rotation (repositories.go's Register doc).
	before, err := h.Repositories.Get(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: key}))
	require.NoError(t, err)

	_, err = h.Repositories.Register(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: key}))
	require.NoError(t, err)

	after, err := h.Repositories.Get(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: key}))
	require.NoError(t, err)
	assert.Equal(t, before.Msg.GetDeployKey(), after.Msg.GetDeployKey(), "a repeat Register by its own owner must not rotate the deploy key")
}

// TestRegister_DeniesAPeerThatDidNotRegisterIt is the transport-proven
// denial for Register: v1's own p2p path skips its authorization check
// entirely for any caller (authz.go's doc on registerGitHubRepository's
// `if client != nil` gate), so this is a genuinely new guard, not a ported
// one — and it has to hold against a SECOND, real, differently-identified
// peer, not a mocked claim of one.
func TestRegister_DeniesAPeerThatDidNotRegisterIt(t *testing.T) {
	h := newWireHarness(t)
	key := "github.com/acme/pay"

	_, err := h.Repositories.Register(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: key}))
	require.NoError(t, err)

	_, otherClient := h.secondCaller(t)
	_, err = otherClient.Register(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: key}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	// The original registration must be untouched by the denied attempt.
	got, err := h.Repositories.Get(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: key}))
	require.NoError(t, err)
	assert.NotEmpty(t, got.Msg.GetDeployKey())
}

// TestRegister_AnUnregisteredKeyIsOpenToAnyPeer is the other half of
// authorizeRepositoryOwner: the FIRST Register on a key has nothing to
// protect yet, so any authenticated peer may claim it — first writer wins,
// per authz.go's documented OSS default.
func TestRegister_AnUnregisteredKeyIsOpenToAnyPeer(t *testing.T) {
	h := newWireHarness(t)
	_, otherClient := h.secondCaller(t)

	_, err := otherClient.Register(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: "github.com/acme/pay"}))
	assert.NoError(t, err)
}

func TestUnregister_AllowsTheOwner(t *testing.T) {
	h := newWireHarness(t)
	key := "github.com/acme/pay"

	_, err := h.Repositories.Register(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: key}))
	require.NoError(t, err)

	_, err = h.Repositories.Unregister(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: key}))
	require.NoError(t, err)

	// v1's bug (commit 3a2111da): Delete removed only /key, leaving
	// /project dangling forever. Assert nothing at all survives.
	parsed, err := parseRepoKey(key)
	require.NoError(t, err)
	assertNothingUnderPrefix(t, h.Service.kv, repoRoot(parsed))
}

// TestUnregister_DeniesAPeerThatDidNotRegisterIt is the task's required
// "handler rejecting a caller it should reject, proven by the transport's
// identity rather than a mocked claim": a second real libp2p host, with its
// own genuine peer id, is denied — and the repository it tried to tear down
// is still fully there afterward.
func TestUnregister_DeniesAPeerThatDidNotRegisterIt(t *testing.T) {
	h := newWireHarness(t)
	key := "github.com/acme/pay"

	_, err := h.Repositories.Register(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: key}))
	require.NoError(t, err)

	otherNode, otherClient := h.secondCaller(t)
	assert.NotEqual(t, h.CallerNode.ID(), otherNode.ID(), "the denial must come from a genuinely different peer, not the same one twice")

	_, err = otherClient.Unregister(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: key}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	got, err := h.Repositories.Get(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: key}))
	require.NoError(t, err, "a denied Unregister must not have removed anything")
	assert.NotEmpty(t, got.Msg.GetDeployKey())
}

// TestUnregister_NotFoundIsNotConflatedWithDenied: a never-registered key
// answers NotFound, not PermissionDenied, whoever asks — authz.go's doc on
// why a never-registered key is not treated as a denial.
func TestUnregister_NotFoundIsNotConflatedWithDenied(t *testing.T) {
	h := newWireHarness(t)

	_, err := h.Repositories.Unregister(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: "github.com/acme/never-registered"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}
