package auth

import (
	"strconv"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authv1 "github.com/taubyte/tau/pkg/v2/proto/gen/auth/v1"
)

// HookService.Get is covered here as a direct call. .List needs a real
// connect.ServerStream and is covered in rpc_test.go. Nothing in this
// package's RPC surface creates a hook (hooks.go's package doc), so every
// fixture below is written straight through store.go's own row functions —
// exactly the shape a future GitHub-token HTTP rewrite would eventually
// write through, per that same doc.

func putGithubHookFixture(t testing.TB, s *Service, id string, providerHookID int64, secret, repository string) {
	t.Helper()
	batch, err := s.kv.Batch(t.Context())
	require.NoError(t, err)
	require.NoError(t, batch.Put(hookProviderRow(id), []byte("github")))
	require.NoError(t, batch.Put(hookGithubIDRow(id), []byte(strconv.FormatInt(providerHookID, 10))))
	require.NoError(t, batch.Put(hookGithubSecretRow(id), []byte(secret)))
	require.NoError(t, batch.Put(hookGithubRepoRow(id), []byte(repository)))
	require.NoError(t, batch.Commit())
}

func TestHookGet_NotFound(t *testing.T) {
	s, _ := newTestService(t)
	h := &hookHandler{s}

	_, err := h.Get(t.Context(), connect.NewRequest(&authv1.HookRef{Id: "does-not-exist"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestHookGet_Github(t *testing.T) {
	s, _ := newTestService(t)
	h := &hookHandler{s}
	putGithubHookFixture(t, s, "hook-1", 42, "s3cr3t", "github.com/acme/pay")

	res, err := h.Get(t.Context(), connect.NewRequest(&authv1.HookRef{Id: "hook-1"}))
	require.NoError(t, err)
	gh := res.Msg.GetGithub()
	require.NotNil(t, gh)
	assert.Equal(t, "hook-1", gh.GetId())
	assert.Equal(t, int64(42), gh.GetProviderHookId())
	assert.Equal(t, "s3cr3t", gh.GetSecret())
	assert.Equal(t, "github.com/acme/pay", gh.GetRepositoryKey())
	assert.Nil(t, res.Msg.GetBitbucket())
}

// TestHookGet_UnsupportedProvider is BitbucketHook's documented state:
// reachable in the schema (auth.proto's Hook oneof), unreachable in the
// running service (hooks.go's doc, mirroring services/auth/hooks/hooks.go's
// own Fetch, which has no bitbucket case either).
func TestHookGet_UnsupportedProvider(t *testing.T) {
	s, _ := newTestService(t)
	h := &hookHandler{s}
	require.NoError(t, s.kv.Put(t.Context(), hookProviderRow("hook-2"), []byte("bitbucket")))

	_, err := h.Get(t.Context(), connect.NewRequest(&authv1.HookRef{Id: "hook-2"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
}

func TestHookGet_CorruptGithubRow(t *testing.T) {
	s, _ := newTestService(t)
	h := &hookHandler{s}
	require.NoError(t, s.kv.Put(t.Context(), hookProviderRow("hook-3"), []byte("github")))
	require.NoError(t, s.kv.Put(t.Context(), hookGithubIDRow("hook-3"), []byte("not-a-number")))

	_, err := h.Get(t.Context(), connect.NewRequest(&authv1.HookRef{Id: "hook-3"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

// TestHookGet_MissingSecretRow and TestHookGet_MissingRepositoryRow reach
// fetchGithubHook's other two row reads failing — a hook whose /provider
// and /github/id rows exist but is missing a row a real Register would
// always have written together (store.go's registerRepository-equivalent
// for hooks does not exist yet, per hooks.go's own doc, so nothing enforces
// this today beyond fetchGithubHook noticing).
func TestHookGet_MissingSecretRow(t *testing.T) {
	s, _ := newTestService(t)
	h := &hookHandler{s}
	require.NoError(t, s.kv.Put(t.Context(), hookProviderRow("hook-4"), []byte("github")))
	require.NoError(t, s.kv.Put(t.Context(), hookGithubIDRow("hook-4"), []byte("1")))

	_, err := h.Get(t.Context(), connect.NewRequest(&authv1.HookRef{Id: "hook-4"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestHookGet_MissingRepositoryRow(t *testing.T) {
	s, _ := newTestService(t)
	h := &hookHandler{s}
	require.NoError(t, s.kv.Put(t.Context(), hookProviderRow("hook-5"), []byte("github")))
	require.NoError(t, s.kv.Put(t.Context(), hookGithubIDRow("hook-5"), []byte("1")))
	require.NoError(t, s.kv.Put(t.Context(), hookGithubSecretRow("hook-5"), []byte("s3cr3t")))

	_, err := h.Get(t.Context(), connect.NewRequest(&authv1.HookRef{Id: "hook-5"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}
