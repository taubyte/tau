//go:build ee

package auth

import (
	iface "github.com/taubyte/tau/core/services/auth"
	secretsPkg "github.com/taubyte/tau/ee/clients/p2p/auth/secrets"
)

// Secrets returns the Secrets interface for secret management
func (c *Client) Secrets() iface.Secrets {
	secrets, err := secretsPkg.New(c.node, c.client)
	if err != nil {
		// This should never happen in practice since node and client are set during Client creation
		// But we need to handle it to satisfy the interface
		return nil
	}
	return secrets
}
