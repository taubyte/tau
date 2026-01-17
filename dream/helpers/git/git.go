package helpers

import (
	"context"
	"strings"

	"github.com/taubyte/tau/dream/helpers"
	"github.com/taubyte/tau/pkg/git"
)

func CloneToDir(ctx context.Context, dir string, _repo helpers.Repository) (err error) {
	// Only used in testing
	cloneURL := _repo.HookInfo.Repository.SSHURL
	if strings.HasPrefix(cloneURL, "git@") {
		cloneURL = git.ConvertSSHToHTTPS(cloneURL)
	}

	options := []git.Option{
		git.URL(cloneURL),
		git.Root(dir),
	}
	if _repo.HookInfo.Repository.Branch != "" {
		options = append(options, git.Branch(_repo.HookInfo.Repository.Branch))
	}

	_, err = git.New(ctx, options...)
	if err != nil {
		return err
	}

	return err
}
