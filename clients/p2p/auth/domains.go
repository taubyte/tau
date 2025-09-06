package auth

import (
	"fmt"

	"github.com/taubyte/tau/p2p/streams/command"
)

// RegisterDomain registers a domain for a project and returns domain validation information
func (c *Client) RegisterDomain(fqdn, projectID string) (map[string]string, error) {
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
	result := make(map[string]string)
	if token, ok := response["token"].(string); ok {
		result["token"] = token
	}
	if entry, ok := response["entry"].(string); ok {
		result["entry"] = entry
	}
	if domainType, ok := response["type"].(string); ok {
		result["type"] = domainType
	}

	return result, nil
}
