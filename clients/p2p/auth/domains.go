package auth

import (
	"fmt"

	"github.com/taubyte/tau/core/services/auth"
	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/tau/utils/maps"
)

// RegisterDomain registers a domain for a project and returns domain validation information
func (c *Client) RegisterDomain(fqdn, projectID string) (*auth.DomainRegistration, error) {
	logger.Debugf("Registering domain `%s` for project `%s`", fqdn, projectID)
	defer logger.Debugf("Registering domain `%s` for project `%s` done", fqdn, projectID)

	response, err := c.client.Send("domain", command.Body{
		"action":  "register",
		"fqdn":    fqdn,
		"project": projectID,
	}, c.peers...)
	if err != nil {
		return nil, fmt.Errorf("failed to register domain: %w", err)
	}

	// Extract the response fields
	return &auth.DomainRegistration{
		Token: maps.TryString(response, "token"),
		Entry: maps.TryString(response, "entry"),
		Type:  maps.TryString(response, "type"),
	}, nil
}
