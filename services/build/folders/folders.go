// Package folders is a development cloud reading repositories from folders on its
// machine, named by repository key in the build service's configuration: each is
// cloned and confirmed from its folder by the same clone a node clones a forge with,
// and every other key falls through to the forge.
package folders

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/services/build/deploy"
	"github.com/taubyte/tau/services/build/run"
)

// Folders is the repositories read from folders.
type Folders struct {
	clone  *deploy.Clone
	folder map[string]string
}

// New is folders by repository key.
func New(folders map[string]string) (*Folders, error) {
	f := &Folders{folder: make(map[string]string, len(folders))}
	for key, dir := range folders {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return nil, err
		}
		f.folder[key] = abs
	}
	f.clone = deploy.NewClone(nil, deploy.WithAddress(func(key string, over forge.Protocol) (forge.Address, error) {
		dir, ok := f.folder[key]
		if !ok || over != forge.HTTPS {
			return forge.Address{}, fmt.Errorf("folders: %s is not a local folder", key)
		}
		return forge.Address{URL: "file://" + dir}, nil
	}))
	return f, nil
}

// Checkout is the folders in front of inner.
func (f *Folders) Checkout(inner deploy.Checkout) deploy.Checkout { return checkout{f, inner} }

// Confirm is the folders in front of inner.
func (f *Folders) Confirm(inner run.Confirmer) run.Confirmer { return confirm{f, inner} }

type checkout struct {
	f     *Folders
	inner deploy.Checkout
}

func (c checkout) Checkout(ctx context.Context, repositoryKey, branch, commit string) (string, func(), error) {
	if _, ok := c.f.folder[repositoryKey]; ok {
		return c.f.clone.Checkout(ctx, repositoryKey, branch, commit)
	}
	return c.inner.Checkout(ctx, repositoryKey, branch, commit)
}

type confirm struct {
	f     *Folders
	inner run.Confirmer
}

func (c confirm) Confirm(ctx context.Context, repositoryKey, ref, commit string) error {
	if _, ok := c.f.folder[repositoryKey]; ok {
		return c.f.clone.Confirm(ctx, repositoryKey, ref, commit)
	}
	return c.inner.Confirm(ctx, repositoryKey, ref, commit)
}
