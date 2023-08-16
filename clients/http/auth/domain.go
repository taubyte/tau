package client

import "fmt"

// RegisterDomain returns information for creating a CNAME record
func (c *Client) RegisterDomain(fqdn, projectId string) (response DomainResponse, err error) {
	err = c.http.Post("/domain/"+fqdn+"/for/"+projectId, nil, &response)
	if err != nil {
		err = fmt.Errorf("register domain `%s` failed with: %s", fqdn, err)
	}

	return
}
