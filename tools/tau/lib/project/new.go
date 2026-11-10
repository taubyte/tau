package projectLib

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/pterm/pterm"
	httpClient "github.com/taubyte/tau/clients/http/auth"
	authClient "github.com/taubyte/tau/tools/tau/clients/auth_client"
	"github.com/taubyte/tau/tools/tau/common"
	"github.com/taubyte/tau/tools/tau/config"
	projectI18n "github.com/taubyte/tau/tools/tau/i18n/project"
	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/shared"
	"github.com/taubyte/tau/tools/tau/prompts/spinner"
	"github.com/taubyte/tau/tools/tau/session"
)

/*
New creates a new project. It creates the config and code repositories
and registers them with the auth server. It then creates a project on
the auth server and sets the current project to the new project.
After that it will clone the project then push the id, description, and
email to the config.yaml file. The project is selected only once all that
worked. Any failure can be finished by running the same command again.

Args:

	ctx: The cli context
	p: The project to create
	location: The location to clone the project to. If not specified, it
		clones to the current directory.
	embedToken: Whether to embed the auth token in the git config
*/
func New(p *Project, location string, embedToken bool) error {
	user, err := config.GetSelectedUser()
	if err != nil {
		return err
	}

	stopGlobe := spinner.Globe()

	// capture pterm output momentarily so that the globe doesn't go everywhere
	buf := bytes.NewBuffer(nil)
	pterm.SetDefaultOutput(buf)
	defer func() {
		stopGlobe()

		// Return pterm output to normal
		pterm.SetDefaultOutput(os.Stdout)

		// Read all from buf and display
		pterm.Println(buf.String())
	}()

	client, err := authClient.Load()
	if err != nil {
		return singletonsI18n.LoadingAuthClientFailed(err)
	}

	clientProject := p.Resume
	if clientProject == nil {
		if clientProject, err = createProject(client, p); err != nil {
			return projectI18n.ProjectNotFinished(p.Name, err)
		}
	} else if _, err := config.Projects().Get(clientProject.Name); err == nil {
		// The failed run's entry would stop the clone as "already cloned".
		if err := config.Projects().Delete(clientProject.Name); err != nil {
			return err
		}
	}

	if err = cloneProjectAndPushConfig(clientProject, location, p.Description, user, embedToken, p.Account, p.Plan); err != nil {
		return projectI18n.ProjectNotFinished(p.Name, err)
	}

	return session.Set().SelectedProject(clientProject.Name)
}

// createProject creates the repositories and registers the project. Each step
// is safe to redo after an interrupted run.
func createProject(client *httpClient.Client, p *Project) (*httpClient.Project, error) {
	private := !p.Public

	// Create config repository
	config_name := fmt.Sprintf(common.ConfigRepoPrefix, p.Name)
	code_name := fmt.Sprintf(common.CodeRepoPrefix, p.Name)
	config_id, err := createOrAdoptRepository(client, config_name, p.Description, private)
	if errors.Is(err, projectI18n.ErrorNameTaken) {
		return nil, projectI18n.ProjectNameTaken(p.Name, withCommits(client, config_name, code_name))
	}
	if err != nil {
		return nil, projectI18n.ConfigRepoCreateFailed(err)
	}

	// Create code repository
	code_id, err := createOrAdoptRepository(client, code_name, p.Description, private)
	if errors.Is(err, projectI18n.ErrorNameTaken) {
		return nil, projectI18n.ProjectNameTaken(p.Name, withCommits(client, config_name, code_name))
	}
	if err != nil {
		return nil, projectI18n.CodeRepoCreateFailed(err)
	}

	// Register config repository
	err = client.RegisterRepository(config_id)
	if err != nil {
		return nil, projectI18n.ConfigRepoRegisterFailed(err)
	}

	// Register code repository
	err = client.RegisterRepository(code_id)
	if err != nil {
		return nil, projectI18n.CodeRepoRegisterFailed(err)
	}

	// Create project
	clientProject := &httpClient.Project{
		Name: p.Name,
	}
	if err = clientProject.Create(client, config_id, code_id); err != nil {
		return nil, projectI18n.CreatingProjectFailed(err)
	}

	return clientProject, nil
}

// withCommits keeps the named repositories that hold commits.
func withCommits(client *httpClient.Client, names ...string) []string {
	return slices.DeleteFunc(names, func(name string) bool { return !client.RepositoryHasCommits(name) })
}

// createOrAdoptRepository creates a repository, or uses one of that name with
// no commits. One with commits is other work: it is left alone, and the error
// is projectI18n.ErrorNameTaken, as running again cannot change that.
func createOrAdoptRepository(client *httpClient.Client, name, description string, private bool) (string, error) {
	id, err := CreateRepository(client, name, description, private)
	if err != nil {
		if emptyID := client.EmptyRepository(name); emptyID != "" {
			pterm.Info.Printfln("Using the existing empty repository `%s`", name)
			return emptyID, nil
		}
		if client.RepositoryHasCommits(name) {
			return "", projectI18n.ErrorNameTaken
		}
	}
	return id, err
}
