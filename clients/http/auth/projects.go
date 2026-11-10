package client

import (
	"fmt"
	"strings"

	"github.com/taubyte/tau/clients/http/auth/git/common"
)

// GetProjectById returns the project with the given id and an error
// Note: The repository field is not populated
func (c *Client) GetProjectById(projectId string) (*Project, error) {
	var data ProjectReturn
	err := c.http.Get("/projects/"+projectId, &data)
	if err != nil {
		return nil, err
	}

	return data.Project, nil
}

// GetProjectByIdWithCors returns the project with cors information with the given id and an error
func (c *Client) GetProjectByIdWithCors(projectId string) (*ProjectReturnWithCors, error) {
	data := new(ProjectReturnWithCors)
	err := c.http.Get("/projects/"+projectId, &data)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// ProjectsLast returns the last projects, newest first. A server too old to
// order them returns them all, unordered, and they are all kept: cutting an
// unordered list would hide arbitrary projects.
func (c *Client) ProjectsLast(last int) ([]*Project, error) {
	if last <= 0 {
		return c.Projects()
	}
	var data ProjectsReturn
	if err := c.http.Get(fmt.Sprintf("/projects?last=%d", last), &data); err != nil {
		return nil, err
	}
	if data.Newest && len(data.Projects) > last {
		data.Projects = data.Projects[:last]
	}
	for _, p := range data.Projects {
		p.client = c
	}
	return data.Projects, nil
}

// Projects returns a list of projects and an error
func (c *Client) Projects() ([]*Project, error) {
	var data ProjectsReturn
	err := c.http.Get("/projects", &data)
	if err != nil {
		return nil, err
	}

	projects := make([]*Project, len(data.Projects))

	// Affix the client to each project
	for idx, project := range data.Projects {
		_project := project
		_project.client = c
		projects[idx] = _project
	}

	return projects, nil
}

// Repositories will populate then return the repositories field and an error
func (p *Project) Repositories() (*RawRepoDataOuter, error) {
	if p.RepoList == nil {
		var response ProjectReturn
		err := p.client.http.Get("/projects/"+p.Id, &response)
		if err != nil {
			return nil, err
		}

		p.RepoList = response.Project.RepoList
	}

	return p.RepoList, nil
}

// Unfinished reports whether creating this project never finished. tau's last
// step is the config repository's first commit, so a project without one has
// nothing to lose by being finished. On any error it is not called unfinished.
func (p *Project) Unfinished() bool {
	repos, err := p.Repositories()
	if err != nil {
		return false
	}
	owner, name, ok := strings.Cut(repos.Configuration.Fullname, "/")
	if !ok {
		return false
	}
	empty, err := p.client.RepositoryEmpty(owner, name)
	return err == nil && empty
}

// Config returns the project configuration and an error
// The configuration is a "config.yaml" file in the root of the repository
func (p *Project) Config() (*common.ProjectConfig, error) {
	// Load repositories, this only loads them if they are not yet found
	_, err := p.Repositories()
	if err != nil {
		return nil, fmt.Errorf("loading repositories failed with: %s", err)
	}

	userAndName := strings.Split(p.RepoList.Configuration.Fullname, "/")
	if len(userAndName) < 2 {
		return nil, fmt.Errorf("invalid fullname: `%s` expected user/repo-name", p.RepoList.Configuration.Fullname)
	}

	return p.client.Git().ReadConfig(userAndName[0], userAndName[1])
}

type deleteResponse struct {
	Project struct {
		Id     string
		Status string
	}
}

// Delete deletes the project and returns an error
func (p *Project) Delete() (response deleteResponse, err error) {
	err = p.client.http.Delete("/projects/"+p.Id, nil, &response)
	return response, err
}
