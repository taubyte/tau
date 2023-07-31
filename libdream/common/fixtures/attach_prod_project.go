package fixtures

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/go-github/github"
	httpAuthClient "github.com/taubyte/tau/clients/http/auth"
	commonDreamland "github.com/taubyte/tau/libdream/common"
	"github.com/taubyte/tau/libdream/helpers"
	dreamlandRegistry "github.com/taubyte/tau/libdream/registry"
	commonAuth "github.com/taubyte/tau/protocols/common"
	"golang.org/x/oauth2"
)

func init() {
	dreamlandRegistry.Fixture("attachProdProject", attachProdProject)
}

// Added this variable so that import can call the attachProdProjectFixture, without having
// to rewrite code
var SharedRepositoryData *httpAuthClient.RawRepoDataOuter

func attachProdProject(u commonDreamland.Universe, params ...interface{}) error {
	if len(params) < 2 {
		return errors.New("attachProdProject expects 2 parameters [project-id] [git-token]")
	}

	projectId := params[0].(string)
	if len(projectId) > 0 {
		helpers.ProjectID = projectId
	}

	gitToken := params[1].(string)
	if len(gitToken) > 0 {
		helpers.GitToken = gitToken
	}

	prodAuthURL := "https://auth.taubyte.com"
	prodClient, err := httpAuthClient.New(u.Context(), httpAuthClient.URL(prodAuthURL), httpAuthClient.Auth(gitToken), httpAuthClient.Unsecure(), httpAuthClient.Provider(helpers.GitProvider))
	if err != nil {
		return fmt.Errorf("creating new auth client failed with: %w", err)
	}

	project, err := prodClient.GetProjectById(projectId)
	if err != nil {
		return fmt.Errorf("getting project `%s` failed with: %w", projectId, err)
	}

	SharedRepositoryData, err = project.Repositories()
	if err != nil {
		return fmt.Errorf("getting repository data failed with: %w", err)
	}

	// Override auth method so that projectID is not changed
	commonAuth.GetNewProjectID = func(args ...interface{}) string {
		return projectId
	}

	SharedRepositoryData.Configuration.Id, err = GetRepoId(u.Context(), SharedRepositoryData.Configuration.Fullname, gitToken)
	if err != nil {
		return fmt.Errorf("getting config repo Id failed with: %w", err)
	}

	SharedRepositoryData.Code.Id, err = GetRepoId(u.Context(), SharedRepositoryData.Code.Fullname, gitToken)
	if err != nil {
		return fmt.Errorf("getting code repo Id failed with: %w", err)
	}

	devAuthUrl, err := u.GetURLHttp(u.Auth().Node())
	if err != nil {
		return fmt.Errorf("getting auth url failed with: %w", err)
	}

	devClient, err := httpAuthClient.New(u.Context(), httpAuthClient.URL(devAuthUrl), httpAuthClient.Auth(gitToken), httpAuthClient.Provider(helpers.GitProvider))
	if err != nil {
		return fmt.Errorf("creating new http auth client failed with %w", err)
	}

	if err = devClient.RegisterRepository(SharedRepositoryData.Configuration.Id); err != nil {
		return fmt.Errorf("registering config repo failed with: %w", err)
	}

	if err = devClient.RegisterRepository(SharedRepositoryData.Code.Id); err != nil {
		return fmt.Errorf("registering code repo failed with: %w", err)
	}

	if err = project.Create(devClient, SharedRepositoryData.Configuration.Id, SharedRepositoryData.Code.Id); err != nil {
		return fmt.Errorf("creating project failed with: %w", err)
	}

	return nil
}

func GetRepoId(ctx context.Context, repoFullName string, token string) (string, error) {
	gitClient := newGithubClient(ctx, token)
	if len(repoFullName) == 0 {
		return "", errors.New("repo not found")
	}

	repoFullnameSplit := strings.Split(repoFullName, "/")
	repo, resp, err := gitClient.Repositories.Get(ctx, repoFullnameSplit[0], repoFullnameSplit[1])
	if err != nil {
		body, err0 := io.ReadAll(resp.Body)
		defer resp.Body.Close()
		if err0 != nil {
			return "", fmt.Errorf("calling github api failed with: [%v & %v]", err, err0)
		}
		return "", fmt.Errorf("getting github repo failed with %v, got response %s", err, string(body))
	}

	return fmt.Sprintf("%d", repo.GetID()), nil
}

var gitClient *github.Client

func newGithubClient(ctx context.Context, token string) *github.Client {
	if gitClient != nil {
		return gitClient
	}
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)
	gitClient = github.NewClient(tc)
	return gitClient
}
