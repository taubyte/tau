package project

import (
	"context"
	"encoding/json"
	"io"
	"sync"

	"github.com/google/go-github/v53/github"
	"github.com/pterm/pterm"
	"github.com/taubyte/tau/pkg/git"
	projectFlags "github.com/taubyte/tau/tools/tau/flags/project"
	"github.com/taubyte/tau/tools/tau/i18n"
	projectI18n "github.com/taubyte/tau/tools/tau/i18n/project"
	repositoryI18n "github.com/taubyte/tau/tools/tau/i18n/repository"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	"github.com/taubyte/tau/tools/tau/singletons/config"
	"github.com/urfave/cli/v2"
	"golang.org/x/oauth2"
)

// See if they have cloned the project, if not show help
func checkProjectClonedHelp(name string) {
	project, err := config.Projects().Get(name)
	if err != nil || len(project.Location) == 0 {
		i18n.Help().BeSureToCloneProject()
	}
}

type dualRepoHandler struct {
	ctx         *cli.Context
	projectName string
	repository  projectLib.ProjectRepository
	action      func(*git.Repository) error
	errorFormat func(string) error
}

// Run will parse for config-only || code-only
// then Runs a go routine to commit the action on both
// config and code repositories asynchronously or run config/code only
func (h *dualRepoHandler) Run() error {
	config, code, err := projectFlags.ParseConfigCodeFlags(h.ctx)
	if err != nil {
		return err
	}

	var (
		configErr error
		codeErr   error
	)

	var wg sync.WaitGroup

	if config {
		wg.Add(1)
		go func() {
			defer wg.Done()

			var configRepo *git.Repository

			configRepo, configErr = h.repository.Config()
			if configErr != nil {
				return
			}

			configErr = h.action(configRepo)
			if configErr != nil {
				pterm.Error.Printfln(projectI18n.ConfigRepo, configErr)
			}
		}()
	}

	if code {
		wg.Add(1)
		go func() {
			defer wg.Done()

			var codeRepo *git.Repository

			codeRepo, codeErr = h.repository.Code()
			if codeErr != nil {
				return
			}

			codeErr = h.action(codeRepo)
			if codeErr != nil {
				pterm.Error.Printfln(projectI18n.CodeRepo, codeErr)
			}
		}()
	}

	wg.Wait()
	if configErr != nil || codeErr != nil {
		return h.errorFormat(h.projectName)
	}

	return nil
}

func newGithubClient(ctx context.Context, token string) *github.Client {
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)

	tc := oauth2.NewClient(ctx, ts)
	return github.NewClient(tc)
}

// Var to allow override in tests
var ListRepos = func(ctx context.Context, token, user string) ([]*github.Repository, error) {
	client := newGithubClient(ctx, token)
	repos, _, err := client.Repositories.List(ctx, "", &github.RepositoryListOptions{
		Visibility:  "all",
		Affiliation: "owner,collaborator,organization_member",
	})
	if err != nil {
		return nil, repositoryI18n.ErrorListRepositories(user, err)
	}

	return repos, nil
}

func removeFromGithub(ctx context.Context, token, user, name string) error {
	client := newGithubClient(ctx, token)
	if res, err := client.Repositories.Delete(ctx, user, name); err != nil {
		var deleteRes deleteRes
		data, err := io.ReadAll(res.Body)
		if err == nil {
			if err = json.Unmarshal(data, &deleteRes); err == nil && deleteRes.Message == adminRights {
				pterm.Error.Println(adminRights[:len(adminRights)-1] + " to delete")
				pterm.Info.Println(
					"Add token with delete permissions\n" +
						pterm.FgGreen.Sprint("$ tau login --new -n {profile-name} -p github -d -t {token}"))
				return repositoryI18n.ErrorAdminRights
			}

		}
		return repositoryI18n.ErrorDeleteRepository(name, err)
	}

	return nil
}

type deleteRes struct {
	Message string `json:"message"`
}

var adminRights = "Must have admin rights to Repository."
