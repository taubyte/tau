package p2p

import (
	"fmt"

	moodyCommon "github.com/taubyte/go-interfaces/moody"
	"github.com/taubyte/go-interfaces/p2p/streams"
	iface "github.com/taubyte/go-interfaces/services/auth"
	"github.com/taubyte/utils/maps"
)

/* projects */
type Projects Client

func (c *Client) Projects() iface.Projects {
	return (*Projects)(c)
}

func (p *Projects) Hooks() iface.Hooks {
	return p.Hooks()
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

	return &prj
}

func (p *Projects) Get(project_id string) *iface.Project {
	logger.Debug(moodyCommon.Object{"message": fmt.Sprintf("Getting project `%s`", project_id)})
	defer logger.Debug(moodyCommon.Object{"message": fmt.Sprintf("Getting project `%s` done", project_id)})

	response, err := p.client.Send("projects", streams.Body{"action": "get", "id": project_id})
	if err != nil {
		return nil
	}

	return p.New(response)
}

func (p *Projects) List() ([]string, error) {
	response, err := p.client.Send("projects", streams.Body{"action": "list"})
	if err != nil {
		return nil, err
	}
	ids, err := maps.StringArray(response, "ids")
	if err != nil {
		return nil, fmt.Errorf("Failed map string array on list error: %v", err)
	}
	return ids, nil
}
