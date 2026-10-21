package frontend

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
)

// A server that offers no shallow fetch (go-git's own, behind the fake
// forge) is read and committed to all the same: the whole history is
// fetched, still under the bound.
func TestContentAgainstAServerWithoutShallow(t *testing.T) {
	s, err := fake.Serve(fake.GitHubLike)
	require.NoError(t, err)
	defer s.Close()
	u, err := url.Parse("http://" + s.Host() + "/" + s.Keys().Writable.FullName() + ".git")
	require.NoError(t, err)
	remote := Remote{URL: u, Transport: forge.PinnedTransport(s.Host(), nil, func(h http.Header) {
		(&http.Request{Header: h}).SetBasicAuth("x-access-token", s.TokenString())
	})}

	parent := plumbing.ZeroHash
	for _, body := range []string{"one", "two"} {
		tip, err := fetchTip(t.Context(), remote, "", 1<<20)
		require.NoError(t, err)
		c := &committer{st: tip.st}
		root, parents := plumbing.ZeroHash, []plumbing.Hash(nil)
		ref := plumbing.NewBranchReferenceName("main")
		if !tip.empty {
			root, parents, ref = tip.commit.TreeHash, []plumbing.Hash{tip.commit.Hash}, tip.ref
			require.Equal(t, parent, tip.commit.Hash)
		}
		blob, err := c.blob([]byte(body))
		require.NoError(t, err)
		root, err = c.put(root, []string{"file"}, &blob)
		require.NoError(t, err)
		commit, err := c.commit(&object.Commit{Message: body, TreeHash: root, ParentHashes: parents})
		require.NoError(t, err)
		require.NoError(t, push(t.Context(), remote, ref, tip.old, commit, c))
		parent = commit
	}

	tip, err := fetchTip(t.Context(), remote, "main", 1<<20)
	require.NoError(t, err)
	require.Equal(t, parent, tip.commit.Hash)
	f, err := tip.commit.File("file")
	require.NoError(t, err)
	got, err := f.Contents()
	require.NoError(t, err)
	require.Equal(t, "two", got)
}
