package client

import (
	"fmt"
)

type registerRepositoryResponse struct {
	Key string `json:"key"`
}

// RegisterRepository registers a git repository with the auth server
func (c *Client) RegisterRepository(repoId string) error {
	response := registerRepositoryResponse{}
	err := c.Put("/repository/"+c.Provider()+"/"+repoId, nil, &response)
	if err != nil {
		return fmt.Errorf("registering repository `%s` failed with: %s", repoId, err)
	}

	if response.Key == "" {
		return fmt.Errorf("registering repository `%s` failed with: empty key", repoId)
	}

	return nil
}

// UnregisterRepository un-registers a git repository from the auth server
func (c *Client) UnregisterRepository(repoId string) error {
	err := c.Delete("/repository/"+c.Provider()+"/"+repoId, nil, nil)
	if err != nil {
		return fmt.Errorf("un-registering repository `%s` failed with: %s", repoId, err)
	}

	return nil
}
