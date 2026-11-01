package dream

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/taubyte/tau/node"
	"github.com/taubyte/tau/services/build/artifact"
	"github.com/taubyte/tau/services/build/backend"
	"github.com/taubyte/tau/services/build/deploy"
)

// uploads is a universe's door for a developer's build: `tau build` carries the tree
// and what it built, taken in as a commit of the configuration repository. From
// there it is a push. Three seams answer differently for an uploaded commit: the
// checkout is the unpacked tree, confirmation is that the universe holds it, and the
// engine hands back the developer's product for the recipe the universe planned. A
// product from any other recipe is not found. Only dream passes node.WithUploads and
// node.WithBuildSeams.
type uploads struct {
	root string

	mu       sync.Mutex
	byCommit map[string]upload
}

type upload struct {
	repository string
	ref        string
	bundle     artifact.Bundle
}

func newUploads() (*uploads, error) {
	root, err := os.MkdirTemp("", "tau-uploads-")
	if err != nil {
		return nil, err
	}
	return &uploads{root: root, byCommit: map[string]upload{}}, nil
}

func (u *uploads) Close() error { return os.RemoveAll(u.root) }

// Put is frontend.Uploads. The commit is the bundle's hash, so the same build
// uploaded twice is one commit, and one job.
func (u *uploads) Put(_ context.Context, _, repositoryKey, ref string, bundle io.Reader) (string, error) {
	dir, err := os.MkdirTemp(u.root, "upload-")
	if err != nil {
		return "", err
	}

	sum := sha1.New()
	read := io.TeeReader(bundle, sum)
	b, err := artifact.UnpackBundle(read, dir)
	if err == nil {
		_, err = io.Copy(io.Discard, read)
	}
	if err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	commit := hex.EncodeToString(sum.Sum(nil))

	u.mu.Lock()
	defer u.mu.Unlock()
	if _, held := u.byCommit[commit]; held {
		os.RemoveAll(dir)
		return commit, nil
	}
	u.byCommit[commit] = upload{repository: repositoryKey, ref: ref, bundle: b}
	return commit, nil
}

func (u *uploads) held(repositoryKey, commit string) (upload, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	up, ok := u.byCommit[commit]
	return up, ok && up.repository == repositoryKey
}

func (u *uploads) of(dir string) (upload, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, up := range u.byCommit {
		if up.bundle.Tree == dir {
			return up, true
		}
	}
	return upload{}, false
}

// layer is node.WithBuildSeams: the uploaded commits in front of the node's
// own clone and engine.
func (u *uploads) layer(s node.BuildSeams) node.BuildSeams {
	return node.BuildSeams{
		Checkout: uploadCheckout{u, s.Checkout},
		Confirm:  uploadConfirm{u, s.Confirm},
		Engine:   uploadEngine{u, s.Engine},
	}
}

type uploadCheckout struct {
	u     *uploads
	inner deploy.Checkout
}

// Checkout is the unpacked tree for an uploaded commit, released with the universe
// since a deploy and the build after it read the same tree.
func (c uploadCheckout) Checkout(ctx context.Context, repositoryKey, branch, commit string) (string, func(), error) {
	if up, ok := c.u.held(repositoryKey, commit); ok {
		return up.bundle.Tree, func() {}, nil
	}
	return c.inner.Checkout(ctx, repositoryKey, branch, commit)
}

type uploadConfirm struct {
	u     *uploads
	inner backend.Confirmer
}

// Confirm is the universe holding the commit, for one it was handed.
func (c uploadConfirm) Confirm(ctx context.Context, repositoryKey, ref, commit string) error {
	if up, ok := c.u.held(repositoryKey, commit); ok && up.ref == ref {
		return nil
	}
	return c.inner.Confirm(ctx, repositoryKey, ref, commit)
}

type uploadEngine struct {
	u     *uploads
	inner artifact.Engine
}

// Make is the developer's product for the recipe this universe planned, for a
// build of an uploaded tree, and the node's own engine for anything else.
func (e uploadEngine) Make(ctx context.Context, spec artifact.Spec, log io.Writer) (io.ReadCloser, error) {
	if up, ok := e.u.of(spec.Dir); ok {
		digest := spec.Digest()
		product, found := up.bundle.Product(digest)
		if !found {
			return nil, fmt.Errorf("dream: the upload carries nothing built from recipe %s for %q: it was built from a recipe other than the one this universe planned from the configuration it published", digest, spec.Path)
		}
		fmt.Fprintf(log, "taking the upload's product for recipe %s\n", digest)
		return product, nil
	}
	if e.inner == nil {
		return nil, fmt.Errorf("%w: this universe runs no containers, so it builds only what a developer's `tau build` carried in", artifact.ErrNoEngine)
	}
	return e.inner.Make(ctx, spec, log)
}
