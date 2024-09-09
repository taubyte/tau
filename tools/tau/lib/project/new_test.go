package projectLib_test

import (
	"os"
	"strings"
	"testing"

	"github.com/pterm/pterm"
	httpClient "github.com/taubyte/tau/clients/http/auth"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	authClient "github.com/taubyte/tau/tools/tau/singletons/auth_client"
	"github.com/taubyte/tau/tools/tau/singletons/config"
	"github.com/taubyte/tau/tools/tau/states"
	"gotest.tools/v3/assert"
)

func unregisterAndDeleteRepository(client *httpClient.Client, fullname string) error {
	nameSplit := strings.Split(fullname, "/")
	user := nameSplit[0]
	name := nameSplit[1]

	config, err := client.GetRepositoryByName(fullname)
	if err != nil {
		return err
	}

	err = client.UnregisterRepository(config.Get().ID())
	if err != nil {
		return err
	}

	githubClient, err := client.Git().GithubTODO()
	if err != nil {
		return err
	}

	response, err := githubClient.Repositories.Delete(states.Context, user, name)
	if err != nil {
		if response != nil {
			pterm.Info.Printfln("Github response: %#v", *response)
		}
		return err
	}

	return nil
}

func TestNew(t *testing.T) {
	t.Skip("Heavy test, uses current login which needs to have a delete key")
	var (
		projectName = "tau_test_new_project"
		description = "a test project for tau"
		cloneDir    = "_assets"
		user        *httpClient.UserData
		project     *httpClient.Project
		repoData    *httpClient.RawRepoDataOuter
	)

	// Try to remove
	os.RemoveAll(cloneDir)

	// Cleanup
	defer func() {
		_client, err := authClient.Load()
		assert.NilError(t, err)

		err = unregisterAndDeleteRepository(_client, repoData.Configuration.Fullname)
		assert.NilError(t, err)

		err = unregisterAndDeleteRepository(_client, repoData.Code.Fullname)
		assert.NilError(t, err)

		_, err = project.Delete()
		assert.NilError(t, err)

		err = os.RemoveAll(cloneDir)
		assert.NilError(t, err)

		err = config.Projects().Delete(projectName)
		assert.NilError(t, err)
	}()

	err := projectLib.New(&projectLib.Project{
		Name:        projectName,
		Description: description,
		Public:      false,
	}, cloneDir, false)
	assert.NilError(t, err)

	client, err := authClient.Load()
	assert.NilError(t, err)

	user, err = client.User().Get()
	assert.NilError(t, err)

	projects, err := client.Projects()
	assert.NilError(t, err)

	for _, _project := range projects {
		if _project.Name == projectName {
			project = _project
			break
		}
	}

	repoData, err = project.Repositories()
	assert.NilError(t, err)

	// Confirm config read from github is accurate
	config, err := client.Git().ReadConfig(user.Login, repoData.Configuration.Name)
	assert.NilError(t, err)

	assert.Equal(t, config.Name, projectName)
	assert.Equal(t, config.Description, description)
	assert.Equal(t, config.Notification.Email, user.Email)
}
