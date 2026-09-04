package auth

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authv1 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v1"
)

// TestRepositoryLifecycle_GitLabSubgroup is the task's required scenario run
// over the real wire, start to finish: a GitLab subgroup key — one path
// segment deeper than github.com/acme/pay — through Register, Get, List and
// Unregister, as the actual generated ConnectRPC client a real caller would
// use, not a direct call. Register needs a genuine caller identity, which is
// only obtainable over the transport (harness_test.go), so this is also
// this package's proof that a variable-depth key survives the wire without
// being truncated or mis-split anywhere between the client and the store.
func TestRepositoryLifecycle_GitLabSubgroup(t *testing.T) {
	h := newWireHarness(t)
	const key = "gitlab.com/acme/team/widget"

	_, err := h.Repositories.Register(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: key}))
	require.NoError(t, err)

	got, err := h.Repositories.Get(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: key}))
	require.NoError(t, err)
	assert.Equal(t, key, got.Msg.GetRepositoryKey())
	assert.NotEmpty(t, got.Msg.GetDeployKey())

	stream, err := h.Repositories.List(t.Context(), connect.NewRequest(&authv1.Empty{}))
	require.NoError(t, err)
	var listed []string
	for stream.Receive() {
		listed = append(listed, stream.Msg().GetRepositoryKey())
	}
	require.NoError(t, stream.Err())
	assert.Equal(t, []string{key}, listed)

	_, err = h.Repositories.Unregister(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: key}))
	require.NoError(t, err)

	_, err = h.Repositories.Get(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: key}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	stream, err = h.Repositories.List(t.Context(), connect.NewRequest(&authv1.Empty{}))
	require.NoError(t, err)
	listed = nil
	for stream.Receive() {
		listed = append(listed, stream.Msg().GetRepositoryKey())
	}
	require.NoError(t, stream.Err())
	assert.Empty(t, listed, "Unregister must leave nothing a List can still see")
}

// TestProjectList_OverTheWire is the server-streaming proof for
// ProjectService: connect.ServerStream has no exported constructor
// (handler_stream.go), so List can only be exercised over a real
// connection.
func TestProjectList_OverTheWire(t *testing.T) {
	h := newWireHarness(t)

	config, err := parseRepoKey("github.com/acme/config")
	require.NoError(t, err)
	code, err := parseRepoKey("github.com/acme/code")
	require.NoError(t, err)
	require.NoError(t, putProject(t.Context(), h.Service.kv, "proj-1", "demo", config, code))

	stream, err := h.Projects.List(t.Context(), connect.NewRequest(&authv1.Empty{}))
	require.NoError(t, err)
	var ids []string
	for stream.Receive() {
		ids = append(ids, stream.Msg().GetId())
	}
	require.NoError(t, stream.Err())
	assert.Equal(t, []string{"proj-1"}, ids)
}

// TestHookList_OverTheWire is HookService's server-streaming proof, same
// reasoning as TestProjectList_OverTheWire.
func TestHookList_OverTheWire(t *testing.T) {
	h := newWireHarness(t)

	batch, err := h.Service.kv.Batch(t.Context())
	require.NoError(t, err)
	require.NoError(t, batch.Put(hookProviderRow("hook-1"), []byte("github")))
	require.NoError(t, batch.Commit())

	stream, err := h.Hooks.List(t.Context(), connect.NewRequest(&authv1.Empty{}))
	require.NoError(t, err)
	var ids []string
	for stream.Receive() {
		ids = append(ids, stream.Msg().GetId())
	}
	require.NoError(t, stream.Err())
	assert.Equal(t, []string{"hook-1"}, ids)
}

// TestRemotePeerReachesAuthzThroughTheRealTransport is the harness's own
// sanity check: the caller identity Register records is exactly the
// caller's real libp2p peer id, not merely "some string that was present".
func TestRemotePeerReachesAuthzThroughTheRealTransport(t *testing.T) {
	h := newWireHarness(t)
	key, err := parseRepoKey("github.com/acme/pay")
	require.NoError(t, err)

	_, err = h.Repositories.Register(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: key.String()}))
	require.NoError(t, err)

	owner, registered := repositoryOwner(t.Context(), h.Service.kv, key)
	require.True(t, registered)
	assert.Equal(t, h.CallerHost.ID(), owner)
}

// The three tests below inject a store failure AFTER the harness's Service
// is already serving, by swapping its kv for a wrapper that still delegates
// reads to the same underlying store (store_failures_test.go's
// failingKVDB) and only fails the write the RPC under test is about to make
// — reaching each streaming List handler's own listing-error branch and
// Register/Unregister's own internal-error branch, over the real transport,
// which is the only way to reach a List handler at all (connect.ServerStream
// has no exported constructor) and the only way Register/Unregister ever
// run (they require a genuine caller identity).

func TestRepositoryList_StoreFails(t *testing.T) {
	h := newWireHarness(t)
	h.Service.kv = &failingKVDB{Store: h.Service.kv, failListRegEx: true}

	stream, err := h.Repositories.List(t.Context(), connect.NewRequest(&authv1.Empty{}))
	require.NoError(t, err)
	assert.False(t, stream.Receive())
	require.Error(t, stream.Err())
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(stream.Err()))
}

func TestProjectList_StoreFails(t *testing.T) {
	h := newWireHarness(t)
	h.Service.kv = &failingKVDB{Store: h.Service.kv, failListRegEx: true}

	stream, err := h.Projects.List(t.Context(), connect.NewRequest(&authv1.Empty{}))
	require.NoError(t, err)
	assert.False(t, stream.Receive())
	require.Error(t, stream.Err())
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(stream.Err()))
}

func TestHookList_StoreFails(t *testing.T) {
	h := newWireHarness(t)
	h.Service.kv = &failingKVDB{Store: h.Service.kv, failListRegEx: true}

	stream, err := h.Hooks.List(t.Context(), connect.NewRequest(&authv1.Empty{}))
	require.NoError(t, err)
	assert.False(t, stream.Receive())
	require.Error(t, stream.Err())
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(stream.Err()))
}

func TestRegister_StoreFailureIsInternal(t *testing.T) {
	h := newWireHarness(t)
	h.Service.kv = &failingKVDB{Store: h.Service.kv, failBatch: true}

	_, err := h.Repositories.Register(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: "github.com/acme/pay"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestUnregister_StoreFailureIsInternal(t *testing.T) {
	h := newWireHarness(t)
	key := "github.com/acme/pay"
	_, err := h.Repositories.Register(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: key}))
	require.NoError(t, err)

	h.Service.kv = &failingKVDB{Store: h.Service.kv, failBatch: true}

	_, err = h.Repositories.Unregister(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: key}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}
