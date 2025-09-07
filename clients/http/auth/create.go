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
