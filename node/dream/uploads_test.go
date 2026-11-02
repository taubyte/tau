//go:build dreaming

package dream

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node"
	"github.com/taubyte/tau/services/build/artifact"
)

type forgeClone struct{ checked, confirmed []string }

func (f *forgeClone) Checkout(_ context.Context, repo, _, commit string) (string, func(), error) {
	f.checked = append(f.checked, repo+"@"+commit)
	return "/from/the/forge", func() {}, nil
}

func (f *forgeClone) Confirm(_ context.Context, repo, _, commit string) error {
	f.confirmed = append(f.confirmed, repo+"@"+commit)
	return errors.New("the forge was asked")
}

type machine struct{ made []artifact.Spec }

func (m *machine) Make(_ context.Context, spec artifact.Spec, _ io.Writer) (io.ReadCloser, error) {
	m.made = append(m.made, spec)
	return io.NopCloser(strings.NewReader("built here")), nil
}

func bundleOf(t *testing.T, recipe artifact.Spec, product string) *bytes.Buffer {
	t.Helper()
	tree := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tree, "project.yaml"), []byte("id: x"), 0o644))
	out := filepath.Join(t.TempDir(), "product")
	require.NoError(t, os.WriteFile(out, []byte(product), 0o644))
	var b bytes.Buffer
	require.NoError(t, artifact.PackBundle(&b, tree, []string{"project.yaml"}, map[string]string{recipe.Digest(): out}))
	return &b
}

func TestAnUploadIsACommitTheUniverseHolds(t *testing.T) {
	u, err := newUploads()
	require.NoError(t, err)
	defer u.Close()

	recipe := artifact.Spec{Path: "services/hello", Steps: []artifact.Step{{Name: "build", Image: "rust:1.90", Commands: []string{"cargo component build"}}}, Output: "out.wasm"}
	commit, err := u.Put(t.Context(), "proj", "git.example/acme/shop", "refs/heads/main", bundleOf(t, recipe, "the developer's"))
	require.NoError(t, err)
	require.Len(t, commit, 40)

	again, err := u.Put(t.Context(), "proj", "git.example/acme/shop", "refs/heads/main", bundleOf(t, recipe, "the developer's"))
	require.NoError(t, err)
	require.Equal(t, commit, again, "the same build is the same commit")

	forge, own := &forgeClone{}, &machine{}
	s := u.layer(node.BuildSeams{Checkout: forge, Confirm: forge, Engine: own})

	require.NoError(t, s.Confirm.Confirm(t.Context(), "git.example/acme/shop", "refs/heads/main", commit))
	require.Error(t, s.Confirm.Confirm(t.Context(), "git.example/acme/shop", "refs/heads/other", commit), "only at the ref it was uploaded for")
	require.Error(t, s.Confirm.Confirm(t.Context(), "git.example/acme/else", "refs/heads/main", commit), "only for the repository it was uploaded as")

	dir, done, err := s.Checkout.Checkout(t.Context(), "git.example/acme/shop", "main", commit)
	require.NoError(t, err)
	done()
	require.FileExists(t, filepath.Join(dir, "project.yaml"))

	recipe.Dir = dir
	product, err := s.Engine.Make(t.Context(), recipe, io.Discard)
	require.NoError(t, err)
	body, err := io.ReadAll(product)
	product.Close()
	require.NoError(t, err)
	require.Equal(t, "the developer's", string(body))
	require.Empty(t, own.made, "nothing was built for an upload")

	// A RECIPE THE DEVELOPER DID NOT BUILD is not found, so an upload deploys
	// only what this universe would have built.
	other := recipe
	other.Steps = []artifact.Step{{Name: "build", Image: "rust:1.90", Commands: []string{"cargo component build --release"}}}
	_, err = s.Engine.Make(t.Context(), other, io.Discard)
	require.ErrorContains(t, err, "recipe other than the one this universe planned")

	// Anything else is the forge's and the node's own.
	dir, _, err = s.Checkout.Checkout(t.Context(), "git.example/acme/shop", "main", strings.Repeat("1", 40))
	require.NoError(t, err)
	require.Equal(t, "/from/the/forge", dir)
	require.Error(t, s.Confirm.Confirm(t.Context(), "git.example/acme/shop", "refs/heads/main", strings.Repeat("1", 40)))
	_, err = s.Engine.Make(t.Context(), artifact.Spec{Dir: "/from/the/forge"}, io.Discard)
	require.NoError(t, err)
	require.Len(t, own.made, 1)
	require.Len(t, forge.checked, 1)
}

func TestAUniverseWithNoContainersDeclinesWhatWasNotUploaded(t *testing.T) {
	u, err := newUploads()
	require.NoError(t, err)
	defer u.Close()

	s := u.layer(node.BuildSeams{Checkout: &forgeClone{}, Confirm: &forgeClone{}})
	_, err = s.Engine.Make(t.Context(), artifact.Spec{Dir: "/from/the/forge"}, io.Discard)
	require.ErrorIs(t, err, artifact.ErrNoEngine)
}

func TestABundleThatIsNotOneIsNoCommit(t *testing.T) {
	u, err := newUploads()
	require.NoError(t, err)
	defer u.Close()

	_, err = u.Put(t.Context(), "proj", "git.example/acme/shop", "refs/heads/main", strings.NewReader("not a bundle"))
	require.ErrorIs(t, err, artifact.ErrBadBundle)
	entries, err := os.ReadDir(u.root)
	require.NoError(t, err)
	require.Empty(t, entries, "nothing of it is kept")
}

func TestBuiltFallsBackToTheBuildService(t *testing.T) {
	b := &Built{fallback: artifactsFunc(func(_ context.Context, project, _, _, resource string) (string, error) {
		if resource == "QmBuilt" {
			return "bafybuilt", nil
		}
		return "", errors.New("nothing has been built for " + resource)
	})}
	b.Publish("shop", "prod", map[string]string{"QmStaged": "bafystaged"})

	cid, err := b.Artifact(t.Context(), "shop", "prod", "function", "QmStaged")
	require.NoError(t, err)
	require.Equal(t, "bafystaged", cid)

	cid, err = b.Artifact(t.Context(), "shop", "prod", "function", "QmBuilt")
	require.NoError(t, err)
	require.Equal(t, "bafybuilt", cid)

	_, err = b.Artifact(t.Context(), "shop", "prod", "function", "QmNeither")
	require.ErrorContains(t, err, "nothing was staged")
	require.ErrorContains(t, err, "nothing has been built for QmNeither")
}

type artifactsFunc func(ctx context.Context, project, environment, kind, resource string) (string, error)

func (f artifactsFunc) Artifact(ctx context.Context, project, environment, kind, resource string) (string, error) {
	return f(ctx, project, environment, kind, resource)
}
