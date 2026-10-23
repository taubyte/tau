package dream

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/taubyte/tau/node"
	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/services/build/backend"
	"github.com/taubyte/tau/services/build/deploy"
)

// local is a universe reading repositories from folders on this machine: a
// repository key named in Config.Local is cloned and confirmed from its folder,
// by the same Clone a node clones a forge with, and every other key falls
// through to the node's own.
type local struct {
	clone  *deploy.Clone
	folder map[string]string
}

func newLocal(folders map[string]string) (*local, error) {
	l := &local{folder: make(map[string]string, len(folders))}
	for key, dir := range folders {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return nil, err
		}
		l.folder[key] = abs
	}
	l.clone = deploy.NewClone(nil, deploy.WithAddress(func(key string, over forge.Protocol) (forge.Address, error) {
		dir, ok := l.folder[key]
		if !ok || over != forge.HTTPS {
			return forge.Address{}, fmt.Errorf("dream: %s is not a local folder", key)
		}
		return forge.Address{URL: "file://" + dir}, nil
	}))
	return l, nil
}

func (l *local) layer(s node.BuildSeams) node.BuildSeams {
	if len(l.folder) == 0 {
		return s
	}
	s.Checkout = localCheckout{l, s.Checkout}
	s.Confirm = localConfirm{l, s.Confirm}
	return s
}

type localCheckout struct {
	l     *local
	inner deploy.Checkout
}

func (c localCheckout) Checkout(ctx context.Context, repositoryKey, branch, commit string) (string, func(), error) {
	if _, ok := c.l.folder[repositoryKey]; ok {
		return c.l.clone.Checkout(ctx, repositoryKey, branch, commit)
	}
	return c.inner.Checkout(ctx, repositoryKey, branch, commit)
}

type localConfirm struct {
	l     *local
	inner backend.Confirmer
}

func (c localConfirm) Confirm(ctx context.Context, repositoryKey, ref, commit string) error {
	if _, ok := c.l.folder[repositoryKey]; ok {
		return c.l.clone.Confirm(ctx, repositoryKey, ref, commit)
	}
	return c.inner.Confirm(ctx, repositoryKey, ref, commit)
}
