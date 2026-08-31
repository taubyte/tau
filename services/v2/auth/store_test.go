package auth

import (
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
	kvdbmock "github.com/taubyte/tau/pkg/kvdb/mock"
)

func newTestKV(t testing.TB) kvdb.KVDB {
	t.Helper()
	factory := kvdbmock.New()
	db, err := factory.New(testLogger, "store-test", 1)
	require.NoError(t, err)
	t.Cleanup(db.Close)
	return db
}

// TestRepositoryStoreSurvivesAGitLabSubgroup is the store-layer half of the
// task's required scenario: a GitLab subgroup key — variable depth, one
// segment deeper than github.com/acme/pay — through register, fetch, list
// and delete, exercising store.go directly rather than through the RPC
// surface (rpc_test.go covers the same shape over the wire).
func TestRepositoryStoreSurvivesAGitLabSubgroup(t *testing.T) {
	kv := newTestKV(t)
	key, err := parseRepoKey("gitlab.com/acme/team/widget")
	require.NoError(t, err)
	require.Equal(t, "acme/team", key.Owner, "the subgroup must be part of Owner, not swallowed into Name")

	owner := peer.ID("QmRegistrant")
	require.NoError(t, registerRepository(t.Context(), kv, key, owner))

	record, err := fetchRepository(t.Context(), kv, key)
	require.NoError(t, err)
	assert.NotEmpty(t, record.deployKey)
	assert.Equal(t, "", record.project)

	got, registered := repositoryOwner(t.Context(), kv, key)
	require.True(t, registered)
	assert.Equal(t, owner, got)

	keys, err := listRepositoryKeys(t.Context(), kv)
	require.NoError(t, err)
	assert.Equal(t, []string{key.String()}, keys)

	require.NoError(t, deleteRepository(t.Context(), kv, key))
	assertNothingUnderPrefix(t, kv, repoRoot(key))
}

// TestListRepositoryKeysAtMixedDepths is "listing a store whose keys are
// variable-depth": a three-segment key and a four-segment subgroup key
// registered side by side, neither miscounted as the other and neither
// dropping the other's /project or /hooks rows into the listing.
func TestListRepositoryKeysAtMixedDepths(t *testing.T) {
	kv := newTestKV(t)
	shallow, err := parseRepoKey("github.com/acme/pay")
	require.NoError(t, err)
	deep, err := parseRepoKey("gitlab.com/acme/team/widget")
	require.NoError(t, err)

	who := peer.ID("QmSomePeer")
	require.NoError(t, registerRepository(t.Context(), kv, shallow, who))
	require.NoError(t, registerRepository(t.Context(), kv, deep, who))

	// A /project row and a /hooks/<id> row filed beside the deep key must
	// not appear in the listing as if they were repositories of their own —
	// this is the exact shape commit 3a2111da's fix protects against, and
	// the reason listRepositoryKeys matches the /key marker row rather than
	// listing the /repositories/ prefix raw.
	require.NoError(t, linkRepositoryToProject(t.Context(), kv, deep, "someProjectID"))
	require.NoError(t, kv.Put(t.Context(), repoHookRow(deep, "hook-1"), nil))

	keys, err := listRepositoryKeys(t.Context(), kv)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{shallow.String(), deep.String()}, keys)
}

// TestUnregisterLeavesNothingBehind is the store-layer half of the task's
// other required scenario — v1's bug (commit 3a2111da) was that Delete
// removed only /key, leaving /project dangling forever. deleteRepository
// must remove /key, /registered_by, /project and every hook filed under the
// repository, all in one batch.
func TestUnregisterLeavesNothingBehind(t *testing.T) {
	kv := newTestKV(t)
	key, err := parseRepoKey("github.com/acme/pay")
	require.NoError(t, err)
	who := peer.ID("QmOwner")

	require.NoError(t, registerRepository(t.Context(), kv, key, who))
	require.NoError(t, linkRepositoryToProject(t.Context(), kv, key, "proj-1"))

	batch, err := kv.Batch(t.Context())
	require.NoError(t, err)
	require.NoError(t, batch.Put(hookProviderRow("hook-1"), []byte("github")))
	require.NoError(t, batch.Put(hookGithubIDRow("hook-1"), []byte("42")))
	require.NoError(t, batch.Put(hookGithubSecretRow("hook-1"), []byte("s3cr3t")))
	require.NoError(t, batch.Put(hookGithubRepoRow("hook-1"), []byte(key.String())))
	require.NoError(t, batch.Put(repoHookRow(key, "hook-1"), nil))
	require.NoError(t, batch.Commit())

	require.NoError(t, deleteRepository(t.Context(), kv, key))

	assertNothingUnderPrefix(t, kv, repoRoot(key))
	assertNothingUnderPrefix(t, kv, hookRoot("hook-1"))

	_, registered := repositoryOwner(t.Context(), kv, key)
	assert.False(t, registered)
	_, err = fetchHookProvider(t.Context(), kv, "hook-1")
	assert.Error(t, err)
}

func assertNothingUnderPrefix(t testing.TB, kv kvdb.KVDB, prefix string) {
	t.Helper()
	rows, err := kv.List(t.Context(), prefix)
	require.NoError(t, err)
	assert.Empty(t, rows, "expected nothing left under %q, found %v", prefix, rows)
}

func TestProjectStore(t *testing.T) {
	kv := newTestKV(t)
	config, err := parseRepoKey("github.com/acme/config")
	require.NoError(t, err)
	code, err := parseRepoKey("github.com/acme/code")
	require.NoError(t, err)

	require.NoError(t, putProject(t.Context(), kv, "proj-1", "my project", config, code))

	record, err := fetchProject(t.Context(), kv, "proj-1")
	require.NoError(t, err)
	assert.Equal(t, "my project", record.name)
	assert.Equal(t, config.String(), record.config)
	assert.Equal(t, code.String(), record.code)

	ids, err := listProjectIDs(t.Context(), kv)
	require.NoError(t, err)
	assert.Equal(t, []string{"proj-1"}, ids)

	_, err = fetchProject(t.Context(), kv, "does-not-exist")
	assert.Error(t, err)
}

func TestLinkRepositoryToProjectDoesNotRequireRegistration(t *testing.T) {
	kv := newTestKV(t)
	key, err := parseRepoKey("github.com/acme/pay")
	require.NoError(t, err)

	// Linking a key that was never Registered must not make it appear in
	// listRepositoryKeys — only the /key marker row does that.
	require.NoError(t, linkRepositoryToProject(t.Context(), kv, key, "proj-1"))

	keys, err := listRepositoryKeys(t.Context(), kv)
	require.NoError(t, err)
	assert.Empty(t, keys)
}

func TestCertificateStore(t *testing.T) {
	kv := newTestKV(t)

	require.NoError(t, kv.Put(t.Context(), acmeCertRow("example.com"), []byte("acme-pem")))
	got, err := getCertRow(t.Context(), kv, acmeCertRow("example.com"))
	require.NoError(t, err)
	assert.Equal(t, []byte("acme-pem"), got)

	_, err = getCertRow(t.Context(), kv, acmeCertRow("missing.example.com"))
	assert.Error(t, err)

	require.NoError(t, kv.Put(t.Context(), staticCertRow("www.example.com"), []byte("static-pem")))
	got, err = getCertRow(t.Context(), kv, staticCertRow(wildcardFQDN("anything.example.com")))
	assert.Error(t, err) // no wildcard row stored yet
	assert.Nil(t, got)

	require.NoError(t, kv.Put(t.Context(), staticCertRow(wildcardFQDN("www.example.com")), []byte("wildcard-pem")))
	got, err = getCertRow(t.Context(), kv, staticCertRow(wildcardFQDN("anything.example.com")))
	require.NoError(t, err)
	assert.Equal(t, []byte("wildcard-pem"), got)
}

func TestGetCertRowSelfHealsAnEmptyRow(t *testing.T) {
	kv := newTestKV(t)
	require.NoError(t, kv.Put(t.Context(), acmeCertRow("empty.example.com"), nil))

	_, err := getCertRow(t.Context(), kv, acmeCertRow("empty.example.com"))
	assert.Error(t, err)

	rows, err := kv.List(t.Context(), acmeCertRow("empty.example.com"))
	require.NoError(t, err)
	assert.Empty(t, rows, "an empty row must be deleted, not left behind as a false hit")
}

func TestWildcardFQDN(t *testing.T) {
	assert.Equal(t, "*.example.com", wildcardFQDN("www.example.com"))
	assert.Equal(t, "*", wildcardFQDN("localhost"))
}

func TestParseRepoKeyRejectsGarbage(t *testing.T) {
	_, err := parseRepoKey("")
	assert.Error(t, err)
}

func TestGenerateECKeyPairProducesParsablePEM(t *testing.T) {
	priv, pub, err := generateECKeyPair()
	require.NoError(t, err)
	assert.Contains(t, priv, "EC PRIVATE KEY")
	assert.Contains(t, pub, "PUBLIC KEY")
}
