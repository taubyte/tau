package uploads

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

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

func open(t *testing.T) *Dir {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "uploads"))
	require.NoError(t, err)
	return d
}

func TestAnUploadIsACommitTheDirectoryHolds(t *testing.T) {
	d := open(t)

	recipe := artifact.Spec{Path: "services/hello", Steps: []artifact.Step{{Name: "build", Image: "rust:1.90", Commands: []string{"cargo component build"}}}, Output: "out.wasm"}
	commit, err := d.Put("git.example/acme/shop", "refs/heads/main", bundleOf(t, recipe, "the developer's"))
	require.NoError(t, err)
	require.Len(t, commit, 40)

	again, err := d.Put("git.example/acme/shop", "refs/heads/main", bundleOf(t, recipe, "the developer's"))
	require.NoError(t, err)
	require.Equal(t, commit, again, "the same build is the same commit")

	forge, own := &forgeClone{}, &machine{}
	checkout, confirm, engine := d.Checkout(forge), d.Confirm(forge), d.Engine(own)

	require.NoError(t, confirm.Confirm(t.Context(), "git.example/acme/shop", "refs/heads/main", commit))
	require.Error(t, confirm.Confirm(t.Context(), "git.example/acme/shop", "refs/heads/other", commit), "only at the ref it was uploaded for")
	require.Error(t, confirm.Confirm(t.Context(), "git.example/acme/else", "refs/heads/main", commit), "only for the repository it was uploaded as")

	dir, done, err := checkout.Checkout(t.Context(), "git.example/acme/shop", "main", commit)
	require.NoError(t, err)
	done()
	require.FileExists(t, filepath.Join(dir, "project.yaml"))

	recipe.Dir = dir
	product, err := engine.Make(t.Context(), recipe, io.Discard)
	require.NoError(t, err)
	body, err := io.ReadAll(product)
	product.Close()
	require.NoError(t, err)
	require.Equal(t, "the developer's", string(body))
	require.Empty(t, own.made, "nothing was built for an upload")

	// A RECIPE THE DEVELOPER DID NOT BUILD is not found, so an upload deploys
	// only what this cloud would have built.
	other := recipe
	other.Steps = []artifact.Step{{Name: "build", Image: "rust:1.90", Commands: []string{"cargo component build --release"}}}
	_, err = engine.Make(t.Context(), other, io.Discard)
	require.ErrorContains(t, err, "recipe other than the one this cloud planned")

	// Anything else is the forge's and the node's own.
	dir, _, err = checkout.Checkout(t.Context(), "git.example/acme/shop", "main", strings.Repeat("1", 40))
	require.NoError(t, err)
	require.Equal(t, "/from/the/forge", dir)
	_, _, err = checkout.Checkout(t.Context(), "git.example/acme/else", "main", commit)
	require.NoError(t, err)
	require.Error(t, confirm.Confirm(t.Context(), "git.example/acme/shop", "refs/heads/main", strings.Repeat("1", 40)))
	_, err = engine.Make(t.Context(), artifact.Spec{Dir: "/from/the/forge"}, io.Discard)
	require.NoError(t, err)
	require.Len(t, own.made, 1)
	require.Len(t, forge.checked, 2)

	// A SECOND DIRECTORY OVER THE SAME ROOT is the other half of the build reading
	// what the first kept.
	reader, err := Open(d.root)
	require.NoError(t, err)
	require.NoError(t, reader.Confirm(forge).Confirm(t.Context(), "git.example/acme/shop", "refs/heads/main", commit))
}

func TestTwoUploadsOfOneBuildAreOneCommit(t *testing.T) {
	d := open(t)
	recipe := artifact.Spec{Path: "p", Output: "o"}
	var raw bytes.Buffer
	_, err := raw.ReadFrom(bundleOf(t, recipe, "same"))
	require.NoError(t, err)

	var wg sync.WaitGroup
	commits := make([]string, 8)
	errs := make([]error, 8)
	for i := range commits {
		wg.Go(func() {
			commits[i], errs[i] = d.Put("git.example/acme/shop", "refs/heads/main", bytes.NewReader(raw.Bytes()))
		})
	}
	wg.Wait()
	for i := range commits {
		require.NoError(t, errs[i])
		require.Equal(t, commits[0], commits[i])
	}
	entries, err := os.ReadDir(d.root)
	require.NoError(t, err)
	require.Len(t, entries, 1, "one commit kept, no staged copy left")
}

func TestACloudWithNoContainersDeclinesWhatWasNotUploaded(t *testing.T) {
	d := open(t)
	_, err := d.Engine(nil).Make(t.Context(), artifact.Spec{Dir: "/from/the/forge"}, io.Discard)
	require.ErrorIs(t, err, artifact.ErrNoEngine)
}

func TestABundleThatIsNotOneIsNoCommit(t *testing.T) {
	d := open(t)

	_, err := d.Put("git.example/acme/shop", "refs/heads/main", strings.NewReader("not a bundle"))
	require.ErrorIs(t, err, artifact.ErrBadBundle)
	entries, err := os.ReadDir(d.root)
	require.NoError(t, err)
	require.Empty(t, entries, "nothing of it is kept")
}

func TestWhatIsNotAnUploadIsNotReadAsOne(t *testing.T) {
	d := open(t)
	for _, commit := range []string{"", "../../etc", strings.Repeat("g", 40), strings.Repeat("1", 40)} {
		_, ok := d.held(commit)
		require.False(t, ok, commit)
	}
	for _, dir := range []string{"/elsewhere", d.root, filepath.Join(d.root, strings.Repeat("1", 40), "tree"), filepath.Join(d.root, "x", "tree")} {
		_, ok := d.of(dir)
		require.False(t, ok, dir)
	}
	_, ok := d.product(strings.Repeat("1", 40), "../../x")
	require.False(t, ok)

	// A manifest that does not decode is no upload.
	commit := strings.Repeat("a", 40)
	require.NoError(t, os.MkdirAll(filepath.Join(d.root, commit), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(d.root, commit, manifestFile), []byte("{"), 0o600))
	_, ok = d.held(commit)
	require.False(t, ok)
}

func TestOpenNeedsADirectoryItCanMake(t *testing.T) {
	_, err := Open("")
	require.Error(t, err)

	file := filepath.Join(t.TempDir(), "a file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	_, err = Open(filepath.Join(file, "below"))
	require.Error(t, err)

}

func TestAnUploadsProductsAreNamedByTheRecipesACloudWorksOut(t *testing.T) {
	const basic = "../../../pkg/tcc/taubyte/v2/fixtures/basic"
	recipes, err := Recipes(basic, "main")
	require.NoError(t, err)
	require.Contains(t, recipes, "prod", "the environment the branch feeds")
	require.NotEmpty(t, recipes["prod"])
	for name, digest := range recipes["prod"] {
		require.Regexp(t, digestPattern, digest, "package/%s", name)
	}
	again, err := Recipes(basic, "main")
	require.NoError(t, err)
	require.Equal(t, recipes, again, "one tree is one set of recipes")

	_, err = Recipes(t.TempDir(), "main")
	require.Error(t, err, "a tree with no project builds nothing")
}
