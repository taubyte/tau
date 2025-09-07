package helpers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/go-github/v71/github"
	"github.com/taubyte/tau/dream/helpers"
	"github.com/taubyte/tau/pkg/git"
)

func CloneToDirSSH(ctx context.Context, dir string, _repo helpers.Repository) (err error) {
	githubClient := github.NewClient(http.DefaultClient)

	gitOptions := []git.Option{
		git.URL(_repo.HookInfo.Repository.SSHURL),
		git.Root(dir),
	}

	if _repo.HookInfo.Repository.Branch != "" {
		gitOptions = append(gitOptions, git.Branch(_repo.HookInfo.Repository.Branch))
	}

	// clone repo
	_, err = git.New(ctx, gitOptions...)
	if err != nil {
		return
	}

	repo, _, err := githubClient.Repositories.Get(ctx, helpers.GitUser, _repo.Name)
	if err != nil {
		return
	}
	if repo.ID == nil {
		err = fmt.Errorf("repo ID not found")
		return
	}
	return
}
