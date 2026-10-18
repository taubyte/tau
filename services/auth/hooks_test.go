package auth

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authv1 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v1"
)

// HookService.Get is covered here as a direct call. .List needs a real
// connect.ServerStream and is covered in rpc_test.go. Nothing in this
// package's RPC surface creates a hook (hooks.go's package doc), so every
// fixture below is written straight through store.go's own row functions —
// the same putHook whatever registers a repository writes through.

func hook(repository string) hookRecord {
	return hookRecord{repository: repository, handle: "42", scheme: "hub-sha256", secret: "s3cr3t"}
}

func TestHookGet_NotFound(t *testing.T) {
	s := newTestService(t)
	h := &hookHandler{s}

	_, err := h.Get(t.Context(), connect.NewRequest(&authv1.HookRef{Id: "does-not-exist"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

// A hook carries its repository, the forge's handle, the scheme it was
// created under and its secret — and nothing that names a provider.
func TestHookGet(t *testing.T) {
	s := newTestService(t)
	h := &hookHandler{s}
	require.NoError(t, putHook(t.Context(), s.kv, "hook-1", hook("gitlab.example.com/acme/pay")))

	res, err := h.Get(t.Context(), connect.NewRequest(&authv1.HookRef{Id: "hook-1"}))
	require.NoError(t, err)
	assert.Equal(t, "hook-1", res.Msg.GetId())
	assert.Equal(t, "gitlab.example.com/acme/pay", res.Msg.GetRepositoryKey())
	assert.Equal(t, "42", res.Msg.GetHandle())
	assert.Equal(t, "hub-sha256", res.Msg.GetScheme())
	assert.Equal(t, []byte("s3cr3t"), res.Msg.GetSecret())
}

// A REGISTERED HOOK MISSING A ROW IS A BROKEN STORE, not an unknown hook:
// answering NotFound would read, in a forge's delivery log, like a deleted hook.
func TestHookGet_MissingRow(t *testing.T) {
	for _, row := range []func(string) string{hookHandleRow, hookSchemeRow, hookSecretRow} {
		s := newTestService(t)
		h := &hookHandler{s}
		require.NoError(t, putHook(t.Context(), s.kv, "hook-1", hook("github.com/acme/pay")))
		require.NoError(t, s.kv.Delete(t.Context(), row("hook-1")))

		_, err := h.Get(t.Context(), connect.NewRequest(&authv1.HookRef{Id: "hook-1"}))
		require.Error(t, err)
		assert.Equal(t, connect.CodeInternal, connect.CodeOf(err), row("hook-1"))
	}
}

// A STORE THAT FAILED IS NOT AN UNKNOWN HOOK.
func TestHookGet_StoreFailure(t *testing.T) {
	s := newTestService(t)
	require.NoError(t, putHook(t.Context(), s.kv, "hook-1", hook("github.com/acme/pay")))
	s.kv = &gettingKVDB{Store: s.kv, fail: map[string]bool{hookRepoRow("hook-1"): true}}

	_, err := (&hookHandler{s}).Get(t.Context(), connect.NewRequest(&authv1.HookRef{Id: "hook-1"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

// gettingKVDB fails a read of the named rows.
type gettingKVDB struct {
	Store
	fail map[string]bool
}

func (k *gettingKVDB) Get(ctx context.Context, key string) ([]byte, error) {
	if k.fail[key] {
		return nil, errStoreFailure
	}
	return k.Store.Get(ctx, key)
}
