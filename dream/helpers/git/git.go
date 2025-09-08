package helpers

import (
	"context"

	"github.com/taubyte/tau/dream/helpers"
	"github.com/taubyte/tau/pkg/git"
)

func CloneToDir(ctx context.Context, dir string, _repo helpers.Repository) (err error) {
	options := []git.Option{
		git.URL(_repo.HookInfo.Repository.SSHURL),
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
