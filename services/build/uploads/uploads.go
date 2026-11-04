// Package uploads is a development cloud's door for a developer's build: `tau build`
// carries the tree and what it built, kept as a commit of the repository it was
// uploaded for, in the directory the build service's configuration names. Both halves
// name the same directory, the frontend to keep an upload and the backend to read one,
// so it is the door of a cloud whose build runs on one machine.
//
// From there an upload is a push. Three seams answer differently for an uploaded
// commit: the checkout is the unpacked tree, confirmation is that the directory holds
// it at that ref, and the engine hands back the developer's product for the recipe the
// cloud planned. A product from any other recipe is not found.
package uploads

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/taubyte/tau/services/build/artifact"
	"github.com/taubyte/tau/services/build/deploy"
	"github.com/taubyte/tau/services/build/run"
)

const manifestFile = "upload.json"

var (
	commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Dir is the directory uploads are kept in.
type Dir struct{ root string }

// manifest is what an upload is a commit of.
type manifest struct {
	Repository string `json:"repository"`
	Ref        string `json:"ref"`
}

// Open is the uploads kept under root, made if it is not there.
func Open(root string) (*Dir, error) {
	if root == "" {
		return nil, errors.New("uploads: no directory")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, err
	}
	return &Dir{root: abs}, nil
}

// Put keeps a bundle as a commit of repositoryKey at ref. The commit is the bundle's
// hash, so the same build uploaded twice is one commit, and one job.
func (d *Dir) Put(repositoryKey, ref string, bundle io.Reader) (string, error) {
	staged, err := os.MkdirTemp(d.root, ".upload-")
	if err != nil {
		return "", err
	}
	kept := false
	defer func() {
		if !kept {
			os.RemoveAll(staged)
		}
	}()

	sum := sha1.New()
	read := io.TeeReader(bundle, sum)
	if _, err := artifact.UnpackBundle(read, staged); err != nil {
		return "", err
	}
	if _, err := io.Copy(io.Discard, read); err != nil {
		return "", err
	}
	commit := hex.EncodeToString(sum.Sum(nil))

	raw, err := json.Marshal(manifest{Repository: repositoryKey, Ref: ref})
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(staged, manifestFile), raw, 0o600); err != nil {
		return "", err
	}

	// A RENAME IS THE COMMIT, so two uploads of one build race to one directory and
	// the loser's copy is dropped.
	if err := os.Rename(staged, filepath.Join(d.root, commit)); err != nil {
		if _, held := d.held(commit); held {
			return commit, nil
		}
		return "", err
	}
	kept = true
	return commit, nil
}

// held is the manifest of an uploaded commit.
func (d *Dir) held(commit string) (manifest, bool) {
	if !commitPattern.MatchString(commit) {
		return manifest{}, false
	}
	raw, err := os.ReadFile(filepath.Join(d.root, commit, manifestFile))
	if err != nil {
		return manifest{}, false
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return manifest{}, false
	}
	return m, true
}

// tree is where an uploaded commit's tree is.
func (d *Dir) tree(commit string) string { return filepath.Join(d.root, commit, "tree") }

// of is the commit whose tree dir is, for an engine handed a checkout.
func (d *Dir) of(dir string) (string, bool) {
	rel, err := filepath.Rel(d.root, dir)
	if err != nil {
		return "", false
	}
	commit, rest := filepath.Split(rel)
	commit = filepath.Clean(commit)
	if rest != "tree" || !commitPattern.MatchString(commit) {
		return "", false
	}
	if _, ok := d.held(commit); !ok {
		return "", false
	}
	return commit, true
}

// product opens what an upload carries for a recipe digest.
func (d *Dir) product(commit, digest string) (io.ReadCloser, bool) {
	if !digestPattern.MatchString(digest) {
		return nil, false
	}
	f, err := os.Open(filepath.Join(d.root, commit, "products", digest))
	if err != nil {
		return nil, false
	}
	return f, true
}

// Checkout is the uploaded commits in front of inner.
func (d *Dir) Checkout(inner deploy.Checkout) deploy.Checkout { return checkout{d, inner} }

// Confirm is the uploaded commits in front of inner.
func (d *Dir) Confirm(inner run.Confirmer) run.Confirmer { return confirm{d, inner} }

// Engine is the uploaded products in front of inner, which is nil on a cloud that
// runs no containers.
func (d *Dir) Engine(inner artifact.Engine) artifact.Engine { return engine{d, inner} }

type checkout struct {
	d     *Dir
	inner deploy.Checkout
}

// Checkout is the unpacked tree for an uploaded commit, kept as long as the cloud
// keeps it, since a deploy and the build after it read the same tree.
func (c checkout) Checkout(ctx context.Context, repositoryKey, branch, commit string) (string, func(), error) {
	if m, ok := c.d.held(commit); ok && m.Repository == repositoryKey {
		return c.d.tree(commit), func() {}, nil
	}
	return c.inner.Checkout(ctx, repositoryKey, branch, commit)
}

type confirm struct {
	d     *Dir
	inner run.Confirmer
}

// Confirm is the directory holding the commit, for the repository and ref it was
// uploaded as.
func (c confirm) Confirm(ctx context.Context, repositoryKey, ref, commit string) error {
	if m, ok := c.d.held(commit); ok && m.Repository == repositoryKey && m.Ref == ref {
		return nil
	}
	return c.inner.Confirm(ctx, repositoryKey, ref, commit)
}

type engine struct {
	d     *Dir
	inner artifact.Engine
}

// Make is the developer's product for the recipe this cloud planned, for a build of
// an uploaded tree, and the cloud's own engine for anything else.
func (e engine) Make(ctx context.Context, spec artifact.Spec, log io.Writer) (io.ReadCloser, error) {
	if commit, ok := e.d.of(spec.Dir); ok {
		digest := spec.Digest()
		product, found := e.d.product(commit, digest)
		if !found {
			return nil, fmt.Errorf("uploads: the upload carries nothing built from recipe %s for %q: it was built from a recipe other than the one this cloud planned from the configuration it published", digest, spec.Path)
		}
		fmt.Fprintf(log, "taking the upload's product for recipe %s\n", digest)
		return product, nil
	}
	if e.inner == nil {
		return nil, fmt.Errorf("%w: this cloud runs no containers, so it builds only what a developer's `tau build` carried in", artifact.ErrNoEngine)
	}
	return e.inner.Make(ctx, spec, log)
}
