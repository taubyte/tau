package backend

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
)

func mustKey(t *testing.T, s string) repokey.Key {
	t.Helper()
	k, ok := parseRepositoryKey(s)
	require.True(t, ok, "%q must be a canonical repository key", s)
	return k
}

func TestASeparatorJoinedTripleCollidesAndTheFramedOneDoesNot(t *testing.T) {
	naive := func(k, r, s string) string {
		sum := sha256.Sum256([]byte(k + ":" + r + ":" + s))
		return hex.EncodeToString(sum[:])
	}

	key := mustKey(t, "github.com/o/n")

	require.Equal(t,
		naive(key.String(), "refs/heads/x:deadbeef", "c"),
		naive(key.String(), "refs/heads/x", "deadbeef:c"),
		"the naive form must be shown to collide, or this test proves nothing")
	require.Equal(t, "9913daff5bd6d6b306ccef31848fe940e88a62ca8b8187435af49c5d6ee39277",
		naive(key.String(), "refs/heads/x", "deadbeef:c"))

	require.NotEqual(t,
		jobID(key, "refs/heads/x:deadbeef", "c"),
		jobID(key, "refs/heads/x", "deadbeef:c"))
}

func TestTheRepositoryKeyAndTheRefCannotBeRecut(t *testing.T) {
	a := jobID(mustKey(t, "github.com/o/n"), "refs/heads/x", strings.Repeat("a", 40))
	b := jobID(mustKey(t, "github.com/o"), "refs/heads/x", strings.Repeat("a", 40))
	require.NotEqual(t, a, b)
}

func TestATripleRecutAtTheKeyRefBoundaryDoesNotCollide(t *testing.T) {
	sha := strings.Repeat("a", 40)
	left := struct{ key, ref string }{"github.com/o/n", "refs/heads/arefs/heads/x"}
	right := struct{ key, ref string }{"github.com/o/nrefs/heads/a", "refs/heads/x"}

	// Unframed the two are indistinguishable, or this test proves nothing.
	require.Equal(t, left.key+left.ref+sha, right.key+right.ref+sha)

	// And both are triples this service would accept — mustKey is
	// parseRepositoryKey, which is Enqueue's own check.
	require.True(t, validRef(left.ref), "%q must be a ref this service builds", left.ref)
	require.True(t, validRef(right.ref), "%q must be a ref this service builds", right.ref)
	require.True(t, validCommitSHA(sha))

	require.NotEqual(t,
		jobID(mustKey(t, left.key), left.ref, sha),
		jobID(mustKey(t, right.key), right.ref, sha),
		"a length prefix is what keeps the key/ref boundary from sliding")
}

func TestAJobIDIsDeterministicAndExactlyOneKeySegment(t *testing.T) {
	key := mustKey(t, "github.com/acme/pay")
	sha := strings.Repeat("d", 40)

	id := jobID(key, "refs/heads/main", sha)
	require.Equal(t, id, jobID(key, "refs/heads/main", sha), "the same push must mint the same id")
	require.Len(t, id, 64)
	require.NoError(t, requireJobID(id))
	require.NotContains(t, id, "/")

	require.NotEqual(t, id, jobID(key, "refs/heads/other", sha))
	require.NotEqual(t, id, jobID(key, "refs/heads/main", strings.Repeat("e", 40)))
}

func TestTheDomainSeparatorIsInTheDigest(t *testing.T) {
	require.Contains(t, jobIDDomain, "tau.build.v2")
	require.True(t, strings.HasSuffix(jobIDDomain, "\x00"))

	// Without the domain, framing alone would give a different digest; the
	// point is only that the constant participates.
	key := mustKey(t, "github.com/a/b")
	require.NotEqual(t,
		base64.RawURLEncoding.EncodeToString(func() []byte {
			h := sha256.New()
			h.Write([]byte(key.String() + "refs/heads/main" + strings.Repeat("a", 40)))
			return h.Sum(nil)
		}()),
		jobID(key, "refs/heads/main", strings.Repeat("a", 40)))
}

func TestRequireJobIDRefusesAnythingItCouldNotHaveMinted(t *testing.T) {
	require.Error(t, requireJobID(""))
	for _, bad := range []string{
		strings.Repeat("A", 42), strings.Repeat("A", 44),
		strings.Repeat("A", 42) + "/", strings.Repeat("A", 42) + "=",
		"..", "../../etc/passwd", strings.Repeat("A", 42) + ".",
	} {
		require.Error(t, requireJobID(bad), "requireJobID(%q) must fail", bad)
	}
}

func TestValidRef(t *testing.T) {
	for _, ok := range []string{
		"refs/heads/main", "refs/heads/feature/x", "refs/heads/v1.2.3",
		"refs/heads/a-b_c", "refs/heads/" + strings.Repeat("a", 255),
	} {
		require.True(t, validRef(ok), "%q must be accepted", ok)
	}
	for _, bad := range []string{
		"", "main", "refs/tags/v1", "refs/heads/", "refs/heads/..",
		"refs/heads/a/../b", "refs/heads/a//b", "refs/heads/a b",
		"refs/heads/a;rm -rf", "refs/heads/" + strings.Repeat("a", 256),
		"refs/heads/.", "refs/heads/a/.", "refs/heads/a/",
	} {
		require.False(t, validRef(bad), "%q must be refused", bad)
	}
}

func TestValidCommitSHA(t *testing.T) {
	require.True(t, validCommitSHA(strings.Repeat("0", 40)))
	require.True(t, validCommitSHA("0123456789abcdef0123456789abcdef01234567"))
	for _, bad := range []string{
		"", strings.Repeat("0", 39), strings.Repeat("0", 41),
		strings.Repeat("A", 40), "0123456789ABCDEF0123456789abcdef01234567",
		strings.Repeat("g", 40),
	} {
		require.False(t, validCommitSHA(bad), "%q must be refused", bad)
	}
}

func TestParseRepositoryKeyAcceptsOnlyTheCanonicalSpelling(t *testing.T) {
	for _, ok := range []string{
		"github.com/acme/pay",
		"gitlab.com/acme/team/widget",
		"github.com:8443/acme/pay",
	} {
		k, valid := parseRepositoryKey(ok)
		require.True(t, valid, "%q must be accepted", ok)
		require.Equal(t, ok, k.String())
	}

	for _, bad := range []string{
		"", "GitHub.com/Acme/Pay", "github.com/acme/pay.git", "github.com/acme/pay/",
		"github.com:443/acme/pay", "github.com/acme/pay?x=1", "github.com/acme/pay#f",
		"github.com//acme/pay", "git@github.com:acme/pay", "github.com/a b/c",
		"evil.com/../x", "https://github.com/acme/pay", "github.com/acme/pay%2f..",
	} {
		_, valid := parseRepositoryKey(bad)
		require.False(t, valid, "%q must be refused", bad)
	}
}

func TestANonsenseHostIsCanonicalAndHarmless(t *testing.T) {
	k, ok := parseRepositoryKey("../etc/passwd")
	require.True(t, ok)
	require.Equal(t, "../etc/passwd", k.String())
	require.NotContains(t, seg(k.String()), "/")
	require.NotContains(t, seg(k.String()), ".")
}
