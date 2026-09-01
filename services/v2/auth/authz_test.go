package auth

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/auth/v1"
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

	// Nothing at all survives: /key, /registered_by, /project and every
	// hook row. v1 deletes /key and /project too (commit 3a2111da), so what
	// is being asserted here is the one batch and the v2-only
	// /registered_by row — see repositories.go's Unregister.
	parsed, err := parseRepoKey(key)
	require.NoError(t, err)
	assertNothingUnderPrefix(t, h.Store, repoRoot(parsed))
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
	assert.NotEqual(t, h.CallerHost.ID(), otherNode.ID(), "the denial must come from a genuinely different peer, not the same one twice")

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

// TestRepositoryTakeoverThroughANestedNamespaceIsImpossible is the
// regression test for the takeover store.go's header describes, driven end
// to end over the real transport by two genuinely different peers.
//
// The attack needs no privilege and no forged identity. Every RPC in it is
// one the attacker is entitled to make: registering a key nobody has
// claimed is first-writer-wins by design (authz.go), and unregistering a key
// you registered is your own business. What made it a takeover was purely
// the key layout — a repository key spliced into a path raw, so that the
// victim's rows landed inside the hook reverse index of a SHALLOWER key.
// Deleting that shallower key took the victim's /registered_by with it, and
// an unowned key is open to anyone.
//
// The three cases differ in how deep the victim sits relative to the
// reserved word, which is what decides which part of the layout catches
// them: revert segment() to the identity and the first and third fail
// immediately, while the second is caught by the anchored listing instead —
// so the set holds whether the encoding or the anchor is the thing that
// breaks.
func TestRepositoryTakeoverThroughANestedNamespaceIsImpossible(t *testing.T) {
	for _, tc := range []struct {
		name string
		// victim is registered first, by the harness's own caller.
		victim string
		// attacker is a DIFFERENT, unclaimed repository whose reserved
		// sub-namespace the victim's rows used to fall inside.
		attacker string
	}{
		{
			name:     "the victim's last segment is the reserved word",
			victim:   "gitlab.com/acme/team/hooks",
			attacker: "gitlab.com/acme/team",
		},
		{
			name:     "the victim is one segment deeper than the reserved word",
			victim:   "gitlab.com/acme/team/hooks/widget",
			attacker: "gitlab.com/acme/team",
		},
		{
			name:     "three segments is already enough",
			victim:   "gitlab.com/acme/hooks",
			attacker: "gitlab.com/acme",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newWireHarness(t)
			victimKey, err := parseRepoKey(tc.victim)
			require.NoError(t, err)

			_, err = h.Repositories.Register(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: tc.victim}))
			require.NoError(t, err)
			registered, err := h.Repositories.Get(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: tc.victim}))
			require.NoError(t, err)
			deployKey := registered.Msg.GetDeployKey()
			require.NotEmpty(t, deployKey)

			attackerHost, attackerClient := h.secondCaller(t)
			require.NotEqual(t, h.CallerHost.ID(), attackerHost.ID(), "the attacker must be a genuinely different peer")

			// Head on, the attacker is refused — this much always held.
			_, err = attackerClient.Unregister(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: tc.victim}))
			require.Error(t, err)
			assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

			// So it takes the long way round, entirely within its rights.
			_, err = attackerClient.Register(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: tc.attacker}))
			require.NoError(t, err, "an unclaimed key is open to any peer; that is not the bug")
			_, err = attackerClient.Unregister(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: tc.attacker}))
			require.NoError(t, err)

			// The victim's registration survives it, whole.
			after, err := h.Repositories.Get(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: tc.victim}))
			require.NoError(t, err, "the victim's repository must still be registered")
			assert.Equal(t, deployKey, after.Msg.GetDeployKey(), "and must still hold the same deploy key")

			// Including the row the whole attack was after: ownership.
			owner, stillRegistered := repositoryOwner(t.Context(), h.Service.kv, victimKey)
			require.True(t, stillRegistered)
			assert.Equal(t, h.CallerHost.ID(), owner, "/registered_by is what a deleted victim loses first")

			// So the attacker still cannot have it.
			_, err = attackerClient.Register(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: tc.victim}))
			require.Error(t, err)
			assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

			// And the owner can still tear down its own repository.
			_, err = h.Repositories.Unregister(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: tc.victim}))
			require.NoError(t, err)
			assertNothingUnderPrefix(t, h.Store, repoRoot(victimKey))
		})
	}
}
