package git

import (
	"context"
	"errors"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage/memory"
)

// ListRemote is the remote's references, read the way a clone with the same
// options would reach it and with nothing fetched.
func ListRemote(ctx context.Context, options ...Option) ([]*plumbing.Reference, error) {
	c := &Repository{ctx: ctx}
	for _, opt := range options {
		if err := opt(c); err != nil {
			return nil, err
		}
	}
	if c.url == "" {
		return nil, errors.New("listing a remote needs its url")
	}
	remote := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{Name: git.DefaultRemoteName, URLs: []string{c.url}})
	return remote.ListContext(ctx, &git.ListOptions{Auth: c.auth})
}
