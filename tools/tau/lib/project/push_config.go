//go:build !localAuthClient

package projectLib

import (
	"fmt"
	"os"
	"path"
	"strings"

	httpClient "github.com/taubyte/tau/clients/http/auth"
	"github.com/taubyte/tau/pkg/schema/project"
	"github.com/taubyte/tau/tools/tau/singletons/config"
)

func cloneProjectAndPushConfig(clientProject *httpClient.Project, location, description, user string, embedToken bool) error {
	// Build location to clone the project, either to cwd/<project name> or providedLoc/<project name>
	if len(location) == 0 {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		location = path.Join(cwd, clientProject.Name)

		// Check if user has already defined project name in given location
	} else if !strings.HasSuffix(strings.ToLower(location), strings.ToLower(clientProject.Name)) {
		location = path.Join(location, clientProject.Name)
	}

	// Set new project in config ~/tau.yaml
	configProject := config.Project{
		DefaultProfile: user,
		Location:       location,
	}
	err := config.Projects().Set(clientProject.Name, configProject)
	if err != nil {
		return err
	}

	// Clone project to given location
	projectRepository, err := Repository(clientProject.Name).Clone(configProject, embedToken)
	if err != nil {
		return fmt.Errorf("failed to clone %s with %w", clientProject.Name, err)
	}

	// Get go-project-schema project for config access
	projectIface, err := SelectedProjectInterface()
	if err != nil {
		return err
	}

	// Get GitEmail from profile
	profile, err := config.Profiles().Get(user)
	if err != nil {
		return err
	}

	err = projectIface.Set(true,
		project.Id(clientProject.Id),
		project.Name(clientProject.Name),
		project.Description(description),
		project.Email(profile.GitEmail),
	)
	if err != nil {
		return err
	}

	// Get the config repository commit and push
	gitRepo, err := projectRepository.Config()
	if err != nil {
		return err
	}

	err = gitRepo.Commit("init", ".")
	if err != nil {
		return err
	}

	return gitRepo.Push()
}
