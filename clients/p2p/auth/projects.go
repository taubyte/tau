package auth

import (
	"fmt"

	iface "github.com/taubyte/tau/core/services/auth"
	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/utils/maps"
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

	configID, _ := maps.Int(obj, "config")
	codeID, _ := maps.Int(obj, "code")
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
