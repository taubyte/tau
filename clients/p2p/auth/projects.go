package auth

import (
	"fmt"

	iface "github.com/taubyte/tau/core/services/auth"
	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/tau/utils/maps"
)

func (c *Client) Projects() iface.Projects {
	return (*Projects)(c)
}

func (p *Projects) Hooks() iface.Hooks {
	return (*Hooks)(p)
}

func (p *Projects) New(obj map[string]interface{}) *iface.Project {
	var prj iface.Project
	var err error
	prj.Id, err = maps.String(obj, "id")
	if err != nil {
		return nil
	}

	// The two repository ids are canonical keys, so there is one shape to read
	// them in. The ladder that used to be here — string, then int, then zero —
	// existed to reconcile a numeric id that arrived spelled either way, and a
	// key arrives spelled one way or is not a key.
	configID, _ := maps.String(obj, "config")
	codeID, _ := maps.String(obj, "code")

	prj.Git.Config = &GithubRepository{
		RepositoryCommon: RepositoryCommon{
			id: configID,
		},
	}
	prj.Git.Code = &GithubRepository{
		RepositoryCommon: RepositoryCommon{
			id: codeID,
		},
	}

	prj.Name, err = maps.String(obj, "name")
	if err != nil {
		return nil
	}

	prj.Provider, err = maps.String(obj, "provider")
	if err != nil {
		prj.Provider = "github"
	}

	return &prj
}

func (p *Projects) Get(project_id string) *iface.Project {
	logger.Debugf("Getting project `%s`", project_id)
	defer logger.Debugf("Getting project `%s` done", project_id)

	response, err := p.client.Send("projects", command.Body{"action": "get", "id": project_id}, p.peers...)
	if err != nil {
		return nil
	}

	return p.New(response)
}

func (p *Projects) List() ([]string, error) {
	response, err := p.client.Send("projects", command.Body{"action": "list"}, p.peers...)
	if err != nil {
		return nil, err
	}
	ids, err := maps.StringArray(response, "ids")
	if err != nil {
		return nil, fmt.Errorf("failed map string array on list error: %v", err)
	}
	return ids, nil
}

// Create creates a new project with the given name and repository IDs
func (p *Projects) Create(name, configRepoID, codeRepoID string) error {
	logger.Debugf("Creating project `%s` with config repo `%s` and code repo `%s`", name, configRepoID, codeRepoID)
	defer logger.Debugf("Creating project `%s` done", name)

	_, err := p.client.Send("projects", command.Body{
		"action": "create",
		"name":   name,
		"config": configRepoID,
		"code":   codeRepoID,
	}, p.peers...)
	if err != nil {
		return fmt.Errorf("failed to create project: %w", err)
	}

	return nil
}
