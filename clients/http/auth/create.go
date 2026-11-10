package client

import (
	"fmt"
)

// Create creates a new project with the registered config and code repository ids
func (p *Project) Create(c *Client, configRepoId string, codeRepoId string) error {
	// Affix client to project
	p.client = c

	sendData := CreateProjectData{
		Config: Repository{
			Id: configRepoId,
		},
		Code: Repository{
			Id: codeRepoId,
		},
	}

	err := p.client.http.Post("/project/new/"+p.Name, &sendData, &ProjectReturn{p})
	if err != nil {
		return fmt.Errorf("creating new project failed with: %s", err)
	}

	return nil
}

// Create creates a new device for the project
func (d *Device) Create(c *Client) error {
	err := c.http.Post(fmt.Sprintf("/project/%s/devices/new", d.Project.Id), d, d)
	if err != nil {
		return fmt.Errorf("creating new device failed with: %s", err)
	}

	return nil
}

// Import registers the project under projectID, the id in its config, instead
// of a new one. A project's builds are filed under its config's id, so a cloud
// that knew the project by another id listed none of its jobs and served none
// of its resources.
func (p *Project) Import(c *Client, projectID, configRepoId, codeRepoId string) error {
	p.client = c

	sendData := ImportProjectData{
		Config:    Repository{Id: configRepoId},
		Code:      Repository{Id: codeRepoId},
		ProjectID: projectID,
	}

	if err := p.client.http.Post("/project/import/"+p.Name, &sendData, &ProjectReturn{p}); err != nil {
		return fmt.Errorf("importing project failed with: %s", err)
	}

	return nil
}
