package projectLib

import (
	"bytes"
	"fmt"
	"os"

	"github.com/pterm/pterm"
	httpClient "github.com/taubyte/tau/clients/http/auth"
	"github.com/taubyte/tau/tools/tau/common"
	"github.com/taubyte/tau/tools/tau/env"
	projectI18n "github.com/taubyte/tau/tools/tau/i18n/project"
	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/singletons"
	"github.com/taubyte/tau/tools/tau/prompts/spinner"
	authClient "github.com/taubyte/tau/tools/tau/singletons/auth_client"
	"github.com/taubyte/tau/tools/tau/singletons/session"
)

/*
New creates a new project. It creates the config and code repositories
and registers them with the auth server. It then creates a project on
the auth server and sets the current project to the new project.
After that it will clone the project then push the id, description, and
email to the config.yaml file.

Args:

	ctx: The cli context
	p: The project to create
	location: The location to clone the project to. If not specified, it
		clones to the current directory.
	embedToken: Whether to embed the auth token in the git config
*/
func New(p *Project, location string, embedToken bool) error {
	user, err := env.GetSelectedUser()
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

	private := !p.Public

	client, err := authClient.Load()
	if err != nil {
		return singletonsI18n.LoadingAuthClientFailed(err)
	}

	// Create config repository
	config_name := fmt.Sprintf(common.ConfigRepoPrefix, p.Name)
	config_id, err := CreateRepository(client, config_name, p.Description, private)
	if err != nil {
		return projectI18n.ConfigRepoCreateFailed(err)
	}

	// Create code repository
	code_name := fmt.Sprintf(common.CodeRepoPrefix, p.Name)
	code_id, err := CreateRepository(client, code_name, p.Description, private)
	if err != nil {
		return projectI18n.CodeRepoCreateFailed(err)
	}

	// Register config repository
	err = client.RegisterRepository(config_id)
	if err != nil {
		return projectI18n.ConfigRepoRegisterFailed(err)
	}

	// Register code repository
	err = client.RegisterRepository(code_id)
	if err != nil {
		return projectI18n.CodeRepoRegisterFailed(err)
	}

	// Create project
	clientProject := &httpClient.Project{
		Name: p.Name,
	}
	err = clientProject.Create(client, config_id, code_id)
	if err != nil {
		return projectI18n.CreatingProjectFailed(err)
	}

	// Select created project
	err = session.Set().SelectedProject(clientProject.Name)
	if err != nil {
		return err
	}

	return cloneProjectAndPushConfig(clientProject, location, p.Description, user, embedToken)
}
