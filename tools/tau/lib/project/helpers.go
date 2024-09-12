package projectLib

import (
	httpClient "github.com/taubyte/tau/clients/http/auth"
	projectI18n "github.com/taubyte/tau/tools/tau/i18n/project"
	authClient "github.com/taubyte/tau/tools/tau/singletons/auth_client"
)

func projectByName(name string) (*httpClient.Project, error) {
	client, err := authClient.Load()
	if err != nil {
		return nil, err
	}

	projects, err := client.Projects()
	if err != nil {
		return nil, projectI18n.GettingProjectsFailed(err)
	}

	var project *httpClient.Project
	for _, _project := range projects {
		if _project.Name == name {
			project = _project
			break
		}
	}
	if project == nil {
		return nil, projectI18n.ProjectNotFound(name)
	}

	return project, nil
}
