package auth

import (
	"regexp"
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
)

// TestRepositoryStoreSurvivesAGitLabSubgroup is the store-layer half of the
// task's required scenario: a GitLab subgroup key — variable depth, one
// segment deeper than github.com/acme/pay — through register, fetch, list
// and delete, exercising store.go directly rather than through the RPC
// surface (rpc_test.go covers the same shape over the wire).
func TestRepositoryStoreSurvivesAGitLabSubgroup(t *testing.T) {
	kv := newStore(t)
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
	kv := newStore(t)
	shallow, err := parseRepoKey("github.com/acme/pay")
	require.NoError(t, err)
	deep, err := parseRepoKey("gitlab.com/acme/team/widget")
	require.NoError(t, err)

	who := peer.ID("QmSomePeer")
	require.NoError(t, registerRepository(t.Context(), kv, shallow, who))
	require.NoError(t, registerRepository(t.Context(), kv, deep, who))

	// A /project row and a /hooks/<id> row filed beside the deep key must
	// not appear in the listing as if they were repositories of their own,
	// which is why listRepositoryKeys matches the /key marker row rather
	// than listing the /repositories/ prefix raw.
	require.NoError(t, linkRepositoryToProject(t.Context(), kv, deep, "someProjectID"))
	require.NoError(t, kv.Put(t.Context(), repoHookRow(deep, "hook-1"), nil))

	keys, err := listRepositoryKeys(t.Context(), kv)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{shallow.String(), deep.String()}, keys)
}

// TestUnregisterLeavesNothingBehind: deleteRepository must remove /key,
// /registered_by, /project and every hook filed under the repository, all
// in one batch — no half-deleted state where some hooks are gone and others
// are not, and no row left under a key that no longer exists.
func TestUnregisterLeavesNothingBehind(t *testing.T) {
	kv := newStore(t)
	key, err := parseRepoKey("github.com/acme/pay")
	require.NoError(t, err)
	who := peer.ID("QmOwner")

	require.NoError(t, registerRepository(t.Context(), kv, key, who))
	require.NoError(t, linkRepositoryToProject(t.Context(), kv, key, "proj-1"))

	require.NoError(t, putHook(t.Context(), kv, "hook-1", hook(key.String())))
	require.NoError(t, kv.Put(t.Context(), repoHookRow(key, "hook-1"), nil))

	require.NoError(t, deleteRepository(t.Context(), kv, key))

	assertNothingUnderPrefix(t, kv, repoRoot(key))
	assertNothingUnderPrefix(t, kv, hookRoot("hook-1"))

	_, registered := repositoryOwner(t.Context(), kv, key)
	assert.False(t, registered)
	_, err = fetchHook(t.Context(), kv, "hook-1")
	assert.ErrorIs(t, err, errNoHook)
}

func assertNothingUnderPrefix(t testing.TB, kv kvdb.KVDB, prefix string) {
	t.Helper()
	rows, err := kv.List(t.Context(), prefix)
	require.NoError(t, err)
	assert.Empty(t, rows, "expected nothing left under %q, found %v", prefix, rows)
}

func TestProjectStore(t *testing.T) {
	kv := newStore(t)
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
	kv := newStore(t)
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
	kv := newStore(t)

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
	kv := newStore(t)
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

// ─────────────────────────── the key grammar ───────────────────────────
//
// store.go's header states one invariant — a literal is a segment, an id is
// exactly one segment, so every boundary sits at a fixed segment index —
// and the three tests below are that sentence, clause by clause.

// TestSegmentIsAlwaysExactlyOneSegment covers the encoding's half. The
// inputs are the ones that broke the raw splice or would break a
// hand-rolled escape: a variable-depth repository key, the reserved words
// this file also uses as literals, the path-cleaning specials
// go-datastore's ds.NewKey rewrites, and a percent sign, which is what an
// escape-based encoding would have had to escape as well.
func TestSegmentIsAlwaysExactlyOneSegment(t *testing.T) {
	for _, id := range []string{
		"github.com/acme/pay",
		"gitlab.com/acme/team/widget",
		"gitlab.com/acme/team/hooks",
		"gitlab.com/acme/team/hooks/widget/key",
		"key", "registered_by", "project", "hooks", "provider", "name",
		".", "..", "./..", "//", "/",
		"%2F", "%", "a b", "*.example.com", "Ω",
		"hook-1",
	} {
		seg := segment(id)
		assert.NotContains(t, seg, "/", "segment(%q) must be one segment", id)
		assert.NotEqual(t, ".", seg, "segment(%q) must survive path cleaning", id)
		assert.NotEqual(t, "..", seg, "segment(%q) must survive path cleaning", id)
		assert.NotEmpty(t, seg, "segment(%q) must not vanish", id)

		back, err := unsegment(seg)
		require.NoError(t, err)
		assert.Equal(t, id, back, "segment must round-trip, or two ids could share a row")
	}
}

// TestIdRowPatternMatchesOneSegmentOnly covers the anchoring's half. The
// pattern is what makes a listing's correctness LOCAL: it matches a row
// whose id position holds exactly one segment, and refuses one with a
// boundary left in it, without having to know what else is filed under the
// same prefix.
func TestIdRowPatternMatchesOneSegmentOnly(t *testing.T) {
	re := regexp.MustCompile(idRowPattern(repositoriesPrefix, repositoryKeyRowSuffix))

	assert.True(t, re.MatchString(repositoriesPrefix+segment("github.com/acme/pay")+repositoryKeyRowSuffix))

	for _, row := range []string{
		"/repositories/Z2l0/hooks/key", // a sub-namespace's row, not a marker row
		"/repositories//key",           // an empty id is not an id
		"/repositories/Z2l0/key/more",  // the suffix is the end of the row
		"/repositories/Z2l0/keyx",      // and it is the whole last segment
		"/x/repositories/Z2l0/key",     // anchored at the front as well as the back
	} {
		assert.False(t, re.MatchString(row), "%q must not read as a marker row", row)
	}
}

// TestListingRefusesARowItDidNotWrite: a row that matches the shape but
// whose id is not something segment() ever produced is a corrupt store, and
// says so rather than being silently skipped or handed back as a repository
// key made of nonsense.
func TestListingRefusesARowItDidNotWrite(t *testing.T) {
	kv := newStore(t)
	require.NoError(t, kv.Put(t.Context(), repositoriesPrefix+"not!base64"+repositoryKeyRowSuffix, []byte("pem")))

	_, err := listRepositoryKeys(t.Context(), kv)
	assert.Error(t, err)
}

// TestDeleteRepository_CorruptHookRow: the reverse index is the one listing
// whose rows this package deletes rather than reports, so a row it cannot
// decode has to stop the batch rather than be trimmed into some other hook's
// id and deleted along with everything else.
func TestDeleteRepository_CorruptHookRow(t *testing.T) {
	kv := newStore(t)
	key, err := parseRepoKey("github.com/acme/pay")
	require.NoError(t, err)
	require.NoError(t, registerRepository(t.Context(), kv, key, peer.ID("QmOwner")))
	require.NoError(t, kv.Put(t.Context(), repoHooksPrefix(key)+"not!base64", nil))

	assert.Error(t, deleteRepository(t.Context(), kv, key))

	_, registered := repositoryOwner(t.Context(), kv, key)
	assert.True(t, registered, "a refused delete must not have half-run")
}

// TestARowWithNoValueIsNotARegistration: a /key row present but empty is
// not a registered repository. It is the same defence getCertRow makes for
// certificates, and it matters more here — read as registered with no
// /registered_by beside it, the repository would answer every Register and
// every Unregister with PermissionDenied, since the owner it compares
// against is the empty peer id. Nobody could claim it and nobody could
// remove it.
func TestARowWithNoValueIsNotARegistration(t *testing.T) {
	kv := newStore(t)
	key, err := parseRepoKey("github.com/acme/pay")
	require.NoError(t, err)
	require.NoError(t, kv.Put(t.Context(), repoKeyRow(key), nil))

	_, err = fetchRepository(t.Context(), kv, key)
	assert.Error(t, err)

	_, registered := repositoryOwner(t.Context(), kv, key)
	assert.False(t, registered)
}
